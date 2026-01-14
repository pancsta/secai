// Package cook is a recipe-choosing and cooking agent with a gen-ai character.
package cook

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"dario.cat/mergo"
	"github.com/gliderlabs/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	"github.com/pancsta/secai/examples/cook/db/sqlc"

	agentllm "github.com/pancsta/secai/agent_llm"
	sa "github.com/pancsta/secai/examples/cook/schema"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tools/searxng"
	"github.com/pancsta/secai/tui"
	ssui "github.com/pancsta/secai/tui/states"
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

var ss = sa.CookStates
var ssT = ssui.TUIStates
var SAdd = am.SAdd

type S = am.S

var WelcomeMessage = "Please wait while loading..."

type StoryUI struct {
	*sa.Story

	Actions []shared.StoryAction
}

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
	return Config{
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
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

type Agent struct {
	// inherit from LLM AgentLLM
	*agentllm.AgentLLM

	// public

	Config      *Config
	StoriesList []string
	Stories     map[string]*StoryUI
	UIs         []*tui.Tui
	Msgs        []*shared.Msg
	MemCutoff   atomic.Uint64
	UINum       int

	// DB

	dbConn        *sql.DB
	dbQueries     *sqlc.Queries
	character     atomic.Pointer[sa.ResultGenCharacter]
	jokes         atomic.Pointer[sa.ResultGenJokes]
	recipe        atomic.Pointer[sa.Recipe]
	stepComments  atomic.Pointer[sa.ResultGenStepComments]
	resources     atomic.Pointer[sa.ResultGenResources]
	ingredients   atomic.Pointer[[]sa.Ingredient]
	moveOrienting atomic.Pointer[sa.ResultOrienting]

	// machs

	mem *am.Machine

	// tools

	tSearxng *searxng.Tool

	// prompts

	pGenCharacter       *sa.PromptGenCharacter
	pGenResources       *sa.PromptGenResources
	pGenJokes           *sa.PromptGenJokes
	pIngredientsPicking *sa.PromptIngredientsPicking
	pRecipePicking      *sa.PromptRecipePicking
	pGenSteps           *sa.PromptGenSteps
	pGenStepComments    *sa.PromptGenStepComments
	pCookingStarted     *sa.PromptCookingStarted
	pOrienting          *sa.PromptOrienting

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

	// pSearchingLLM *secai.Prompt[schema.ParamsSearching, schema.ResultSearching]
	// pAnswering    *secai.Prompt[schema.ParamsAnswering, schema.ResultAnswering]
}

// NewCook returns a preconfigured instance of Agent.
func NewCook(ctx context.Context, cfg *Config) (*Agent, error) {
	a := New(ctx, ss.Names(), sa.CookSchema)
	if err := a.Init(cfg); err != nil {
		return nil, err
	}

	return a, nil
}

// New returns a custom instance of Agent.
func New(ctx context.Context, states am.S, schema am.Schema) *Agent {
	a := &Agent{
		AgentLLM: agentllm.New(ctx, states, schema),
	}

	// defaults
	a.jokes.Store(&sa.ResultGenJokes{})
	a.recipe.Store(&sa.Recipe{})
	a.ingredients.Store(&[]sa.Ingredient{})
	a.reqLimitOk.Store(true)

	// predefined msgs
	a.Msgs = append(a.Msgs, shared.NewMsg(WelcomeMessage, shared.FromSystem))

	return a
}

func (a *Agent) Init(cfg *Config) error {
	var err error

	APrefix = cfg.Agent.ID

	// build config
	a.Config = cfg
	baseDefault := shared.ConfigDefault()
	if err := mergo.Merge(&baseDefault, cfg.Config, mergo.WithOverride); err != nil {
		return err
	}

	// call super
	err = a.AgentLLM.Init(a, &baseDefault, LogArgs, sa.CookGroups, sa.CookStates, NewArgsRpc())
	if err != nil {
		return err
	}
	cfg.Config = baseDefault
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
	a.pGenCharacter = sa.NewPromptGenCharacter(a)
	a.pGenResources = sa.NewPromptGenResources(a)
	a.pGenJokes = sa.NewPromptGenJokes(a)
	a.pIngredientsPicking = sa.NewPromptIngredientsPicking(a)
	a.pRecipePicking = sa.NewPromptRecipePicking(a)
	a.pCookingStarted = sa.NewPromptCookingStarted(a)
	a.pGenSteps = sa.NewPromptGenSteps(a)
	a.pOrienting = sa.NewPromptOrienting(a)
	a.pGenStepComments = sa.NewPromptGenStepComments(a)

	// register tools
	// secai.ToolAddToPrompts(a.tSearxng, a.pSearchingLLM, a.pAnswering)

	// init memory
	err = a.initMem()
	if err != nil {
		return err
	}

	a.initStories()

	return nil
}

type MemHandlers struct {
	a *Agent
}

func (m *MemHandlers) AnyState(_ *am.Event) {
	// redraw on change
	for _, ui := range m.a.UIs {
		ui.Redraw()
	}
}

func (a *Agent) initMem() error {
	// TODO cook's mem schema
	var err error
	mach := a.Mach()
	cfg := a.Config
	if a.mem != nil {
		a.MemCutoff.Add(a.mem.Time(nil).Sum(nil))
	}

	a.mem, err = am.NewCommon(mach.Ctx(), "memory-"+cfg.Agent.ID, baseschema.MemSchema,
		baseschema.MemStates.Names(), nil, mach, nil)
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
		for _, ui := range a.UIs {
			ui.Redraw()
		}
	})

	// bind the new machine to all stories
	for _, s := range a.Stories {
		s.Memory.Mach = a.mem
		// TODO safe?
		s.Memory.TimeActivated = nil
		s.Memory.TimeDeactivated = nil
	}

	return nil
}

func (a *Agent) Queries() *sqlc.Queries {
	if a.dbQueries == nil {
		a.dbQueries = sqlc.New(a.dbConn)
	}

	return a.dbQueries
}

// TODO move to base agent
func (a *Agent) StoryActivate(e *am.Event, story string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, PassAA(&AA{
		StatesList:   S{story},
		ActivateList: []bool{true},
	}))
}

// TODO move to base agent
func (a *Agent) StoryDeactivate(e *am.Event, story string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, PassAA(&AA{
		StatesList:   S{story},
		ActivateList: []bool{false},
	}))
}

// Phrase returns a random phrase from resources under [key], or an empty string.
// TODO move to base agent
func (a *Agent) Phrase(key string, args ...any) string {
	r := a.resources.Load()
	if r == nil || len(r.Phrases[key]) == 0 {
		return ""
	}
	txt := r.Phrases[key][rand.Intn(len(r.Phrases[key]))]

	return fmt.Sprintf(txt, args...)
}

// OutputPhrase is sugar for Phrase followed by Output FromAssistant.
func (a *Agent) OutputPhrase(key string, args ...any) error {
	txt := a.Phrase(key, args...)
	if txt != "" {
		a.Log("output phrase", "key", key)
		a.Output(txt, shared.FromAssistant)
		return nil
	}

	return fmt.Errorf("phrase not found: %s", key)
}

// initStories inits Stories and their buttons
func (a *Agent) initStories() {
	mach := a.Mach()

	a.Stories = map[string]*StoryUI{

		// waking up (progress bar)
		sa.CookStates.StoryWakingUp: {
			Story: sa.StoryWakingUp.Clone(),
			Actions: []shared.StoryAction{
				{
					Label: "Overall progress",
					Desc:  "This is the progress of the whole cooking session flow",
					Value: func() int {
						// TODO switch assumes the first active, when we'd like the last active
						return slices.Index(sa.CookGroups.MainFlow,
							mach.Switch(sa.CookGroups.MainFlow))
					},
					ValueEnd: func() int {
						return len(sa.CookGroups.MainFlow) - 1
					},
				},
				{
					Label: "Waking up",
					Desc:  "This button shows the progress of waking up",
					Value: func() int {
						return len(mach.ActiveStates(sa.CookGroups.BootGenReady))
					},
					ValueEnd: func() int {
						return len(sa.CookGroups.BootGen)
					},
					VisibleCook: amhelp.Cond{
						Not: S{ss.Ready},
					},
				},
			},
		},

		// joke (hidden / visible / active)
		sa.CookStates.StoryJoke: {
			Story: sa.StoryJoke.Clone(),
			Actions: []shared.StoryAction{
				{
					Label: "Joke?",
					Desc:  "This button tells a joke",
					VisibleCook: amhelp.Cond{
						Any1: S{ss.StoryIngredientsPicking, ss.StoryRecipePicking, ss.StoryCookingStarted, ss.StoryMealReady},
					},
					IsDisabled: func() bool {
						return mach.Is1(ss.StoryJoke)
					},
					Action: func() {
						// TODO extract as TellJokeState
						s := a.Stories[ss.StoryJoke]

						if !a.hasJokes() {
							a.Output("The cook is working on new jokes.", shared.FromNarrator)
						}
						s.Epoch = a.MemCutoff.Load()
						if s.CanActivate(s.Story) {
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
			},
		},

		sa.CookStates.StoryIngredientsPicking: {
			Story: sa.StoryIngredientsPicking,
			Actions: []shared.StoryAction{
				{
					Value: func() int {
						return len(*a.ingredients.Load())
					},
					ValueEnd: func() int {
						return a.Config.Cook.MinIngredients
					},
					Label:    "Collecting ingredients",
					LabelEnd: "Ingredients ready",
					Desc:     "This button shows a progress of collecting ingredients",
					VisibleCook: amhelp.Cond{
						Is:  S{ss.Ready},
						Not: S{ss.StoryCookingStarted},
					},
				},
			},
		},

		sa.CookStates.StoryRecipePicking: {
			Story: sa.StoryRecipePicking,
			// TODO buttons?
		},

		sa.CookStates.StoryCookingStarted: {
			Story: sa.StoryCookingStarted,
			Actions: []shared.StoryAction{
				{
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
					VisibleCook: amhelp.Cond{
						Any1: S{ss.StoryCookingStarted, ss.StoryMealReady},
					},
				},
			},
			// other buttons are created by [AgentLLM.StoryCookingStartedState]
		},

		sa.CookStates.StoryMealReady: {
			Story: sa.StoryMealReady,
		},

		sa.CookStates.StoryStartAgain: {
			Story: sa.StoryStartAgain,
			Actions: []shared.StoryAction{
				{
					Label: sa.StoryStartAgain.Title,
					Desc:  sa.StoryStartAgain.Desc,
					VisibleCook: amhelp.Cond{
						Is:  S{ss.StoryMealReady},
						Not: S{ss.StoryStartAgain},
					},
					Action: func() {
						a.StoryActivate(nil, ss.StoryStartAgain)
					},
				},
			},
		},

		sa.CookStates.StoryMemoryWipe: {
			Story: sa.StoryMemoryWipe,
			Actions: []shared.StoryAction{
				{
					Label: sa.StoryMemoryWipe.Title,
					Desc:  sa.StoryMemoryWipe.Desc,
					VisibleCook: amhelp.Cond{
						Is:  S{ss.StoryMealReady},
						Not: S{ss.StoryMemoryWipe},
					},
					Action: func() {
						a.StoryActivate(nil, ss.StoryMemoryWipe)
					},
				},
			},
		},
	}

	// sort stories according to the schema
	var list []string
	for _, s := range sa.CookGroups.Stories {
		if _, ok := a.Stories[s]; !ok {
			// TODO log
			continue
		}

		list = append(list, s)
	}
	a.StoriesList = list

	// bind the machines to all the stories
	for _, s := range a.Stories {
		s.Cook.Mach = mach
		s.Memory.Mach = a.mem
	}
}

// allSteps returns all the step states (but only final or solo ines) from the memory machine.
func (a *Agent) allSteps() S {
	memSchema := a.mem.Schema()
	ret := S{}
	for _, name := range a.mem.StateNamesMatch(sa.MatchSteps) {
		if name == sa.MemMealReady {
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

// HistoryStates returns a list of states to track in the history.
func (a *Agent) HistoryStates() S {
	trackedStates := a.Mach().StateNames()

	// dont track a global handler
	trackedStates = shared.SlicesWithout(trackedStates, ss.CheckStories)

	return trackedStates
}

func (a *Agent) renderStories(e *am.Event) {
	for _, ui := range a.UIs {
		stories := ui.MachTUI
		if stories == nil {
			continue
		}

		// pass to the UI
		stories.EvAdd1(e, ssT.ReqReplaceStories, PassAA(&AA{
			Actions: a.storiesButtons(),
			Stories: a.storiesInfo(),
		}))
	}
}

// storiesInfo extracts stories info from the stories map, according to the ordered list.
func (a *Agent) storiesInfo() []shared.StoryInfo {
	var stories []shared.StoryInfo
	for _, key := range a.StoriesList {
		s := a.Stories[key]
		stories = append(stories, s.StoryInfo)
	}

	return stories
}

func (a *Agent) storiesButtons() []shared.StoryAction {
	mach := a.Mach()
	var buts []shared.StoryAction
	for _, key := range a.StoriesList {
		s := a.Stories[key]

		for _, but := range s.Actions {
			// skip invisible ones
			if !but.VisibleCook.Check(mach) || !but.VisibleMem.Check(a.mem) {
				continue
			}

			buts = append(buts, but)
		}
	}

	return buts
}

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
	mach.EvAdd1(e, ss.Orienting, PassAA(&AA{
		Prompt: a.UserInput,
	}))
}

func (a *Agent) nextUIName() string {
	idx := strconv.Itoa(len(a.UIs))
	a.UINum++
	return idx
}

// ///// ///// /////

// ///// MISC

// ///// ///// /////

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

type AA = shared.A
type AARpc = shared.ARpc

// PassAA is shared.Pass.
var PassAA = shared.Pass

// APrefix is the args prefix, set from config.
var APrefix = "cook"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *sa.ResultOrienting `log:"move"`
	TUI  *tui.Tui

	// agent's non-RPC args
	// TODO
}

func NewArgs() A {
	return A{A: &shared.A{}}
}

func NewArgsRpc() ARpc {
	// TODO should be shared.ARpc
	return ARpc{A: &shared.A{}}
}

// ARpc is a subset of [A] that can be passed over RPC (eg no channels, conns, etc)
type ARpc struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *sa.ResultOrienting `log:"move"`
}

// ParseArgs extracts A from [am.Event.Args][APrefix] (decoder).
func ParseArgs(args am.A) *A {
	// RPC-only args (pointer)
	if r, ok := args[APrefix].(*ARpc); ok {
		a := amhelp.ArgsToArgs(r, &A{})
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// RPC-only args (value, eg from a network transport)
	if r, ok := args[APrefix].(ARpc); ok {
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

// PassRpc is a network-safe version of Pass. Use it when mutating aRPC workers.
func PassRpc(args *A) am.A {
	// dont nest in plain maps
	clone := *amhelp.ArgsToArgs(args, &ARpc{})
	clone.A = nil
	out := am.A{APrefix: clone}

	// merge with base args
	return am.AMerge(out, shared.PassRpc(args.A))
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
