// Package cook is a recipe-choosing and cooking agent with a gen-ai character.
package cook

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/google/go-cmp/cmp"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"

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
	// inherit from LLM AgentLLM
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
	handlersWeb      *web.Handlers
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
	err = a.AgentLLM.Init(a, &a.Config.Config, LogArgs, states.CookGroups, states.CookStates, NewArgsRPC())
	if err != nil {
		return err
	}
	mach := a.Mach()

	// mach.AddBreakpoint(nil, S{ss.StoryRecipePicking}, true)

	// loop guards
	a.loop = amhelp.NewStateLoop(mach, ss.Loop, nil)

	// init searxng - websearch tool
	a.tSearxng, err = searxng.New(a)
	if err != nil {
		return err
	}

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
	err = mach.BindHandlers(a.clockService)
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
	logFile := shared.ConfigLogPath(cfg.Agent)
	logAddr := shared.ConfigWebLogAddr(cfg.Web)
	binary := shared.BinaryPath(&cfg.Config)

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

	if cfg.TUI.PortSSH != -1 {
		l("TUI:")
		if cfg.TUI.PortWeb != -1 {
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
		l("- %s repl --config %s", binary, cfg.File)
		l("")
	}

	// LOG

	l("Log:")
	if logAddr != "" {
		l("- http://%s", logAddr)
	}
	l("- %s log --tail --config %s", binary, cfg.File)
	l("- tail -f %s/%s.jsonl -n 100 | fblog -d -x msg -x time -x level", cfg.Agent.Dir, cfg.Agent.ID)
	l("")

	// DEBUGGER

	if cfg.Debug.DBGEmbed && cfg.Debug.DBGAddr != "" {
		_, httpAddr, sshAddr, err := shared.ConfigDbgAddrs(cfg.Debug)
		if err == nil {
			sshAddr2 := strings.Split(sshAddr, ":")
			l("Debugger:")
			if cfg.Debug.DBGEmbedWeb != -1 {
				l("- http://localhost:%d", cfg.Debug.DBGEmbedWeb)
			}
			l("- files: http://%s", httpAddr)
			l("- ssh %s -p %s -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no", sshAddr2[0], sshAddr2[1])
			l("")
		}
	}

	// DB

	addrBase, addrAgent, addrHistory := shared.ConfigWebDBAddrs(cfg.Web)
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
			if act.Label == "Overall progress" {
				// TODO DEBUG
				print()
			}
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
	movesCooking := a.AgentImpl().MachMem().StateNamesMatch(sa.MatchSteps)
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
	if cfg.Debug.REPL {
		opts := arpc.ReplOpts{
			AddrDir: cfg.Agent.Dir,
		}
		if err := arpc.MachRepl(a.mem, "", &opts); err != nil {
			return err
		}
	}

	// update stories memory change (via basic OnChange)
	a.mem.OnChange(func(mach *am.Machine, before, after am.Time) {
		a.renderStories(nil)
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
	for _, name := range a.mem.StateNamesMatch(sa.MatchSteps) {
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

func (a *Agent) renderStories(e *am.Event) {
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
	a.Mach().EvAdd1(e, ss.UIRenderStories, Pass3RPC(&A3{
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
	mach.EvAdd1(e, ss.Orienting, Pass3(&A3{
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

// ///// ///// /////

// ///// MISC

// ///// ///// /////

func validateStepSchema(schema am.Schema, stepStates, allStates am.S) error {
	// TODO check min steps amount

	// check if going 1,2,3..n will end on MealReady
	mach := am.New(context.Background(), schema, nil)
	for _, step := range stepStates {
		mach.Add1(step, nil)
	}

	if !mach.Is1(states.MemMealReady) {
		return fmt.Errorf("step schema is invalid: %s", stepStates)
	}

	// TODO should fail when actiavted from the end

	return nil
}

// sorting steps

func sortSteps(schema am.Schema) S {
	steps := []StepsByReqFinal{}
	for name := range schema {
		steps = append(steps, StepsByReqFinal{
			Name:   name,
			Schema: schema,
		})
	}

	sort.Sort(sortStepsByIdx(steps))

	return shared.Map(steps, func(s StepsByReqFinal) string {
		return s.Name
	})
}

type StepsByReqFinal struct {
	Name   string
	Schema am.Schema
}

type sortStepsByIdx []StepsByReqFinal

func (s sortStepsByIdx) Len() int { return len(s) }
func (s sortStepsByIdx) Less(n1, n2 int) bool {
	el1 := s[n1]
	name1 := el1.Name
	state1 := el1.Schema[name1]
	idx1 := amhelp.TagValue(state1.Tags, "idx")
	idx1Int, _ := strconv.Atoi(idx1)
	isFinal1 := amhelp.TagValue(state1.Tags, "final") != ""

	el2 := s[n2]
	name2 := el2.Name
	state2 := el2.Schema[name2]
	idx2 := amhelp.TagValue(state2.Tags, "idx")
	idx2Int, _ := strconv.Atoi(idx2)
	isFinal2 := amhelp.TagValue(state2.Tags, "final") != ""

	if idx1Int < idx2Int || (idx1 == idx2 && !isFinal1 && isFinal2) {
		return true
	}

	return false
}
func (s sortStepsByIdx) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// aliases

// AgentLLM args

type A2 = agentllm.A
type A2RPC = agentllm.ARPC

var Pass2 = agentllm.Pass
var Pass2RPC = agentllm.PassRPC

// secai args

type A3 = shared.A
type A3RPC = shared.ARPC

var Pass3 = shared.Pass
var Pass3RPC = shared.PassRPC

// APrefix is the args prefix, set from config.
var APrefix = "cook"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// base args
	*agentllm.A

	// agent's args
	TUI *tui.TUI

	// agent's non-RPC args

	// TODO
}

func NewArgs() A {
	return A{A: &agentllm.A{}}
}

func NewArgsRPC() ARPC {
	return ARPC{A: &shared.A{}}
}

// ARPC is a subset of [A] that can be passed over RPC (eg no channels, conns, etc)
type ARPC struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *sallm.ResultOrienting `log:"move"`
}

// ParseArgs extracts A from [am.Event.Args][APrefix] (decoder).
func ParseArgs(args am.A) *A {
	// RPC-only args (pointer)
	if r, ok := args[APrefix].(*ARPC); ok {
		a := amhelp.ArgsToArgs(r, &A{})
		// decode base args
		a.A = agentllm.ParseArgs(args)

		return a
	}

	// RPC-only args (value, eg from a network transport)
	if r, ok := args[APrefix].(ARPC); ok {
		a := amhelp.ArgsToArgs(&r, &A{})
		// decode base args
		a.A = agentllm.ParseArgs(args)

		return a
	}

	// regular args (pointer)
	if a, _ := args[APrefix].(*A); a != nil {
		// decode base args
		a.A = agentllm.ParseArgs(args)

		return a
	}

	// defaults
	return &A{
		A: agentllm.ParseArgs(args),
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
	return am.AMerge(out, agentllm.Pass(args.A))
}

// PassRPC is a network-safe version of Pass. Use it when mutating aRPC workers.
func PassRPC(args *A) am.A {
	// dont nest in plain maps
	clone := *amhelp.ArgsToArgs(args, &ARPC{})
	clone.A = nil
	out := am.A{APrefix: clone}

	// merge with base args
	return am.AMerge(out, agentllm.PassRPC(args.A))
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
