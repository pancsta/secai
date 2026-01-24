//nolint:lll
package schema

import (
	"regexp"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	sa "github.com/pancsta/secai/agent_llm/schema"
	"github.com/pancsta/secai/examples/cook/states"
	"github.com/pancsta/secai/shared"
)

var sp = shared.Sp
var ss = states.CookStates

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////
// Comments are automatically converted to a jsonschema_description tag.

// RESOURCES DATA

// TODO enum for keys (use state names opt?)

var LLMResources = sa.ParamsGenResources{
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
		ss.StoryMealReady: "We made it, the meal is ready! You can enjoy it now. I hope you had fun cooking with us.",
		"ReqLimitReached": "You have reached the limit of %d requests per session. Please come back later.",
	},
}

// TODO static resources

// JOKES

type PromptGenJokes = secai.Prompt[ParamsGenJokes, ResultGenJokes]

func NewPromptGenJokes(agent shared.AgentBaseAPI) *PromptGenJokes {
	return secai.NewPrompt[ParamsGenJokes, ResultGenJokes](
		agent, ss.GenJokes, `
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

func NewPromptIngredientsPicking(agent shared.AgentBaseAPI) *PromptIngredientsPicking {
	return secai.NewPrompt[ParamsIngredientsPicking, ResultIngredientsPicking](
		agent, ss.StoryIngredientsPicking, `
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

func NewPromptRecipePicking(agent shared.AgentBaseAPI) *PromptRecipePicking {
	return secai.NewPrompt[ParamsRecipePicking, ResultRecipePicking](
		agent, ss.StoryRecipePicking, `
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

func NewPromptGenSteps(agent shared.AgentBaseAPI) *PromptGenSteps {
	p := secai.NewPrompt[ParamsGenSteps, ResultGenSteps](
		agent, ss.GenSteps, `
			- You're a cooking process planner.
		`, `
			1. Extract actionable steps from the cooking recipe and represent them as binary flags called "states". Each step can represent either a long-running action (eg WaterHeatingUp), a short-running action (WaterBoiling), a fact (WaterBoiled). Each state can relate to any other state via Require, Remove, and Add relation.
			1. The final and mandatory state is called MealReady.
			1. Not all the states have to be connected with relations.
			1. Put the time length of procedures (if given) inside Tags as "time:5m" to wait for 5min.
			1. Index the steps using a tag "idx:4" for the 5th step in the input. Steps which can't be active at the same time should have Remove relation between them.
			
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
			2 states CAN'T require and remove each other - these relations are for a single point in time. Skip empty fields (null, false). Start the "idx:" counter from 1. If the same "idx" tag is present for more than 1 state, pick a final state from the same group "idx" group and mark it with a "final" tag (eg WaterBoiled is a final state for WaterBoiling).
		`)

	// short history
	p.HistoryMsgLen = 1

	return p
}

type ParamsGenSteps struct {
	Recipe Recipe
}

type ResultGenSteps struct {
	Schema am.Schema
}

// STEP COMMENTS

type PromptGenStepComments = secai.Prompt[ParamsGenStepComments, ResultGenStepComments]

func NewPromptGenStepComments(agent shared.AgentBaseAPI) *PromptGenStepComments {
	return secai.NewPrompt[ParamsGenStepComments, ResultGenStepComments](
		agent, ss.GenStepComments, `
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

func NewPromptCookingStarted(agent shared.AgentBaseAPI) *PromptCookingStarted {
	return secai.NewPrompt[ParamsCookingStarted, ResultCookingStarted](
		agent, ss.StoryCookingStarted, `
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

var StoryWakingUp = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryWakingUp,
		Title: "Waking Up",
		Desc:  "The waking up story is the bot starting on either cold or warm boot.",
	},
	Agent: shared.StoryActor{
		Trigger: amhelp.Cond{
			Not: am.S{ss.Ready},
		},
	},
}

var MatchSteps = regexp.MustCompile(`^Step`)
var MatchIngredients = regexp.MustCompile("^Ingredient")

var StoryJoke = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryJoke,
		Title: "Joke",
		Desc:  "In this story the bot tells a joke when asked to, based on the character.",
	},
	// Either 1st time or the current clocks for steps (sum) are equal or greater than ticks of this story's state.
	CanActivate: func(s *shared.Story) bool {
		mem := s.Memory.Mach
		stepStates := mem.StateNamesMatch(MatchSteps)
		stepsNow := mem.Time(stepStates).Sum(nil) + s.Epoch
		freq := 1.5
		// freq := 2.0

		return s.Tick == 0 || float64(stepsNow)*freq >= float64(s.Tick)
	},
}

var StoryIngredientsPicking = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryIngredientsPicking,
		Title: "Ingredients Picking",
		Desc:  "The bot asks the user what ingredients they have at hand.",
	},
	Agent: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is:  am.S{ss.Ready},
			Not: am.S{ss.IngredientsReady, ss.StoryWakingUp},
		},
	},
}

var StoryRecipePicking = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryRecipePicking,
		Title: "Recipe Picking",
		Desc:  "The bot offers some recipes, based on the ingredients.",
	},
	Agent: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is:  am.S{ss.Ready, ss.IngredientsReady},
			Not: am.S{ss.RecipeReady},
		},
	},
}

var StoryCookingStarted = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryCookingStarted,
		Title: "Cooking Started",
		Desc:  "The main story, the bot translates the recipe into actionable steps, then comments while the user completes them.",
	},
	Agent: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is: am.S{ss.Ready, ss.RecipeReady},
		},
	},
	Memory: shared.StoryActor{
		Trigger: amhelp.Cond{
			Not: am.S{states.MemMealReady},
		},
	},
}

var StoryMealReady = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryMealReady,
		Title: "Meal Ready",
		Desc:  "This story is the end of the flow, the recipe should have been materialized by now.",
	},
	Memory: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is: am.S{states.MemMealReady},
		},
	},
}

var StoryMemoryWipe = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryMemoryWipe,
		Title: "Memory Wipe",
		Desc:  "The bot will clean both short term and long term memory.",
	},
}

var StoryStartAgain = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryStartAgain,
		Title: "Start Again",
		Desc:  "The session will re-start, keeping the bot's memory.",
	},
}
