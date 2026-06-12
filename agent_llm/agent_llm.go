// Package agent_llm is a base agent extended with common LLM prompts.
package agent_llm

import (
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/brianvoe/gofakeit/v7"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssbase "github.com/pancsta/secai/states"
	"gopkg.in/yaml.v3"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/agent_llm/db/sqlc"
	sa "github.com/pancsta/secai/agent_llm/schema"
	"github.com/pancsta/secai/agent_llm/states"
	"github.com/pancsta/secai/shared"
)

// TODO move
var ErrRetried = errors.New("retires reached")

// TODO config
var retires = 5

type S = am.S

var ss = states.AgentLLMStates

// ///// ///// /////

// ///// API

// ///// ///// /////

type ChildAPI interface {
	// LLMResources returns the params for the LLM prompt that generates resources, eg phrases.
	LLMResources() sa.ParamsGenResources
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

var _ shared.AgentQueries[sqlc.Queries] = &AgentLLM{}

// AgentLLM is [secai.AgentBase] extended with common LLM prompts, meant to be embedded in final agents.
type AgentLLM struct {
	*secai.AgentBase

	// config
	orientingThreshold float32

	// data

	Character     atomic.Pointer[sa.ResultGenCharacter]
	Resources     atomic.Pointer[sa.ResultGenResources]
	MoveOrienting atomic.Pointer[sa.ResultOrienting]

	// prompts

	PCheckingMenuRefs *sa.PromptCheckingMenuRefs
	PGenResources     *sa.PromptGenResources
	PGenCharacter     *sa.PromptGenCharacter
	POrienting        *sa.PromptOrienting
	PConfigTest       *sa.PromptConfigTest

	dbQueries         *sqlc.Queries
	DocCharacter      *shared.Document
	CharacterReadyMsg string
}

type AgentLLMDeps struct {
	LogArgs am.LogArgsMapperFn
	Groups  any
	States  am.States
	// RPC args for REPL
	ArgsREPL           []am.ArgsApi
	PromptGenCharacter *sa.PromptGenCharacter
	PromptGenResources *sa.PromptGenResources
	PromptOrienting    *sa.PromptOrienting
	// Certainty above which the orienting move should be accepted.
	OrientingMoveThreshold float32
	// Initial msg, optional, " " to disable.
	CharacterReadyMsg string
}

func New(ctx context.Context, states am.S, schema am.Schema) *AgentLLM {
	// init the agent along with the base
	return &AgentLLM{
		AgentBase: secai.NewAgent(ctx, states, schema),
	}
}

func (a *AgentLLM) Init(
	agentImpl shared.AgentAPI, cfg *shared.Config, deps AgentLLMDeps,
) error {

	// call super
	err := a.AgentBase.Init(agentImpl, cfg, deps.LogArgs, deps.Groups, deps.States, slices.Concat(deps.ArgsREPL, ArgsRPC))
	if err != nil {
		return err
	}

	// prompts
	a.PCheckingMenuRefs = sa.NewPromptCheckingMenuRefs(a)
	a.PConfigTest = sa.NewPromptConfigTest(a)

	// custom prompts
	a.PGenCharacter = deps.PromptGenCharacter
	a.PGenResources = deps.PromptGenResources
	a.POrienting = deps.PromptOrienting

	// config
	a.orientingThreshold = deps.OrientingMoveThreshold
	if a.orientingThreshold == 0 {
		a.orientingThreshold = 0.8
	}
	a.CharacterReadyMsg = deps.CharacterReadyMsg
	if a.CharacterReadyMsg == "" {
		a.CharacterReadyMsg = "Your host will be %s from %d. Profession: %s."
	}

	return nil
}

// Phrase returns a random phrase from resources under [key], or an empty string.
// TODO move to base agent
func (a *AgentLLM) Phrase(key string, args ...any) string {
	r := a.Resources.Load()
	if r == nil || len(r.Phrases[key]) == 0 {
		return ""
	}
	txt := r.Phrases[key][rand.Intn(len(r.Phrases[key]))]

	return fmt.Sprintf(txt, args...)
}

// OutputPhrase is sugar for Phrase followed by Output FromAssistant.
func (a *AgentLLM) OutputPhrase(key string, args ...any) error {
	txt := a.Phrase(key, args...)
	if txt != "" {
		a.Log("output phrase", "key", key)
		a.Output(txt, shared.FromAssistant)
		return nil
	}

	return fmt.Errorf("phrase not found: %s", key)
}

func (a *AgentLLM) Queries() *sqlc.Queries {
	if a.dbQueries == nil {
		a.dbQueries = sqlc.New(a.DbConn)
	}

	return a.dbQueries
}

func (a *AgentLLM) MemoryWipe(ctx context.Context, e *am.Event) {
	mach := a.Mach()
	err := a.Queries().DeleteAllCharacter(ctx)
	mach.EvAddErrState(e, ss.ErrDB, err, nil)
	err = a.Queries().DeleteAllResources(ctx)
	mach.EvAddErrState(e, ss.ErrDB, err, nil)
}

func IsStory(state string) bool {
	return strings.HasPrefix(state, ssbase.PrefixStory) && state != ss.StoryChanged &&
		!strings.HasPrefix(state, ssbase.PrefixStoryDisable) && state != ss.StoryAction
}

// private

func (a *AgentLLM) child() ChildAPI {
	return a.AgentImpl().(ChildAPI)
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

var _ = ss.ConfigValidating

func (a *AgentLLM) ConfigValidatingState(e *am.Event) {
	ctx := a.Mach().NewStateCtx(ss.ConfigValidating)
	a.Mach().Fork(ctx, e, func() {
		_, err := a.PConfigTest.Exec(e, struct{}{})
		a.Mach().EvAddErr(e, err, nil)
	})
}

var _ = ss.ConfigUpdate

func (a *AgentLLM) ConfigUpdateState(e *am.Event) {
	// call super
	a.AgentBase.ConfigUpdateState(e)
	// first AI state
	a.Mach().EvRemove(e, am.S{ss.GenCharacter}, nil)
}

var _ = ss.CheckingMenuRefs

func (a *AgentLLM) CheckingMenuRefsState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.CheckingMenuRefs)
	args := am.ParseArgs[shared.ACheckingMenuRefs](e.Args)

	prompt := args.Prompt
	choices := a.OfferList
	if len(args.Choices) > 0 {
		choices = args.Choices
	}
	retCh := args.RetOfferRef
	llm := a.PCheckingMenuRefs

	// unblock
	mach.Fork(ctx, e, func() {
		for range retires {
			// deferred chan return
			var ret *shared.OfferRef
			defer func() {
				retCh <- ret
			}()

			foundFn := func(i int) *shared.OfferRef {
				if i >= len(choices) {
					return nil
				}
				text := choices[i]
				return &shared.OfferRef{
					Index: i,
					Text:  shared.RemoveStyling(text),
				}
			}

			// infer locally (from 1-based to 0-based)
			i := shared.NumRef(prompt)
			if i >= 0 && i <= len(choices) {
				ret = foundFn(i - 1)
				return // retCh
			}

			if !args.CheckLLM {
				return
			}

			// infer via LLM
			params := sa.ParamsCheckingMenuRefs{
				Choices: shared.Map(choices, func(o string) string {
					return shared.RemoveStyling(o)
				}),
				Prompt: args.Prompt,
			}
			res, err := llm.Exec(e, params)
			if err != nil {
				a.Mach().AddErr(err, nil)
				continue
			}
			if res.RefIndex >= 0 && res.RefIndex < len(choices) {
				ret = foundFn(res.RefIndex)
				return // retCh
			}
		}

		a.LogErr(ss.CheckingMenuRefs, ErrRetried, "num", retires)
	})
}

var _ = ss.ResourcesReady

func (a *AgentLLM) ResourcesReadyEnd(e *am.Event) {
	a.Resources.Store(nil)
}

var _ = ss.GenResources

func (a *AgentLLM) GenResourcesEnter(e *am.Event) bool {
	return a.Resources.Load() == nil
}

func (a *AgentLLM) GenResourcesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenResources)
	llm := a.PGenResources

	params := a.AgentImpl().(ChildAPI).LLMResources()
	if len(params.Phrases) == 0 {
		// next
		mach.EvAdd1(e, ss.ResourcesReady, nil)
		return
	}

	// unblock
	mach.Fork(ctx, e, func() {
		for range retires {
			// run the prompt (checks ctx)
			res, err := llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				continue
			}

			// persist
			for key, phrases := range res.Phrases {
				for _, p := range phrases {
					// TODO base queries
					_, err := a.Queries().AddResource(ctx, sqlc.AddResourceParams{
						Key:   key,
						Value: p,
					})
					if err != nil {
						mach.EvAddErrState(e, ss.ErrDB, err, nil)
						break
					}
				}
			}
			a.Resources.Store(res)

			// next
			mach.EvAdd1(e, ss.ResourcesReady, nil)
			return
		}

		a.LogErr(ss.GenResources, ErrRetried, "num", retires)
	})
}

var _ = ss.Orienting

func (a *AgentLLM) OrientingState(e *am.Event) {
	mach := a.Mach()
	// use multi-state context here on purpose
	ctx := mach.NewStateCtx(ss.Orienting)
	tick := mach.Tick(ss.Orienting)
	llm := a.POrienting
	cookSchema := a.Mach().Schema()
	prompt := am.ParseArgs[shared.APrompt](e.Args).Prompt

	// possible moves: all cooking steps, most stories and some states

	// moves from stories
	movesStories := map[string]string{}
	for _, name := range mach.StateNames() {
		state := cookSchema[name]

		isStory := IsStory(name)
		isTrigger := amhelp.TagValue(state.Tags, ssbase.TagTrigger) != ""
		isManual := amhelp.TagValue(state.Tags, ssbase.TagManual) != ""
		// TODO reflect godoc?
		desc := ""
		if isStory {
			// TODO unsafe
			story := a.AgentImpl().Story(name)
			desc = story.Desc
		}

		if isTrigger || (isStory && !isManual) {
			impossible := amhelp.CantAdd1(mach, name, nil)
			if !impossible {
				movesStories[name] = desc
			}
		}
	}

	// build params
	params := sa.ParamsOrienting{
		Prompt:        prompt,
		MovesWorkflow: a.AgentImpl().OrientingMoves(),
		// TODO desc
		MovesStories: movesStories,
	}

	// unblock
	mach.Fork(ctx, e, func() {
		// check tail
		defer func() {
			if tick != mach.Tick(ss.Orienting) {
				return
			}
			mach.EvRemove1(e, ss.Orienting, nil)
		}()

		// run the prompt (checks ctx)
		resp, err := llm.Exec(e, params)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrAI, err, nil)
			return
		}

		if resp.Certainty < a.orientingThreshold {
			return
		}
		if tick != mach.Tick(ss.Orienting) {
			return
		}

		// store
		a.MoveOrienting.Store(resp)
	})
}

var _ = ss.OrientingMove

func (a *AgentLLM) OrientingMoveEnter(e *am.Event) bool {
	args := am.ParseArgs[AOrientingMove](e.Args)
	return args.Move != nil
}

func (a *AgentLLM) OrientingMoveState(e *am.Event) {
	mach := a.Mach()
	mem := a.AgentImpl().MachMem()
	ctx := mach.NewStateCtx(ss.OrientingMove)
	move := am.ParseArgs[AOrientingMove](e.Args).Move

	mach.Fork(ctx, e, func() {
		defer mach.EvRemove1(e, ss.OrientingMove, nil)

		// dispatch the mutation
		m := move.Move
		var res am.Result
		if mem != nil && mem.Has1(m) {
			if res = mem.EvAdd1(e, m, nil); res == am.Canceled {
				a.Log("mem canceled", "move", m)
			}

		} else if s := a.AgentImpl().Story(m); s != nil {
			if res = a.StoryActivate(e, m); res == am.Canceled {
				a.Log("story canceled", "move", m)
			}

		} else if mach.Has1(m) {
			if res = mach.EvAdd1(e, m, nil); res == am.Canceled {
				a.Log("move canceled", "move", m)
			}
		}

		// TODO timeout
		<-mach.WhenQueue(res)
	})
}

var _ = ss.RestoreCharacter

func (a *AgentLLM) RestoreCharacterState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreCharacter)

	mach.Fork(ctx, e, func() {
		dbChar, err := a.Queries().GetCharacter(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}

		// generate a new character
		if dbChar.Result == "" {
			mach.EvAdd1(e, ss.GenCharacter, nil)
			return
		}

		// unmarshal JSON result into ResultGenCharacter
		var res sa.ResultGenCharacter
		if err := json.Unmarshal([]byte(dbChar.Result), &res); err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		// store and next
		a.Character.Store(&res)
		mach.EvAdd1(e, ss.CharacterReady, nil)
	})
}

var _ = ss.GenCharacter

func (a *AgentLLM) GenCharacterState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenCharacter)
	llm := a.PGenCharacter

	// unblock
	mach.Fork(ctx, e, func() {
		for range retires {
			// run the prompt (checks ctx)
			// TODO gen profession via LLM (age accurate)
			params := sa.ParamsGenCharacter{
				CharacterProfession: gofakeit.JobTitle(),
				CharacterYear:       rand.Intn(120) + 1900,
			}
			res, err := llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				continue
			}

			// persist
			jResult, _ := json.Marshal(res)
			_, err = a.Queries().AddCharacter(ctx, string(jResult))
			if err != nil {
				mach.EvAddErrState(e, ss.ErrDB, err, nil)
				// DB err is OK
			}
			a.Character.Store(res)

			// next
			mach.EvAdd1(e, ss.CharacterReady, nil)
			return
		}

		a.LogErr(ss.GenCharacter, ErrRetried, "num", retires)
	})
}

func (a *AgentLLM) GenCharacterEnd(e *am.Event) {
	a.PGenCharacter.HistClean()
}

var _ = ss.CharacterReady

func (a *AgentLLM) CharacterReadyEnter(e *am.Event) bool {
	return a.Character.Load() != nil
}

func (a *AgentLLM) CharacterReadyState(e *am.Event) {
	char := a.Character.Load()
	j, _ := yaml.Marshal(char)

	// attach to prompts which depend on the character
	a.DocCharacter = shared.NewDocument("Character", string(j))
	a.DocCharacter.AddToPrompts(a.PGenResources, a.POrienting)

	// greeting msg
	msg := a.CharacterReadyMsg
	if strings.TrimSpace(msg) != "" {
		msg = fmt.Sprintf(msg, char.Name, char.Year, char.Profession)
		a.Output(msg, shared.FromNarrator)
	}
	_ = a.OutputPhrase("CharacterReady")
}

func (a *AgentLLM) CharacterReadyEnd(e *am.Event) {
	a.Character.Store(nil)
}

var _ = ss.RestoreResources

func (a *AgentLLM) RestoreResourcesState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreResources)

	mach.Fork(ctx, e, func() {
		// restore TODO fix restoring
		dbRes, err := a.Queries().GetResources(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}
		if len(dbRes) == 0 {
			mach.EvAdd1(e, ss.GenResources, nil)
		} else {

			res := &sa.ResultGenResources{
				Phrases: make(map[string][]string),
			}
			for _, r := range dbRes {
				res.Phrases[r.Key] = append(res.Phrases[r.Key], r.Value)
			}
			a.Resources.Store(res)

			// next
			mach.EvAdd1(e, ss.ResourcesReady, nil)
		}
	})
}

var _ = ss.GenResources

func (a *AgentLLM) GenResourcesEnd(e *am.Event) {
	a.PGenResources.HistClean()
}

// TODO PromptState(e) which checks for unexpected msgs (no InputPending) and compares all states

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// APrefix is the args prefix, set from config.
var APrefix = "secaillm"

type Args struct {
	am.ArgsBase
}

func (Args) ArgsPrefix() string {
	return APrefix
}

// -----

type AOrientingMove struct {
	Args
	Move *sa.ResultOrienting `log:"move"`
}

func (AOrientingMove) ArgsState() string {
	return ss.OrientingMove
}

// ----- RPC boilerplate

func init() {
	for _, arg := range ArgsRPC {
		gob.Register(arg)
	}
}

var ArgsRPC = []am.ArgsApi{AOrientingMove{}}
