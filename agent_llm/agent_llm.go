// Package agent_llm is a base agent extended with common LLM prompts.
package agent_llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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

	dbQueries    *sqlc.Queries
	DocCharacter *secai.Document
}

func New(ctx context.Context, states am.S, schema am.Schema) *AgentLLM {
	// init the agent along with the base
	return &AgentLLM{
		AgentBase: secai.NewAgent(ctx, states, schema),
	}
}

func (a *AgentLLM) Init(
	agentImpl shared.AgentAPI, cfg *shared.Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args any,
) error {

	// call super
	err := a.AgentBase.Init(agentImpl, cfg, logArgs, groups, states, args)
	if err != nil {
		return err
	}

	a.PCheckingMenuRefs = sa.NewPromptCheckingMenuRefs(a)
	a.PConfigTest = sa.NewPromptConfigTest(a)
	a.PGenCharacter = sa.NewPromptGenCharacter(a)
	a.PGenResources = sa.NewPromptGenResources(a)
	a.POrienting = sa.NewPromptOrienting(a)

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

// private

func (a *AgentLLM) child() ChildAPI {
	return a.AgentImpl().(ChildAPI)
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (a *AgentLLM) ConfigValidatingState(e *am.Event) {
	_, err := a.PConfigTest.Exec(e, struct{}{})
	a.Mach().EvAddErr(e, err, nil)
}

func (a *AgentLLM) ConfigUpdateState(e *am.Event) {
	// call super
	a.AgentBase.ConfigUpdateState(e)
	// first AI state
	a.Mach().EvRemove(e, am.S{ss.GenCharacter}, nil)
}

func (a *AgentLLM) CheckingMenuRefsState(e *am.Event) {
	args := shared.ParseArgs(e.Args)

	prompt := args.Prompt
	choices := a.OfferList
	if len(args.Choices) > 0 {
		choices = args.Choices
	}
	retCh := args.RetOfferRef
	llm := a.PCheckingMenuRefs

	// unblock
	go func() {
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
			return
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
			return
		}
		if res.RefIndex >= 0 && res.RefIndex < len(choices) {
			ret = foundFn(res.RefIndex)
			return
		}
	}()
}

func (a *AgentLLM) ResourcesReadyEnd(e *am.Event) {
	a.Resources.Store(nil)
}

func (a *AgentLLM) GenResourcesEnter(e *am.Event) bool {
	return a.Resources.Load() == nil
}

func (a *AgentLLM) GenResourcesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenResources)
	llm := a.PGenResources

	params := a.AgentImpl().(ChildAPI).LLMResources()

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// run the prompt (checks ctx)
		res, err := llm.Exec(e, params)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrAI, err, nil)
			return
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
	}()
}

func (a *AgentLLM) OrientingState(e *am.Event) {
	mach := a.Mach()
	// use multi-state context here on purpose
	ctx := mach.NewStateCtx(ss.Orienting)
	tick := mach.Tick(ss.Orienting)
	llm := a.POrienting
	cookSchema := a.Mach().Schema()
	prompt := ParseArgs(e.Args).Prompt

	// possible moves: all cooking steps, most stories and some states

	// moves from stories
	movesStories := map[string]string{}
	for _, name := range mach.StateNames() {
		state := cookSchema[name]

		// TODO extract
		isStory := strings.HasPrefix(name, "Story") && name != ss.StoryChanged && name != ss.StoryAction
		isTrigger := amhelp.TagValue(state.Tags, ssbase.TagTrigger) != ""
		isManual := amhelp.TagValue(state.Tags, ssbase.TagManual) != ""
		// TODO reflect godoc?
		desc := ""
		if isStory {
			// TODO unsafe
			desc = a.AgentImpl().Story(name).Desc
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
		Prompt:     prompt,
		MovesAgent: a.AgentImpl().OrientingMoves(),
		// TODO desc
		MovesStories: movesStories,
	}

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
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

		if resp.Certainty < 0.8 {
			return
		}
		if tick != mach.Tick(ss.Orienting) {
			return
		}

		// store
		a.MoveOrienting.Store(resp)
	}()
}

func (a *AgentLLM) OrientingMoveEnter(e *am.Event) bool {
	args := ParseArgs(e.Args)
	return args.Move != nil
}

func (a *AgentLLM) OrientingMoveState(e *am.Event) {
	mach := a.Mach()
	mem := a.AgentImpl().MachMem()
	defer mach.Remove1(ss.OrientingMove, nil)
	args := ParseArgs(e.Args)
	move := args.Move
	resCh := args.ResultCh

	// dispatch the mutation
	m := move.Move
	var res am.Result
	if mem.Has1(m) {
		res = mem.Add1(m, nil)
		if res == am.Canceled {
			a.Log("2", "move", m)
		}

	} else if s := a.AgentImpl().Story(m); s != nil {
		res = a.StoryActivate(e, m)
		if res == am.Canceled {
			a.Log("story canceled", "move", m)
		}

	} else if mach.Has1(m) {
		res = mach.Add1(m, nil)
		if res == am.Canceled {
			a.Log("move canceled", "move", m)
		}
	}

	// optionally return the result
	if args.ResultCh == nil || cap(args.ResultCh) < 1 {
		return
	}

	// channel back (buf)
	select {
	case resCh <- res:
	default:
		mach.Log("OrientingMove chan closed")
	}
}

func (a *AgentLLM) RestoreCharacterState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreCharacter)

	go func() {
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
	}()
}

func (a *AgentLLM) GenCharacterState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenCharacter)
	llm := a.PGenCharacter

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

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
			return
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
	}()
}

func (a *AgentLLM) GenCharacterEnd(e *am.Event) {
	a.PGenCharacter.HistClean()
}

func (a *AgentLLM) CharacterReadyEnter(e *am.Event) bool {
	return a.Character.Load() != nil
}

func (a *AgentLLM) CharacterReadyState(e *am.Event) {
	char := a.Character.Load()
	j, _ := yaml.Marshal(char)

	// attach to prompts which depend on the character
	a.DocCharacter = secai.NewDocument("Character", string(j))
	a.DocCharacter.AddToPrompts(a.PGenResources, a.POrienting)

	msg := fmt.Sprintf("Your host will be %s from %d. Profession: %s.", char.Name, char.Year, char.Profession)
	a.Output(msg, shared.FromNarrator)
	_ = a.OutputPhrase("CharacterReady")
}

func (a *AgentLLM) CharacterReadyEnd(e *am.Event) {
	a.Character.Store(nil)
}

func (a *AgentLLM) RestoreResourcesState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreResources)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// restore
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
	}()
}

func (a *AgentLLM) GenResourcesEnd(e *am.Event) {
	a.PGenResources.HistClean()
}

// TODO PromptState(e) which checks for unexpected msgs (no InputPending) and compares all states

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// aliases

type A2 = shared.A
type A2RPC = shared.ARPC

var Pass2 = shared.Pass
var Pass2RPC = shared.PassRPC

// APrefix is the args prefix, set from config.
var APrefix = "secaillm"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *sa.ResultOrienting `log:"move"`

	// agent's non-RPC args

	// TODO
}

func NewArgs() A {
	return A{A: &shared.A{}}
}

func NewArgsRPC() ARPC {
	return ARPC{A: &shared.A{}}
}

// ARPC is a subset of [A] that can be passed over RPC (eg no channels, conns, etc)
type ARPC struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *sa.ResultOrienting `log:"move"`
}

// ParseArgs extracts A from [am.Event.Args][APrefix] (decoder).
func ParseArgs(args am.A) *A {
	// RPC-only args (pointer)
	if r, ok := args[APrefix].(*ARPC); ok {
		a := amhelp.ArgsToArgs(r, &A{})
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// RPC-only args (value, eg from a network transport)
	if r, ok := args[APrefix].(ARPC); ok {
		a := amhelp.ArgsToArgs(&r, &A{})
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// regular args (pointer)
	if a, _ := args[APrefix].(*A); a != nil {
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// defaults
	return &A{
		A: shared.ParseArgs(args),
	}
}

// Pass prepares [am.A] from A to be passed to further mutations (encoder).
func Pass(args *A) am.A {
	// dont nest in plain maps
	clone := *args
	clone.A = nil
	// ref the clone
	out := am.A{APrefix: &clone}

	// merge with base args
	return am.AMerge(out, shared.Pass(args.A))
}

// PassRPC is a network-safe version of Pass. Use it when mutating aRPC workers.
func PassRPC(args *A) am.A {
	// dont nest in plain maps
	clone := *amhelp.ArgsToArgs(args, &ARPC{})
	clone.A = nil
	out := am.A{APrefix: clone}

	// merge with base args
	return am.AMerge(out, shared.PassRPC(args.A))
}

// LogArgs is an args logger for A and [secai.A].
func LogArgs(args am.A) map[string]string {
	a1 := shared.ParseArgs(args)
	a2 := ParseArgs(args)
	if a1 == nil && a2 == nil {
		return nil
	}

	return am.AMerge(amhelp.ArgsToLogMap(a1, 0), amhelp.ArgsToLogMap(a2, 0))
}
