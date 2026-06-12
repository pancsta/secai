// Package cook is a recipe-choosing and cooking agent with a gen-ai character.
package cook

import (
	"context"
	"database/sql"
	"embed"
	"encoding/gob"
	"fmt"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/google/go-cmp/cmp"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	agentllm "github.com/pancsta/secai/agent_llm"
	sallm "github.com/pancsta/secai/agent_llm/schema"
	"github.com/pancsta/secai/examples/cook/db/sqlc"
	sa "github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/examples/cook/states"
	"github.com/pancsta/secai/shared"
	ssbase "github.com/pancsta/secai/states"
	"github.com/pancsta/secai/tools/searxng"
	"github.com/pancsta/secai/tui"
	"github.com/pancsta/secai/web"
	ssp "github.com/pancsta/secai/web/browser/states"
)

// Mock will run a sample scenario
type Mock struct {
	// Local mock switch, complementary to the config switch.
	Active bool

	FlowPromptIngredients string
	FlowPromptRecipe      string
	FlowPromptCooking     string

	Recipe                    string
	GenStepsRes               string
	GenStepCommentsRes        string
	StoryCookingStartedInput  string
	StoryCookingStartedInput3 string
}

var mock = Mock{
	// DEBUG
	Active: true,

	FlowPromptIngredients: "I have 2 carrots, 3 eggs and rice",
	FlowPromptRecipe:      "1",
	// FlowPromptCooking:     "wipe your memory",
	// FlowPromptRecipe: "egg fried rice",

	// TODO MockDump state for dumping mocked fields
	//  output to files in SECAI_DIR

	// start from mocked StoryCookingStarted
	// Recipe:             `{"Name":"Carrot and Egg Fried Rice","Desc":"A simple yet delightful dish that combines the sweetness of carrots with the richness of eggs, all tossed with fluffy rice.","Steps":"1. Cook the rice and set aside. 2. Scramble the eggs in a pan and set aside. 3. Sauté the carrots until tender. 4. Combine all ingredients in the pan and stir-fry with a bit of soy sauce.","ImageURL":"https://example.com/carrot-egg-fried-rice.jpg"}`,
	// GenStepsRes:        `{"Schema":{"CarrotsSauteed":{"remove":["CarrotsSauteing"],"tags":["idx:3","final"]},"CarrotsSauteing":{"remove":["CarrotsSauteed"],"tags":["idx:3"]},"EggsScrambled":{"remove":["EggsScrambling"],"tags":["idx:2","final"]},"EggsScrambling":{"remove":["EggsScrambled"],"tags":["idx:2"]},"IngredientsCombining":{"require":["RiceCooked","EggsScrambled","CarrotsSauteed"],"tags":["idx:4"]},"MealReady":{"auto":true,"require":["IngredientsCombining"]},"RiceCooked":{"remove":["RiceCooking"],"tags":["idx:1","final"]},"RiceCooking":{"remove":["RiceCooked"],"tags":["idx:1"]}}}`,
	// GenStepCommentsRes: `{"Comments":["Ah, starting with the basics—cooking the rice. Remember, folks, the key to perfect fried rice is using day-old rice. It's drier and won't turn your dish into a mushy mess!","Scrambling eggs might seem simple, but don't rush it! A gentle touch ensures they're fluffy and not rubbery. And hey, a pinch of salt never hurt anybody!","Now, sautéing those carrots—let's get that natural sweetness shining through. A little patience here means a lot of flavor later. And who doesn't love a bit of color in their dish?","The grand finale! Tossing everything together with a splash of soy sauce. This is where the magic happens. Keep that pan hot and those ingredients moving for that authentic fried rice charm!"]}`,
	// StoryCookingStartedInput: "rice is cooked",
	// StoryCookingStartedInput3: "start again",
	// StoryCookingStartedInput3: "wipe memory clean",
	// define by amhelp.Cond like whenInpPen3
}

// TODO
// var whenInpPen3 = amhelp.Cond{
// 	Clock: am.Clock{
// 		ss.InputPending: 3,
// 	},
// }

var ss = states.CookStates
var SAdd = am.SAdd
var ssP = ssp.PageStates

// EnvCookDev enabled dev mode (local config and data dir).
var EnvCookDev = "COOK_DEV"

type S = am.S

var WelcomeMessage = "Please wait while loading..."

//go:embed web
var webAssets embed.FS

//go:embed config.tpl.kdl
var ConfigTpl []byte

// ///// ///// /////

// ///// CONFIG

// ///// ///// /////

type Config struct {
	shared.Config

	Cook ConfigCook
}

type ConfigCook struct {
	// Number of ingredients to collect.
	MinIngredients int
	GenJokesAmount int
	// TODO move to secai
	SessionTimeout time.Duration `kdl:",duration"`
	// Number of recipes to generate.
	GenRecipes int
	// TODO remove?
	MinPromptLen int
	// Step comment frequency. Lower number = higher frequency of step comments. 2=50%, 3=33%.
	StepCommentFreq int
	// Heartbeat frequency.
	HeartbeatFreq time.Duration `kdl:",duration"`
	// Certainty above which the orienting move should be accepted.
	OrientingMoveThreshold float64
}

func ConfigDefault() Config {
	cfg := Config{
		Config: shared.ConfigDefault(),
		Cook: ConfigCook{
			MinIngredients:         3,
			GenJokesAmount:         3,
			SessionTimeout:         time.Hour,
			GenRecipes:             3,
			MinPromptLen:           2,
			StepCommentFreq:        2,
			HeartbeatFreq:          time.Hour,
			OrientingMoveThreshold: 0.5,
		},
	}
	cfg.Agent.ID = "cook"
	cfg.Agent.Dir = "tmp-cook"

	return cfg
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

type Agent struct {
	// inherit from AgentLLM
	*agentllm.AgentLLM

	// public

	Config    *Config
	MemCutoff atomic.Uint64

	storiesOrder []string
	stories      map[string]*shared.Story
	tuis         []*tui.TUI
	msgs         []*shared.Msg
	tuiNum       int

	// DB

	dbConn       *sql.DB
	dbQueries    *sqlc.Queries
	jokes        atomic.Pointer[sa.ResultGenJokes]
	recipe       atomic.Pointer[sa.Recipe]
	stepComments atomic.Pointer[sa.ResultGenStepComments]
	ingredients  atomic.Pointer[[]sa.Ingredient]

	// machs

	mem *am.Machine

	// tools

	tSearxng *searxng.Tool

	// prompts

	pGenJokes           *sa.PromptGenJokes
	pIngredientsPicking *sa.PromptIngredientsPicking
	pRecipePicking      *sa.PromptRecipePicking
	pGenSteps           *sa.PromptGenSteps
	pGenStepComments    *sa.PromptGenStepComments
	pCookingStarted     *sa.PromptCookingStarted

	// internals

	srvUI           *ssh.Server
	loop            *amhelp.StateLoop
	loopCooking     *amhelp.StateLoop
	loopIngredients *amhelp.StateLoop
	loopRecipe      *amhelp.StateLoop
	// reqLimitOk is an LLM request limiter guard.
	reqLimitOk   atomic.Bool
	preWakeupSum uint64
	// the last msg was a no-jokes-without-cooking
	jokeRefusedMsg   bool
	orientingPending bool
	lastStoryCheck   uint64
	handlersWeb      *Web
	clockService     *tui.ClockService
	lastActions      []shared.ActionInfo
	lastStories      []shared.StoryInfo

	// pSearchingLLM *secai.Prompt[sa.ParamsSearching, sa.ResultSearching]
	// pAnswering    *secai.Prompt[sa.ParamsAnswering, sa.ResultAnswering]
}

var _ shared.AgentAPI = &Agent{}
var _ agentllm.ChildAPI = &Agent{}
var _ shared.AgentQueries[sqlc.Queries] = &Agent{}

// NewCook returns a preconfigured instance of Agent.
func NewCook(ctx context.Context, cfg *Config) (*Agent, error) {
	// TODO take CLI params
	a := New(ctx)
	if err := a.Init(cfg); err != nil {
		return nil, err
	}

	return a, nil
}

// New returns a custom instance of Agent.
func New(ctx context.Context) *Agent {
	a := &Agent{
		AgentLLM: agentllm.New(ctx, ss.Names(), states.CookSchema),
	}

	// defaults
	a.jokes.Store(&sa.ResultGenJokes{})
	a.recipe.Store(&sa.Recipe{})
	a.ingredients.Store(&[]sa.Ingredient{})
	a.reqLimitOk.Store(true)

	// predefined msgs
	a.msgs = append(a.msgs, shared.NewMsg(WelcomeMessage, shared.FromSystem))

	a.Store().Web = webAssets

	return a
}

func (a *Agent) Init(cfg *Config) error {
	var err error

	APrefix = cfg.Agent.ID
	a.Config = cfg

	// call super
	err = a.AgentLLM.Init(a, &a.Config.Config, agentllm.AgentLLMDeps{
		LogArgs:            amhelp.LogArgsMapper,
		Groups:             states.CookGroups,
		States:             states.CookStates,
		ArgsREPL:           shared.ArgsRPC,
		PromptGenCharacter: sa.NewPromptGenCharacter(a),
		PromptGenResources: sa.NewPromptGenResources(a),
		PromptOrienting:    sa.NewPromptOrienting(a),
	})
	if err != nil {
		return err
	}
	mach := a.Mach()

	// mach.AddBreakpoint1(ss.Disposing, "", true)
	// mach.AddBreakpoint1(ss.Disposing, "", false)

	// loop guards
	a.loop = amhelp.NewStateLoop(mach, ss.Loop, nil)

	// init searxng - websearch tool
	// a.tSearxng, err = searxng.New(a)
	// if err != nil {
	// 	return err
	// }

	// init prompts
	a.pGenJokes = sa.NewPromptGenJokes(a)
	a.pIngredientsPicking = sa.NewPromptIngredientsPicking(a)
	a.pRecipePicking = sa.NewPromptRecipePicking(a)
	a.pCookingStarted = sa.NewPromptCookingStarted(a)
	a.pGenSteps = sa.NewPromptGenSteps(a)
	a.pGenStepComments = sa.NewPromptGenStepComments(a)

	// register tools
	// secai.ToolAddToPrompts(a.tSearxng, a.pSearchingLLM, a.pAnswering)

	// init memory
	err = a.initMem()
	if err != nil {
		return err
	}

	a.initStories()
	// TODO NewClockService
	a.clockService = &tui.ClockService{
		Cfg:       &a.Config.Config,
		Hist:      a.Hist,
		Agent:     mach,
		SeriesLen: 15,
		Height:    4,
	}
	_, err = mach.HandlersBind(a.clockService, am.BindOpts{
		Id: "tui.ClockService",
	})
	if err != nil {
		return err
	}

	return nil
}

func (a *Agent) Queries() *sqlc.Queries {
	if a.dbQueries == nil {
		a.dbQueries = sqlc.New(a.dbConn)
	}

	return a.dbQueries
}

func (a *Agent) DBAgent() *sql.DB {
	return a.dbConn
}

// HistoryStates returns a list of states to track in the history.
func (a *Agent) HistoryStates() S {
	trackedStates := slices.Clone(a.Mach().StateNames())
	trackedStates = slices.DeleteFunc(trackedStates, func(s string) bool {
		// dont track the global handler
		return s == ss.CheckStories ||
			// dont track UI and RemoteUI states
			strings.HasPrefix(s, "UI") || strings.HasPrefix(s, "RemoteUI") ||
			// no health states
			s == ss.Heartbeat || s == ss.Healthcheck
	})

	return trackedStates
}

func (a *Agent) Splash() string {
	cfg := a.Config
	lines := []string{}
	l := func(msg string, args ...any) {
		lines = append(lines, fmt.Sprintf(msg, args...))
	}
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}
	logFile := cfg.Agent.LogPath(false)
	logAddr := cfg.Web.ConfigWebLogAddr()
	binary := shared.BinaryPath(true)
	argCfg := ""
	if cfg.File != "config.kdl" {
		argCfg = " --config " + cfg.File
	}
	// single config in prod
	if cfg.ProdBuild {
		argCfg = ""
	}

	// dev env
	if !cfg.ProdBuild {
		binary = "env " + EnvCookDev + "=1 " + binary
	}

	// HEADER

	l("%s %s", cfg.Agent.Label, version)
	l("")

	// WEB

	if cfg.Web.Addr != "-1" {
		l("Web:")
		l("- %s", cfg.Web.DashURL())
		l("- %s", cfg.Web.AgentURL())
		l("")
	}

	// FILES

	l("Files:")
	l("- config: %s", cfg.File)
	l("- log:    %s", logFile)
	l("")

	// TUI

	if cfg.TUI.PortSSH > 0 {
		l("TUI:")
		if cfg.TUI.PortWeb > 0 {
			l("- http://%s:%d", cfg.TUI.Host, cfg.TUI.PortWeb)
		}
		l("- ssh %s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no", cfg.TUI.Host, cfg.TUI.PortSSH)
		l("")
	}

	// REPL

	if cfg.Debug.REPL {
		l("REPL:")
		if cfg.Debug.REPLWeb != -1 {
			l("- http://localhost:%d", cfg.Debug.REPLWeb)
		}
		l("- %s repl%s", binary, argCfg)
		l("")
	}

	// LOG

	l("Log:")
	if logAddr != "" {
		l("- http://%s", logAddr)
	}
	l("- %s log --tail%s", binary, argCfg)
	l("- tail -f %s/%s.jsonl -n 100 | fblog -d -x msg -x time -x level", cfg.Agent.Dir, cfg.Agent.ID)
	l("")

	// DEBUGGER

	if cfg.Debug.DBGEmbed && cfg.Debug.DBGAddr != "" {
		_, httpAddr, sshAddr, err := cfg.Debug.DbgAddrs()
		if err == nil {
			sshAddr2 := strings.Split(sshAddr, ":")
			l("Debugger:")
			if cfg.Debug.DBGEmbedWeb > 0 {
				l("- http://localhost:%d", cfg.Debug.DBGEmbedWeb)
			}
			l("- files: http://%s", httpAddr)
			l("- ssh %s -p %s -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no", sshAddr2[0], sshAddr2[1])
			l("")
		}
	}

	// DB

	addrBase, addrAgent, addrHistory := cfg.Web.DBAddrs()
	if addrBase != "" {
		l("DB:")
		l("- Base: http://%s", addrBase)
		l("- Agent: http://%s", addrAgent)
		l("- History: http://%s", addrHistory)
		l("")
	}

	// FOOTER

	l("https://AI-gents.work")
	l("")

	return strings.Join(lines, "\n")
}

func (a *Agent) MachSchema() (am.Schema, am.S) {
	return a.Mach().Schema(), a.Mach().StateNames()
}

func (a *Agent) Msgs() []*shared.Msg {
	return a.msgs
}

func (a *Agent) MachMem() *am.Machine {
	return a.mem
}

func (a *Agent) Stories() []shared.StoryInfo {
	var stories []shared.StoryInfo
	for _, key := range a.storiesOrder {
		s := a.stories[key]
		stories = append(stories, s.StoryInfo)
	}

	return stories
}

func (a *Agent) Story(state string) *shared.Story {
	s, ok := a.stories[state]
	if !ok {
		return nil
	}
	return s
}

func (a *Agent) Actions() []shared.ActionInfo {
	mach := a.Mach()
	var ret []shared.ActionInfo
	for _, key := range a.storiesOrder {
		s := a.stories[key]

		for i := range s.Actions {
			act := &s.Actions[i]
			info := shared.ActionInfo{
				ID:           act.ID,
				Label:        act.Label,
				Desc:         act.Desc,
				StateAdd:     act.StateAdd,
				StateRemove:  act.StateRemove,
				VisibleAgent: act.VisibleAgent.Check(mach),
				VisibleMem:   act.VisibleMem.Check(a.mem),
				LabelEnd:     act.LabelEnd,
				Pos:          act.Pos,
				PosInferred:  act.PosInferred,
			}
			if act.Value != nil {
				info.Value = act.Value()
			}
			if act.ValueEnd != nil {
				info.ValueEnd = act.ValueEnd()
			}
			if act.IsDisabled != nil {
				info.IsDisabled = act.IsDisabled()
			}
			if act.Action != nil {
				info.Action = true
			}

			ret = append(ret, info)
		}
	}

	a.ValFile(nil, "actions", ret, "")
	return ret
}

func (a *Agent) LLMResources() sallm.ParamsGenResources {
	return sa.LLMResources
}

func (a *Agent) OrientingMoves() map[string]string {
	ret := map[string]string{}

	// collect and filter cooking moves
	movesCooking := a.AgentImpl().MachMem().StateNames().FilterMatch(sa.MatchSteps)
	movesCooking = slices.DeleteFunc(movesCooking, func(state string) bool {
		return amhelp.CantAdd1(a.AgentImpl().MachMem(), state, nil)
	})

	for _, move := range movesCooking {
		// TODO desc
		ret[move] = ""
	}

	return ret
}

//

// private

//

func (a *Agent) initMem() error {
	// TODO cook's mem schema
	var err error
	mach := a.Mach()
	cfg := a.Config
	if a.mem != nil {
		a.MemCutoff.Add(a.mem.Time(nil).Sum(nil))
	}

	a.mem, err = am.NewCommon(mach.Context(), "memory-"+cfg.Agent.ID, ssbase.MemSchema,
		ssbase.MemStates.Names(), nil, mach, nil)
	if err != nil {
		return err
	}
	shared.MachTelemetry(a.mem, nil)
	// TODO REPL
	// if cfg.Debug.REPL {
	// 	opts := arpc.ReplOpts{
	// 		AddrDir:   cfg.Agent.Dir,
	// 		ArgsBase:      ARPC{},
	// 		ArgsParse: ParseRpc,
	// 	}
	// 	if err := arpc.MachRepl(a.mem, "", &opts); err != nil {
	// 		return err
	// 	}
	// }

	// update stories memory change (via basic OnChange)
	a.mem.OnChange(func(mach *am.Machine, before, after am.Time) {
		a.hRenderStories(nil)
		for _, ui := range a.tuis {
			ui.Redraw()
		}
	})

	// bind the new machine to all stories
	for _, s := range a.stories {
		s.Memory.Mach = a.mem
		// TODO safe?
		s.Memory.TimeActivated = nil
		s.Memory.TimeDeactivated = nil
	}

	return nil
}

// initStories inits stories and their buttons
func (a *Agent) initStories() {
	mach := a.Mach()

	// TODO NewAction & merge
	a.stories = map[string]*shared.Story{

		// waking up (progress bar)
		ss.StoryWakingUp: sa.StoryWakingUp.New([]shared.Action{
			{
				ID:    amhelp.RandId(8),
				Label: "Overall progress",
				Desc:  "This is the progress of the whole cooking session flow",
				Value: func() int {
					// TODO switch assumes the first active, when we'd like the last active
					return slices.Index(states.CookGroups.MainFlow,
						mach.Switch(states.CookGroups.MainFlow))
				},
				ValueEnd: func() int {
					return len(states.CookGroups.MainFlow) - 1
				},
			},
			{
				ID:    amhelp.RandId(8),
				Label: "Waking up",
				Desc:  "This button shows the progress of waking up",
				Value: func() int {
					return len(mach.ActiveStates(states.CookGroups.BootGenReady))
				},
				ValueEnd: func() int {
					return len(states.CookGroups.BootGen)
				},
				VisibleAgent: amhelp.Cond{
					Not: S{ss.Ready},
				},
			},
		}),

		// joke (hidden / visible / active)
		ss.StoryJoke: sa.StoryJoke.New([]shared.Action{
			{
				ID:    amhelp.RandId(8),
				Label: "Joke?",
				Desc:  "This button tells a joke",
				VisibleAgent: amhelp.Cond{
					Any1: S{ss.StoryIngredientsPicking, ss.StoryRecipePicking, ss.StoryCookingStarted, ss.StoryMealReady},
				},
				IsDisabled: func() bool {
					return mach.Is1(ss.StoryJoke)
				},
				Action: func() {
					// TODO extract as TellJokeState
					s := a.stories[ss.StoryJoke]

					if !a.hasJokes() {
						a.Output("The cook is working on new jokes.", shared.FromNarrator)
					}
					s.Epoch = a.MemCutoff.Load()
					if s.CanActivate(s) {
						// activate via ChangeStories, not directly
						_ = a.StoryActivate(nil, ss.StoryJoke)
					} else if !a.jokeRefusedMsg {
						// memorize the refusal
						a.jokeRefusedMsg = true
						_ = a.OutputPhrase("NoCookingNoJokes")
					} else {
						a.Log("repeated no jokes")
					}
				},
			},
		}),

		ss.StoryIngredientsPicking: sa.StoryIngredientsPicking.New([]shared.Action{
			{
				ID: amhelp.RandId(8),
				Value: func() int {
					return len(*a.ingredients.Load())
				},
				ValueEnd: func() int {
					return a.Config.Cook.MinIngredients
				},
				Label:    "Collecting ingredients",
				LabelEnd: "Ingredients ready",
				Desc:     "This button shows a progress of collecting ingredients",
				VisibleAgent: amhelp.Cond{
					Is:  S{ss.Ready},
					Not: S{ss.StoryCookingStarted},
				},
			},
		}),

		ss.StoryRecipePicking: sa.StoryRecipePicking.New(nil),

		ss.StoryCookingStarted: sa.StoryCookingStarted.New([]shared.Action{
			{
				ID: amhelp.RandId(8),
				Value: func() int {
					return 1 + len(a.mem.ActiveStates(a.allSteps()))
				},
				ValueEnd: func() int {
					// fix the progress for optional steps
					if mach.Is1(ss.StoryMealReady) {
						return len(a.mem.ActiveStates(a.allSteps()))
					}

					return 1 + len(a.allSteps())
				},
				Label:    "Cooking steps",
				LabelEnd: "Cooking completed",
				Desc:     "This button shows the progress of cooking",
				VisibleAgent: amhelp.Cond{
					Any: []S{
						{ss.StoryCookingStarted, ss.StepsReady},
						{ss.StoryMealReady},
					},
				},
			},
			// other buttons are created by [Agent.StoryCookingStartedState]
		}),

		ss.StoryMealReady: sa.StoryMealReady.New(nil),

		ss.StoryStartAgain: sa.StoryStartAgain.New([]shared.Action{
			{
				ID:    amhelp.RandId(8),
				Label: sa.StoryStartAgain.Title,
				Desc:  sa.StoryStartAgain.Desc,
				VisibleAgent: amhelp.Cond{
					Is:  S{ss.StoryMealReady},
					Not: S{ss.StoryStartAgain},
				},
				Action: func() {
					a.StoryActivate(nil, ss.StoryStartAgain)
				},
			},
		}),

		ss.StoryMemoryWipe: sa.StoryMemoryWipe.New([]shared.Action{
			{
				ID:    amhelp.RandId(8),
				Label: sa.StoryMemoryWipe.Title,
				Desc:  sa.StoryMemoryWipe.Desc,
				VisibleAgent: amhelp.Cond{
					Is:  S{ss.StoryMealReady},
					Not: S{ss.StoryMemoryWipe},
				},
				Action: func() {
					a.StoryActivate(nil, ss.StoryMemoryWipe)
				},
			},
		}),
	}

	// sort stories according to the schema
	var list []string
	for _, s := range states.CookGroups.Stories {
		if _, ok := a.stories[s]; !ok {
			// TODO log
			continue
		}

		list = append(list, s)
	}
	a.storiesOrder = list

	// bind the machines to all the stories
	for _, s := range a.stories {
		s.Agent.Mach = mach
		s.Memory.Mach = a.mem
	}
}

// allSteps returns all the step states (but only final or solo ones) from the memory machine.
func (a *Agent) allSteps() S {
	memSchema := a.mem.Schema()
	ret := S{}
	for _, name := range a.mem.StateNames().FilterMatch(sa.MatchSteps) {
		if name == states.MemMealReady {
			continue
		}

		state := memSchema[name]
		if amhelp.TagValue(state.Tags, "final") != "" || len(state.Remove) == 0 {
			ret = append(ret, name)
		}

		// TODO dont count optional steps, eg Frosting:
		//  - has last index
		//  - is not required by any other last index
	}

	return ret
}

func (a *Agent) hRenderStories(e *am.Event) {
	// gen
	actions := a.Actions()
	stories := a.Stories()

	// check diff
	if cmp.Diff(actions, a.lastActions) == "" {
		actions = nil
	}
	if cmp.Diff(stories, a.lastStories) == "" {
		stories = nil
	}
	if actions == nil && stories == nil {
		return
	}

	// render and cache
	a.Mach().EvAdd1(e, ss.UIRenderStories, Pass(&shared.AUIRenderStories{
		Actions: actions,
		Stories: stories,
	}))
	a.lastActions = actions
	a.lastStories = stories
}

// TODO remove?
// func (a *Agent) actions() []shared.Action {
// 	mach := a.Mach()
// 	var ret []shared.Action
// 	for _, key := range a.storiesOrder {
// 		s := a.stories[key]
//
// 		for _, but := range s.Actions {
// 			// skip invisible ones
// 			if !but.VisibleAgent.Check(mach) || !but.VisibleMem.Check(a.mem) {
// 				continue
// 			}
//
// 			ret = append(ret, but)
// 		}
// 	}
//
// 	return ret
// }

func (a *Agent) hasJokes() bool {
	j := a.jokes.Load()
	return j != nil && len(j.Jokes) > 0
}

// TODO state OrientingToPrompt?
func (a *Agent) runOrienting(ctx context.Context, e *am.Event) {
	mach := a.Mach()
	if ctx.Err() != nil {
		return // expired
	}

	mach.EvAdd1(e, ss.InputPending, nil)
	<-mach.When1(ss.Prompt, ctx)
	if ctx.Err() != nil {
		return // expired
	}

	// run parallel orienting
	mach.EvAdd1(e, ss.Orienting, Pass(&shared.APrompt{
		Prompt: a.UserInput,
	}))
}

func (a *Agent) nextUIName() string {
	idx := strconv.Itoa(len(a.tuis))
	a.tuiNum++
	return idx
}

func (a *Agent) redrawClock(e *am.Event) {
}

func (a *Agent) storyRecipePickingCleanup(e *am.Event) bool {
	mach := a.Mach()

	// remove recipe
	a.recipe.Store(nil)
	mach.EvRemove1(e, ss.RecipeReady, nil)

	return a.storyCookingStartedCleanup(e)
}

func (a *Agent) storyCookingStartedCleanup(e *am.Event) bool {
	mach := a.Mach()

	// remove step states
	mach.EvRemove(e, S{ss.StepsReady, ss.StepCompleted}, nil)

	return true
}

func (a *Agent) storyIngredientsPickingCleanup(e *am.Event) bool {
	mach := a.Mach()

	mach.EvRemove1(e, ss.IngredientsReady, nil)

	return a.storyRecipePickingCleanup(e)
}

// ///// ///// /////

// ///// MISC

// ///// ///// /////

func sortSteps(schema am.Schema) S {
	steps := make(S, 0, len(schema))
	for name := range schema {
		steps = append(steps, name)
	}

	slices.SortFunc(steps, func(name1, name2 string) int {
		state1 := schema[name1]
		state2 := schema[name2]

		idx1Int, _ := strconv.Atoi(amhelp.TagValue(state1.Tags, "idx"))
		idx2Int, _ := strconv.Atoi(amhelp.TagValue(state2.Tags, "idx"))
		if idx1Int != idx2Int {
			return idx1Int - idx2Int
		}

		isFinal1 := amhelp.TagValue(state1.Tags, "final") != ""
		isFinal2 := amhelp.TagValue(state2.Tags, "final") != ""
		if isFinal1 != isFinal2 {
			if isFinal1 {
				return 1
			}

			return -1
		}

		return strings.Compare(name1, name2)
	})

	return steps
}

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// APrefix is the args prefix, set from config.
var APrefix = "cook"

type Args struct {
	am.ArgsBase
}

func (Args) ArgsPrefix() string {
	return APrefix
}

// -----

type AStepCompleted struct {
	Args
	ID string `log:"id"`
}

func (AStepCompleted) ArgsState() string {
	return ss.StepCompleted
}

// ----- RPC boilerplate

func init() {
	for _, arg := range ArgsRPC {
		gob.Register(arg)
	}
}

var ArgsRPC = []am.ArgsApi{AStepCompleted{}}

// ///// ///// /////

// ///// WEB

// ///// ///// /////

type Web struct {
	*web.Handlers
}

func NewWeb(a *Agent) *Web {
	w := &Web{}
	w.Handlers = web.NewHandlers(a, w)
	return w
}

// custom page data
// func (w *Web) PushDashData(
// 	e *am.Event, client *arpc.Client, dash *typesweb.DataDashboard,
// ) am.Result {
// 	return client.NetMach.EvAdd1(e, ssP.Data, am.Pass(
// 		&typesweb.AData{
// 			DataDash: dash,
// 		},
// 		&shared.AData{
// 			Browsers: shared.DetectBrowsers(),
// 		},
// 	))
// }
