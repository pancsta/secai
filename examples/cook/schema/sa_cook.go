//nolint:lll
package schema

import (
	"context"
	"fmt"
	"regexp"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"

	"github.com/pancsta/secai"
	llm "github.com/pancsta/secai/llm_agent/schema"
	base "github.com/pancsta/secai/schema"
)

// aliases

var ssLLM = llm.LLMAgentStates
var saLLM = llm.LLMAgentSchema
var sp = shared.Sp

// ///// ///// /////

// ///// STATES

// ///// ///// /////

// CookStatesDef contains all the states of the Cook state machine.
type CookStatesDef struct {
	*am.StatesBase

	ErrIngredients string
	ErrCooking     string

	// DB

	DBStarting string
	DBReady    string

	// flow states

	// Ready is when the agent has CharacterReady, JokesReady, and ResourcesReady.
	Ready string
	// IngredientsReady is the first step of the flow, the bot has a list of ingredients.
	IngredientsReady string
	// RecipeReady is when the user and the bot agree on the recipe.
	RecipeReady string
	// One of the cooking steps have been completed.
	StepCompleted string
	// TODO
	// No msgs or steps have been completed in a (defined) while.
	// StaleDialog string

	// stories

	// StoryJoke indicates that the joke story is currently happening.
	StoryJoke string
	// StoryWakingUp indicates that the waking-up story is currently happening.
	// This will boot up the bot from the prev session (SQL) or construct a new one (LLM).
	StoryWakingUp string
	// StoryIngredientsPicking indicates that the ingredients story is currently happening.
	StoryIngredientsPicking string
	StoryRecipePicking      string
	StoryCookingStarted     string
	StoryMealReady          string
	StoryMemoryWipe         string
	StoryStartAgain         string
	// TODO
	// StorySmallTalk          string
	// StoryWeatherTalk        string

	// actions

	// TODO keep story states in secai

	// Check the status of all the stories.
	CheckStories string
	// At least one of the stories has changed its status (active / inactive).
	StoryChanged string

	// prompts

	RestoreCharacter string
	GenCharacter     string
	CharacterReady   string

	RestoreJokes string
	GenJokes     string
	JokesReady   string

	RestoreResources string
	GenResources     string
	ResourcesReady   string

	GenSteps string
	// StepsReady implies the steps have been translated into actionable memory.
	StepsReady string

	GenStepComments   string
	StepCommentsReady string

	// The LLM is given possible moves and checks if the user wants to make any. Orienting usually runs in parallel with other prompts. After de-activation, it leaves results in handler struct `h.oriented`.
	Orienting string
	// OrientingMove performs a move decided upon by Orienting.
	OrientingMove string

	// inherit from LLM Agent
	*llm.LLMAgentStatesDef
}

type StepsStatesDef struct {
}

// CookGroupsDef contains all the state groups Cook state machine.
type CookGroupsDef struct {
	// Group with all stories.
	Stories S
	// Group with all the start states which call LLMs.
	BootGen S
	// Group with ready states for BootGen.
	BootGenReady S
	// All the states which should be stopped on Interrupt
	Interruptable S

	// exclusion groups for the Remove relation

	// All the states for the character generation.
	Character S
	// All the states for jokes generation.
	Jokes S
	// All the states for resource generation.
	Resources S
	// List of main flow states.
	MainFlow S
}

// CookSchema represents all relations and properties of CookStates.
var CookSchema = SchemaMerge(
	// inherit from LLM Agent
	llm.LLMAgentSchema,
	am.Schema{

		// errors

		ssC.ErrIngredients: {},
		ssC.ErrCooking:     {},

		// DB

		ssC.DBStarting: {
			Require: S{ssC.Start},
			Remove:  S{ssC.DBReady},
		},
		ssC.DBReady: {
			Require: S{ssC.Start},
			Remove:  S{ssC.DBStarting},
		},

		// flow

		ssC.IngredientsReady: {},
		ssC.RecipeReady:      {Require: S{ssC.IngredientsReady}},
		ssC.StepCompleted:    {Multi: true},
		// TODO
		// ssC.StaleDialog:          {Require: S{ssC.Ready}},

		// stories

		ssC.StoryJoke:               {},
		ssC.StoryWakingUp:           {Tags: S{base.TagManual}},
		ssC.StoryIngredientsPicking: {Tags: S{base.TagPrompt}},
		ssC.StoryRecipePicking:      {Tags: S{base.TagPrompt}},
		ssC.StoryCookingStarted:     {Tags: S{base.TagPrompt}},
		ssC.StoryMealReady: {
			Tags:   S{base.TagPrompt, base.TagManual},
			Remove: S{ssC.InputPending},
		},
		ssC.StoryMemoryWipe: {Tags: S{base.TagPrompt}},
		ssC.StoryStartAgain: {Tags: S{base.TagPrompt}},
		// TODO
		// ssC.StorySmallTalk:          {Tags: S{base.TagPrompt}},
		// ssC.StoryWeatherTalk:        {Tags: S{base.TagPrompt}},

		// actions

		ssC.CheckStories: {
			Multi:   true,
			Require: S{ssC.Start},
		},
		ssC.StoryChanged: {
			Multi: true,
			After: S{ssC.CheckStories},
		},

		// gen AI

		ssC.RestoreCharacter: {
			Auto:    true,
			Require: S{ssC.DBReady},
			Remove:  sgC.Character,
		},
		ssC.GenCharacter: {
			Require: S{ssC.DBReady},
			Remove:  sgC.Character,
			Tags:    S{base.TagPrompt, base.TagTrigger},
		},
		ssC.CharacterReady: {Remove: sgC.Character},

		ssC.RestoreJokes: {
			Auto:    true,
			Require: S{ssC.DBReady, ssC.CharacterReady},
			Remove:  sgC.Jokes,
		},
		ssC.GenJokes: {
			Require: S{ssC.CharacterReady, ssC.DBReady},
			Remove:  sgC.Jokes,
			Tags:    S{base.TagPrompt},
		},
		ssC.JokesReady: {Remove: sgC.Jokes},

		ssC.RestoreResources: {
			Auto:    true,
			Require: S{ssC.CharacterReady, ssC.DBReady},
			Remove:  sgC.Resources,
		},
		ssC.GenResources: {
			Require: S{ssC.CharacterReady, ssC.DBReady},
			Remove:  sgC.Resources,
			Tags:    S{base.TagPrompt, base.TagTrigger},
		},
		ssC.ResourcesReady: {Remove: sgC.Resources},

		ssC.GenSteps: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted},
			Remove:  S{ssC.StepsReady},
			Tags:    S{base.TagPrompt},
		},
		ssC.StepsReady: {
			Require: S{ssC.RecipeReady},
			Remove:  S{ssC.GenSteps},
		},

		ssC.GenStepComments: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted},
			Remove:  S{ssC.StepCommentsReady},
			Tags:    S{base.TagPrompt},
		},
		ssC.StepCommentsReady: {Remove: S{ssC.GenStepComments}},

		ssC.Orienting: {
			Multi: true,
			Tags:  S{base.TagPrompt},
		},
		ssC.OrientingMove: {},

		// OVERRIDES

		ssC.Start: StateAdd(saLLM[ssLLM.Start], State{
			Add: S{ssC.CheckStories, ssC.DBStarting},
		}),
		ssC.Ready: StateAdd(saLLM[ssLLM.Ready], State{
			Auto:    true,
			Require: S{ssC.CharacterReady, ssC.ResourcesReady},
		}),
		ssC.Interrupted: StateAdd(saLLM[ssLLM.Interrupted], State{
			// stop these from happening when interrupted
			Remove: sgC.Interruptable,
		}),
		ssC.Prompt: StateAdd(saLLM[ssLLM.Prompt], State{}),
	})

// EXPORTS AND GROUPS

var (
	stories = S{
		ssC.StoryWakingUp,
		ssC.StoryIngredientsPicking,
		ssC.StoryRecipePicking,
		ssC.StoryJoke,
		ssC.StoryCookingStarted,
		ssC.StoryMealReady,
		ssC.StoryMemoryWipe,
		ssC.StoryStartAgain,
		// TODO
		// ssC.StorySmallTalk,
		// ssC.StoryWeatherTalk,
	}

	ssC = am.NewStates(CookStatesDef{})
	sgC = am.NewStateGroups(CookGroupsDef{
		MainFlow: S{ssC.StoryWakingUp, ssC.StoryIngredientsPicking, ssC.StoryRecipePicking, ssC.StoryCookingStarted,
			ssC.StoryMealReady},
		Stories:       stories,
		BootGen:       S{ssC.GenCharacter, ssC.GenJokes, ssC.GenResources},
		BootGenReady:  S{ssC.CharacterReady, ssC.JokesReady, ssC.ResourcesReady},
		Interruptable: SAdd(S{ssC.CheckingOfferRefs}, stories),

		Character: S{ssC.CharacterReady, ssC.RestoreCharacter, ssC.GenCharacter},
		Jokes:     S{ssC.JokesReady, ssC.RestoreJokes, ssC.GenJokes},
		Resources: S{ssC.ResourcesReady, ssC.RestoreResources, ssC.GenResources},
	})

	// CookStates contains all the states for the Cook machine.
	CookStates = ssC
	// CookGroups contains all the state groups for the Cook machine.
	CookGroups = sgC
)

// NewCook will create the most basic Cook state machine.
func NewCook(ctx context.Context) *am.Machine {
	return am.New(ctx, CookSchema, nil)
}

// ///// ///// /////

// ///// MEMORY

// ///// ///// /////

// TODO schema, define the base memory in baseschema
var MemMealReady = "StepMealReady"

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////
// Comments are automatically converted to a jsonschema_description tag.

// CHARACTER

type PromptGenCharacter = secai.Prompt[ParamsGenCharacter, ResultGenCharacter]

func NewPromptGenCharacter(agent secai.AgentAPI) *PromptGenCharacter {
	return secai.NewPrompt[ParamsGenCharacter, ResultGenCharacter](
		agent, ssC.GenCharacter, `
			- You're generating a character which will lead a live cooking show.
			- You're being given a vague character's profession and the current year.
		`, `
			1. Generate info related to conversations and cooking.
			2. Add some personality to the character.
			3. Assign a more specific profession from the requested period.
			4. Assign a name.
		`, `
			Stay 
		`)
}

type ParamsGenCharacter struct {
	CharacterProfession string
	CharacterYear       int
}

type ResultGenCharacter struct {
	// 3 sentences describing the character's personality and background.
	Description string
	Profession  string
	Year        int
	Name        string
}

// RESOURCES

type PromptGenResources = secai.Prompt[ParamsGenResources, ResultGenResources]

func NewPromptGenResources(agent secai.AgentAPI) *PromptGenResources {
	return secai.NewPrompt[ParamsGenResources, ResultGenResources](
		agent, ssC.GenResources, `
			- You're a text and speech database.
		`, `
			1. Translate the provided phrases to the expected form, following the character's personality.
			2. Keep a humoristic and entertaining tone of a cooking tv show.
		`, `
			Translate each phrase into 3 different versions. Generate at maximum twice the word count of the original text. Keep the %d and other substitutions in the right place.
		`)
}

type ParamsGenResources struct {
	Phrases map[string]string
}

type ResultGenResources struct {
	Phrases map[string][]string
}

// RESOURCES DATA

// TODO enum for keys (use state names opt?)

var LLMResources = ParamsGenResources{
	Phrases: map[string]string{
		"NoCookingNoJokes":      "No jokes without cooking.",
		"IngredientsPicking":    "Tell me what cooking ingredients do you have at hand, we need at least %d to continue.",
		"IngredientsPickingEnd": "OK, I have the ingredients I need.",
		"RecipePicking":         "Ok, let me check my books for what we could make...",
		"ResumeNeeded":          "You have to Resume for me to do anything.",
		"WokenUp":               "OK I'm ready to start",
		"CookingStarted":        "You chose %s as the recipe which we will cook, got it. I will plan all the cooking steps nicely for us. Just press those buttons on the right, once I'm done.",
		"CharacterReady": sp(`
			Welcome to Cook - your AI-powered cooking assistant! It will help you pick a meal from the ingredients you have, and then you can cook it together (wink wink).
	`),
		ssC.StoryMealReady: "We made it, the meal is ready! You can enjoy it now. I hope you had fun cooking with us.",
		"ReqLimitReached":  "You have reached the limit of %d requests per session. Please come back later.",
	},
}

// TODO static resources

// JOKES

type PromptGenJokes = secai.Prompt[ParamsGenJokes, ResultGenJokes]

func NewPromptGenJokes(agent secai.AgentAPI) *PromptGenJokes {
	return secai.NewPrompt[ParamsGenJokes, ResultGenJokes](
		agent, ssC.GenJokes, `
			- You're a database of jokes
		`, `
			1. Generate the requested amount of jokes.
		`, `
			- Use the character's personality to make the jokes more interesting.
			- Avoid using the same joke twice.
			- Avoid jokes about religion, violence, and minors.
			- Pick jokes touching the time and / or the profession of the character.
			- Ignore the IDs field.
		`)
}

type ParamsGenJokes struct {
	// The number of jokes to generate.
	Amount int
}

type ResultGenJokes struct {
	// List of jokes, max 2 sentences each.
	Jokes []string
	IDs   []int64
}

// INGREDIENTS

type PromptIngredientsPicking = secai.Prompt[ParamsIngredientsPicking, ResultIngredientsPicking]

func NewPromptIngredientsPicking(agent secai.AgentAPI) *PromptIngredientsPicking {
	return secai.NewPrompt[ParamsIngredientsPicking, ResultIngredientsPicking](
		agent, ssC.StoryIngredientsPicking, `
			- You're a database of cooking ingredients.
		`, `
			1. Extract the ingredients from the user's prompt.
			2. Output the amount per each, assume a default value if not specified.
			3. If results are not valid, include a redo message for the user.
			4. Include previous ingredients in the result, unless user changes he's mind.
		`, `
			- Always returns the extracted ingredients, even if the number is less then required.
			- Customize the redo message with character's personality.
		`)
}

type Ingredient struct {
	Name   string
	Amount int
	Unit   string
}

type ParamsIngredientsPicking struct {
	// The minimum number of ingredients needed.
	MinIngredients int
	// Text to extract ingredients from.
	Prompt string
	// List of ingredients extracted from prompts till now.
	Ingredients []Ingredient
}

type ResultIngredientsPicking struct {
	Ingredients []Ingredient
	// A message to be shown to the user if the results are not valid.
	RedoMsg string
}

// RECIPE

type PromptRecipePicking = secai.Prompt[ParamsRecipePicking, ResultRecipePicking]

func NewPromptRecipePicking(agent secai.AgentAPI) *PromptRecipePicking {
	return secai.NewPrompt[ParamsRecipePicking, ResultRecipePicking](
		agent, ssC.StoryRecipePicking, `
			- You're a database of cooking recipes.
		`, `
			1. Suggest recipes based on user's ingredients.
			2. If possible, find 1 extra recipe, which is well known, but 1-3 ingredients are missing.
			3. Summarize the propositions using the character's personality.
		`, `
			- Limit the amount of recipes to the requested number (excluding the extra recipe).
			- Include an image URL per each recipe
		`)
}

type Recipe struct {
	Name  string
	Desc  string
	Steps string
}

type ParamsRecipePicking struct {
	// List of available ingredients.
	Ingredients []Ingredient
	// The number of recipes needed.
	Amount int
}

type ResultRecipePicking struct {
	// List of proposed recipes
	Recipes []Recipe
	// Extra recipe with unavailable ingredients.
	ExtraRecipe Recipe
	// Message to the user, summarizing the recipes. Max 3 sentences.
	Summary string
}

// STEPS

type PromptGenSteps = secai.Prompt[ParamsGenSteps, ResultGenSteps]

func NewPromptGenSteps(agent secai.AgentAPI) *PromptGenSteps {
	return secai.NewPrompt[ParamsGenSteps, ResultGenSteps](
		agent, ssC.GenSteps, `
			- You're a cooking process planner.
		`, `
			1. Extract actionable steps from the cooking recipe and represent them as binary flags called "states". Each step can represent either a long-running action (eg WaterHeatingUp), a short-running action (WaterBoiling), a fact (WaterBoiled). Each state can relate to any other state via Require, Remove, and Add relation.
			2. The final state is called MealReady. Not all the states have to be connected with relations.
			3. Put the time length of procedures (if given) inside Tags as "time:5m" to wait for 5min.
			4. Index the steps using a tag "idx:4" for the 5th step in the input. Steps which can't be active at the same time should have Remove relation between them.
			
			Example "make turkish coffee":
			- WaterHeatingUp
				- Remove: WaterBoiling, WaterBoiled
				- Tags
					- idx:0
			- WaterBoiling
				- Remove: WaterHeatingUp, WaterBoiled
				- Tags
					- idx:0
			- WaterBoiled
				- Remove: WaterBoiling, WaterHeatingUp
				- Tags
					- idx:0
					- final
			- GroundCoffeeInMug
				- Tags
					- idx:1
			- WaterInMug
				- Tags
					- idx:2
			- MealReady
				- Auto: true
				- Require: GroundCoffeeInMug, WaterInMug
			
			Example "re-heat meal":
			- OvenPreheated
				- Tags
					- idx:0
			- MealBaking
				- Require: OvenOn
				- Tags
					- time:5m
					- idx:1
			- MealBaked
				- Tags
					- idx:2
			- MealReady
				- Auto: true
				- Require: MealBaked
		`, `
			Skip empty fields (null, false). Start the "idx:" counter from 1. If the same "idx" tag is present for more than 1 state, pick a final state from the same group "idx" group and mark it with a "final" tag (eg WaterBoiled is a final state for WaterBoiling).
		`)
}

type ParamsGenSteps struct {
	Recipe Recipe
}

type ResultGenSteps struct {
	Schema am.Schema
}

// STEP COMMENTS

type PromptGenStepComments = secai.Prompt[ParamsGenStepComments, ResultGenStepComments]

func NewPromptGenStepComments(agent secai.AgentAPI) *PromptGenStepComments {
	return secai.NewPrompt[ParamsGenStepComments, ResultGenStepComments](
		agent, ssC.GenStepComments, `
			- You're a cooking show host.
		`, `
			1. Comment on each of the provided steps. Keep the tone of the character's personality.
			2. Use the full recipe as a context.
		`, `
			Preserve indexes of the steps in the result.
		`)
}

type ParamsGenStepComments struct {
	Steps  []string
	Recipe Recipe
}

type ResultGenStepComments struct {
	// Comments for each step.
	Comments []string
}

// COOKING

type PromptCookingStarted = secai.Prompt[ParamsCookingStarted, ResultCookingStarted]

func NewPromptCookingStarted(agent secai.AgentAPI) *PromptCookingStarted {
	return secai.NewPrompt[ParamsCookingStarted, ResultCookingStarted](
		agent, ssC.StoryCookingStarted, `
			- You're a person who is cooking.
		`, `
			1. Answer questions about the cooking process. Keep the tone of the character's personality.
			2. Use the full recipe and steps as a context.
		`, `
			Answering is optional. Dont answer rhetorical questions or vague statements. Sometimes simply acknowledge the question.
		`)
}

type ParamsCookingStarted struct {
	Recipe         Recipe
	ExtractedSteps []string
}

type ResultCookingStarted struct {
	// Max 2 sentences, min 3 words.
	Answer string
}

// ORIENTING

type PromptOrienting = secai.Prompt[ParamsOrienting, ResultOrienting]

func NewPromptOrienting(agent secai.AgentAPI) *PromptOrienting {
	// TODO add offer menu integration to avoid DUPs
	p := secai.NewPrompt[ParamsOrienting, ResultOrienting](
		agent, ssC.Orienting, `
			- You're a text matcher in a board game.
		`, `
			1. Try to extract a choice from the user, based on provided lists of MovesCooking and MovesStory. 
			2. Distinguish past and present tense in the prompt, when choosing the right cooking step.
			
			Examples:
			- "rice cooked" is "StepRiceCooked"
			- "rice cooking" is "StepRiceCooking"
			- "switch to story ingredients" is "StoryIngredientsPicking"
		`, `
			Reply only if the user gives you a choice.
		`)

	// disable history
	p.HistoryMsgLen = 0
	return p
}

type ParamsOrienting struct {
	Prompt string
	// List of possible cooking choices to take.
	MovesCooking []string
	// List of possible stories to switch to and their descriptions.
	MovesStories map[string]string
}

// TODO add Removing
type ResultOrienting struct {
	// Users choice
	Move string
	// TODO debug
	Reasoning string
	// Certainty is the probability that the next move is correct.
	Certainty float64
}

func (r ResultOrienting) String() string {
	return fmt.Sprintf("%s@%.2f", r.Move, r.Certainty)
}

// TEMPLATE

// type PromptTemplate = secai.Prompt[ParamsTemplate, ResultTemplate]
//
// func NewPromptTemplate(agent secai.AgentAPI) *PromptTemplate {
// 	return secai.NewPrompt[ParamsTemplate, ResultTemplate](
// 		agent, ssC.Template, `
// 			- You're a text and speech database.
// 		`, `
// 			Translate the provided phrases to the expected form, following the character's personality. Keep a humoristic and entertaining tone of a cooking tv show.
// 		`, `
// 			Translate each phrase into 3 different versions. Generate at maximum twice the word count of the original text. Keep the %d and other substitutions in the right place.
// 		`)
// }
//
// type ParamsTemplate struct {
// 	Phrases map[string]string
// }
//
// type ResultTemplate struct {
// 	Phrases map[string][]string
// }

// ///// ///// /////

// ///// STORIES

// ///// ///// /////

// typecheck
var _ shared.StoryImpl[Story] = &Story{}

// Story is the basis for all stories.
type Story struct {
	shared.Story[Story]

	Cook   shared.StoryActor
	Memory shared.StoryActor
}

// Clone returns a copy of the story.
func (s *Story) Clone() *Story {
	clone := *s
	return &clone
}

var StoryWakingUp = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryWakingUp,
			Title: "Waking Up",
			Desc:  "The waking up story is the bot starting on either cold or warm boot.",
		},
	},
	Cook: shared.StoryActor{
		Trigger: amhelp.Cond{
			Not: S{ssC.Ready},
		},
	},
}

var MatchSteps = regexp.MustCompile(`^Step`)
var MatchIngredients = regexp.MustCompile("^Ingredient")

var StoryJoke = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryJoke,
			Title: "Joke",
			Desc:  "In this story the bot tells a joke when asked to, based on the character.",
		},
		// Either 1st time or the current clocks for steps (sum) are equal or greater than ticks of this story's state.
		CanActivate: func(s *Story) bool {
			mem := s.Memory.Mach
			stepStates := mem.StateNamesMatch(MatchSteps)
			stepsNow := mem.TimeSum(stepStates) + s.Epoch
			freq := 1.5
			// freq := 2.0

			return s.Tick == 0 || float64(stepsNow)*freq >= float64(s.Tick)
		},
	},
}

var StoryIngredientsPicking = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryIngredientsPicking,
			Title: "Ingredients Picking",
			Desc:  "The bot asks the user what ingredients they have at hand.",
		},
	},
	Cook: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is:  S{ssC.Ready},
			Not: S{ssC.IngredientsReady, ssC.StoryWakingUp},
		},
	},
}

var StoryRecipePicking = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryRecipePicking,
			Title: "Recipe Picking",
			Desc:  "The bot offers some recipes, based on the ingredients.",
		},
	},
	Cook: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is:  S{ssC.Ready, ssC.IngredientsReady},
			Not: S{ssC.RecipeReady},
		},
	},
}

var StoryCookingStarted = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryCookingStarted,
			Title: "Cooking Started",
			Desc:  "The main story, the bot translates the recipe into actionable steps, then acts on them.",
		},
	},
	Cook: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is: S{ssC.Ready, ssC.RecipeReady},
		},
	},
	Memory: shared.StoryActor{
		Trigger: amhelp.Cond{
			Not: S{MemMealReady},
		},
	},
}

var StoryMealReady = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryMealReady,
			Title: "Meal Ready",
			Desc:  "This story is the end of the flow, the recipe should have been materialized by now.",
		},
	},
	Memory: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is: S{MemMealReady},
		},
	},
}

var StoryMemoryWipe = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryMemoryWipe,
			Title: "Memory Wipe",
			Desc:  "The bot will clean both short term and long term memory.",
		},
	},
}

var StoryStartAgain = &Story{
	Story: shared.Story[Story]{
		StoryInfo: shared.StoryInfo{
			State: ssC.StoryStartAgain,
			Title: "Start Again",
			Desc:  "The session will re-start, keeping the bot's memory.",
		},
	},
}
