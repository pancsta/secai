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

	"github.com/gliderlabs/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/cook/db"
	"github.com/pancsta/secai/examples/cook/schema"
	llmagent "github.com/pancsta/secai/llm_agent"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tools/searxng"
	"github.com/pancsta/secai/tui"
	statesui "github.com/pancsta/secai/tui/states"
)

const id = "cook"

// mock will run a sample scenario
// const mock = false

type Mock struct {
	// Global mock switch, also requires AM_MOCK=1.
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
	Active: true,

	FlowPromptIngredients: "I have 2 carrots, 3 eggs and rice",
	FlowPromptRecipe:      "cake",
	FlowPromptCooking:     "wipe your memory",
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

var ss = schema.CookStates
var ssui = statesui.UIStoriesStates
var SAdd = am.SAdd

type S = am.S

var WelcomeMessage = "Please wait while loading..."

type StoryUI struct {
	*schema.Story

	Buttons []shared.StoryButton
}

// ///// ///// /////

// ///// CONFIG

// ///// ///// /////

type Config struct {
	// COOK

	MinIngredients int           `arg:"env:COOK_MIN_INGREDIENTS" help:"Number of ingredients to collect." default:"3"`
	GenJokesAmount int           `arg:"env:COOK_GEN_JOKES_AMOUNT" help:"Number of jokes to generate." default:"3"`
	SessionTimeout time.Duration `arg:"env:COOK_SESSION_TIMEOUT" help:"Session timeout." default:"1h"`
	// Number of recipes to generate.
	GenRecipes   int `arg:"env:COOK_GEN_RECIPES" help:"Number of recipes to generate." default:"3"`
	MinPromptLen int `arg:"env:COOK_MIN_PROMPT_LEN" help:"Minimum prompt length." default:"2"`
	// Lower number = higher frequency of step comments. 2=50%, 3=33%.
	StepCommentFreq int           `arg:"env:COOK_STEP_COMMENT_FREQ" help:"Step comment frequency." default:"2"`
	HeartbeatFreq   time.Duration `arg:"env:COOK_HEARTBEAT_FREQ" help:"Heartbeat frequency." default:"1h"`
	// Certainty above which the orienting move should be accepted.
	OrientingMoveThreshold float64 `arg:"env:COOK_ORIENTING_MOVE_THRESHOLD" help:"Certainty above which the orienting move should be accepted." default:"0.5"`

	// SECAI TODO extract

	OpenAIAPIKey   string `arg:"env:OPENAI_API_KEY" help:"OpenAI API key."`
	DeepseekAPIKey string `arg:"env:DEEPSEEK_API_KEY" help:"DeepSeek API key."`
	TUIPort        int    `arg:"env:SECAI_TUI_PORT" help:"SSH port for the TUI." default:"7854"`
	TUIHost        string `arg:"env:SECAI_TUI_HOST" help:"SSH host for the TUI." default:"localhost"`
	Mock           bool   `arg:"env:SECAI_MOCK" help:"Enable scenario mocking."`
	ReqLimit       int    `arg:"env:SECAI_REQ_LIMIT" help:"Max LLM requests per session." default:"1000"`
}

// TODO shared secai config with everything from .env

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

type Agent struct {
	// inherit from LLM Agent
	*llmagent.Agent

	// public

	Config      Config
	StoriesList []string
	Stories     map[string]*StoryUI
	TUIs        []shared.UI
	Msgs        []*shared.Msg
	MemCutoff   atomic.Uint64

	// DB

	dbConn        *sql.DB
	dbQueries     *db.Queries
	character     atomic.Pointer[schema.ResultGenCharacter]
	jokes         atomic.Pointer[schema.ResultGenJokes]
	recipe        atomic.Pointer[schema.Recipe]
	stepComments  atomic.Pointer[schema.ResultGenStepComments]
	resources     atomic.Pointer[schema.ResultGenResources]
	ingredients   atomic.Pointer[[]schema.Ingredient]
	moveOrienting atomic.Pointer[schema.ResultOrienting]

	// machs

	mem *am.Machine

	// tools

	tSearxng *searxng.Tool

	// prompts

	pGenCharacter       *schema.PromptGenCharacter
	pGenResources       *schema.PromptGenResources
	pGenJokes           *schema.PromptGenJokes
	pIngredientsPicking *schema.PromptIngredientsPicking
	pRecipePicking      *schema.PromptRecipePicking
	pGenSteps           *schema.PromptGenSteps
	pGenStepComments    *schema.PromptGenStepComments
	pCookingStarted     *schema.PromptCookingStarted
	pOrienting          *schema.PromptOrienting

	// internals

	srvUI           *ssh.Server
	loop            *amhelp.StateLoop
	loopCooking     *amhelp.StateLoop
	loopIngredients *amhelp.StateLoop
	loopRecipe      *amhelp.StateLoop
	reqLimitOk      atomic.Bool
	preWakeupSum    uint64
	// the last msg was a no-jokes-without-cooking
	jokeRefusedMsg   bool
	orientingPending bool
	lastStoryCheck   uint64

	// pSearchingLLM *secai.Prompt[schema.ParamsSearching, schema.ResultSearching]
	// pAnswering    *secai.Prompt[schema.ParamsAnswering, schema.ResultAnswering]
}

// NewCook returns a preconfigured instance of Agent.
func NewCook(ctx context.Context, config Config) (*Agent, error) {
	a := New(ctx, id, ss.Names(), schema.CookSchema)
	if err := a.Init(a); err != nil {
		return nil, err
	}
	a.Config = config

	return a, nil
}

// New returns a custom instance of Agent.
func New(
	ctx context.Context, id string, states am.S, machSchema am.Schema,
) *Agent {

	a := &Agent{
		Agent: llmagent.New(ctx, id, states, machSchema),
	}

	// defaults
	a.jokes.Store(&schema.ResultGenJokes{})
	a.recipe.Store(&schema.Recipe{})
	a.ingredients.Store(&[]schema.Ingredient{})
	a.reqLimitOk.Store(true)

	// predefined msgs
	a.Msgs = append(a.Msgs, shared.NewMsg(WelcomeMessage, shared.FromSystem))

	return a
}

func (a *Agent) Init(agent secai.AgentAPI) error {
	// call super
	err := a.Agent.Init(agent)
	if err != nil {
		return err
	}
	mach := a.Mach()

	a.Mach().AddBreakpoint(S{ss.StoryWakingUp}, nil)

	// args mapper for logging
	mach.SetLogArgs(LogArgs)
	mach.AddBreakpoint(nil, S{ss.StoryRecipePicking})

	// init searxng - websearch tool
	a.tSearxng, err = searxng.New(a)
	if err != nil {
		return err
	}

	// init prompts
	a.pGenCharacter = schema.NewPromptGenCharacter(a)
	a.pGenResources = schema.NewPromptGenResources(a)
	a.pGenJokes = schema.NewPromptGenJokes(a)
	a.pIngredientsPicking = schema.NewPromptIngredientsPicking(a)
	a.pRecipePicking = schema.NewPromptRecipePicking(a)
	a.pCookingStarted = schema.NewPromptCookingStarted(a)
	a.pGenSteps = schema.NewPromptGenSteps(a)
	a.pOrienting = schema.NewPromptOrienting(a)
	a.pGenStepComments = schema.NewPromptGenStepComments(a)

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

func (a *Agent) initMem() error {
	// TODO cook's mem schema
	var err error
	mach := a.Mach()
	if a.mem != nil {
		a.MemCutoff.Add(a.mem.TimeSum(nil))
	}

	a.mem, err = am.NewCommon(mach.Ctx(), "memory-cook", baseschema.MemSchema,
		baseschema.MemStates.Names(), nil, mach, nil)
	if err != nil {
		return err
	}
	shared.MachTelemetry(a.mem, nil)

	// bind the new machine to all stories
	for _, s := range a.Stories {
		s.Memory.Mach = a.mem
		// TODO safe?
		s.Memory.TimeActivated = nil
		s.Memory.TimeDeactivated = nil
	}

	return nil
}

func (a *Agent) Queries() *db.Queries {
	if a.dbQueries == nil {
		a.dbQueries = db.New(a.dbConn)
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
		schema.CookStates.StoryWakingUp: {
			Story: schema.StoryWakingUp.Clone(),
			Buttons: []shared.StoryButton{
				{
					Label: "Overall progress",
					Desc:  "This is the progress of the whole cooking session flow",
					Value: func() int {
						// TODO switch assumes the first active, when we'd like the last active
						return slices.Index(schema.CookGroups.MainFlow,
							mach.Switch(schema.CookGroups.MainFlow))
					},
					ValueEnd: func() int {
						return len(schema.CookGroups.MainFlow) - 1
					},
				},
				{
					Label: "Waking up",
					Desc:  "This button shows the progress of waking up",
					Value: func() int {
						return mach.CountActive(schema.CookGroups.BootGenReady)
					},
					ValueEnd: func() int {
						return len(schema.CookGroups.BootGen)
					},
					VisibleCook: amhelp.Cond{
						Not: S{ss.Ready},
					},
				},
			},
		},

		// joke (hidden / visible / active)
		schema.CookStates.StoryJoke: {
			Story: schema.StoryJoke.Clone(),
			Buttons: []shared.StoryButton{
				{
					Label: "Joke?",
					Desc:  "This button tells a joke",
					VisibleCook: amhelp.Cond{
						Any1: S{ss.StoryIngredientsPicking, ss.StoryRecipePicking, ss.StoryCookingStarted, ss.StoryMealReady},
					},
					Action: func() {
						// TODO as TellJokeState
						s := a.Stories[ss.StoryJoke]
						// TODO check mach.CanAdd1(ss.StoryJoke, nil) once impl
						if !a.canJoke() {
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
						}
					},
				},
			},
		},

		schema.CookStates.StoryIngredientsPicking: {
			Story: schema.StoryIngredientsPicking,
			Buttons: []shared.StoryButton{
				{
					Value: func() int {
						return len(*a.ingredients.Load())
					},
					ValueEnd: func() int {
						return a.Config.MinIngredients
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

		schema.CookStates.StoryRecipePicking: {
			Story: schema.StoryRecipePicking,
			// TODO buttons?
		},

		schema.CookStates.StoryCookingStarted: {
			Story: schema.StoryCookingStarted,
			Buttons: []shared.StoryButton{
				{
					Value: func() int {
						return a.mem.CountActive(a.allSteps())
					},
					ValueEnd: func() int {
						// fix the progress for optional steps
						if mach.Is1(ss.StoryMealReady) {
							return a.mem.CountActive(a.allSteps())
						}

						return len(a.allSteps())
					},
					Label: "Cooking steps",
					// TODO LabelEnd doesnt work
					LabelEnd: "Cooking completed",
					Desc:     "This button shows the progress of cooking",
					VisibleCook: amhelp.Cond{
						Any1: S{ss.StoryCookingStarted, ss.StoryMealReady},
					},
				},
			},
			// other buttons are created by [Agent.StoryCookingStartedState]
		},

		schema.CookStates.StoryMealReady: {
			Story: schema.StoryMealReady,
		},

		schema.CookStates.StoryStartAgain: {
			Story: schema.StoryStartAgain,
			Buttons: []shared.StoryButton{
				{
					Label: schema.StoryStartAgain.Title,
					Desc:  schema.StoryStartAgain.Desc,
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

		schema.CookStates.StoryMemoryWipe: {
			Story: schema.StoryMemoryWipe,
			Buttons: []shared.StoryButton{
				{
					Label: schema.StoryMemoryWipe.Title,
					Desc:  schema.StoryMemoryWipe.Desc,
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
	for _, s := range schema.CookGroups.Stories {
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
	for _, name := range a.mem.StateNamesMatch(schema.MatchSteps) {
		if name == schema.MemMealReady {
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

// clockStates returns the list of states monitored in the chat's clock emoji chart.
func (a *Agent) clockStates() S {
	trackedStates := a.Mach().StateNames()

	// dont track a global handler
	trackedStates = shared.SlicesWithout(trackedStates, ss.CheckStories)

	return trackedStates
}

func (a *Agent) renderStories(e *am.Event) {
	for _, ui := range a.TUIs {
		uiStories, ok := ui.(*tui.Stories)
		if !ok {
			continue
		}

		// pass to the UI
		uiStories.UIMach().EvAdd1(e, ssui.ReqReplaceContent, PassAA(&AA{
			Buttons: a.storiesButtons(),
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

func (a *Agent) storiesButtons() []shared.StoryButton {
	mach := a.Mach()
	var buts []shared.StoryButton
	for _, key := range a.StoriesList {
		s := a.Stories[key]

		for _, but := range s.Buttons {
			// skip invisible ones
			if !but.VisibleCook.Check(mach) || !but.VisibleMem.Check(a.mem) {
				continue
			}

			buts = append(buts, but)
		}
	}

	return buts
}

// TODO replace with machine.CanAdd
func (a *Agent) canJoke() bool {
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

func (a *Agent) nextUIName(uiType string) string {
	i := 0
	// TODO enum
	switch uiType {
	case "stories":
		for _, ui := range a.TUIs {
			if _, ok := ui.(*tui.Stories); ok {
				i++
			}

		}
	case "chat":
		for _, ui := range a.TUIs {
			if _, ok := ui.(*tui.Chat); ok {
				i++
			}
		}

	case "clock":
		for _, ui := range a.TUIs {
			if _, ok := ui.(*tui.Clock); ok {
				i++
			}
		}
	}

	return strconv.Itoa(i)
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

	sort.Sort(StepsByReqFinalList(steps))

	return shared.Map(steps, func(s StepsByReqFinal) string {
		return s.Name
	})
}

type StepsByReqFinal struct {
	Name   string
	Schema am.Schema
}

type StepsByReqFinalList []StepsByReqFinal

func (s StepsByReqFinalList) Len() int { return len(s) }
func (s StepsByReqFinalList) Less(n1, n2 int) bool {
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
func (s StepsByReqFinalList) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// aliases

type AA = shared.A
type AARpc = shared.ARpc

var PassAA = shared.Pass

const APrefix = "cook"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *schema.ResultOrienting `log:"move"`

	// agent's non-RPC args
	// TODO
}

// ARpc is a subset of [am.A], that can be passed over RPC (eg no channels, instances, etc)
type ARpc struct {
	// base args of the framework
	*shared.A

	// agent's args
	Move *schema.ResultOrienting `log:"move"`
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
