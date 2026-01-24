package states

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	ssllm "github.com/pancsta/secai/agent_llm/states"
	ssbase "github.com/pancsta/secai/states"
)

// aliases

var ssLLM = ssllm.AgentLLMStates
var saLLM = ssllm.LLMAgentSchema

// ///// ///// /////

// ///// STATES

// ///// ///// /////

// CookStatesDef contains all the states of the Cook state machine.
type CookStatesDef struct {
	*am.StatesBase

	ErrIngredients string
	ErrCooking     string

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

	// prompts

	RestoreJokes string
	GenJokes     string
	JokesReady   string

	GenSteps string
	// StepsReady implies the steps have been translated into actionable memory.
	StepsReady string

	GenStepComments   string
	StepCommentsReady string

	// inherit from LLM AgentLLM
	*ssllm.AgentLLMStatesDef
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

	// All the states for jokes generation.
	Jokes S
	// List of main flow states.
	MainFlow S
}

// CookSchema represents all relations and properties of CookStates.
var CookSchema = SchemaMerge(
	// inherit from LLM AgentLLM
	ssllm.LLMAgentSchema,
	am.Schema{

		// errors

		ssC.ErrIngredients: {},
		ssC.ErrCooking:     {},

		// flow

		ssC.IngredientsReady: {},
		ssC.RecipeReady:      {Require: S{ssC.IngredientsReady}},
		ssC.StepCompleted:    {Multi: true},
		// TODO
		// ssC.StaleDialog:          {Require: S{ssC.Ready}},

		// stories

		ssC.StoryJoke:               {},
		ssC.StoryWakingUp:           {Tags: S{ssbase.TagManual}},
		ssC.StoryIngredientsPicking: {Tags: S{ssbase.TagPrompt}},
		ssC.StoryRecipePicking:      {Tags: S{ssbase.TagPrompt}},
		ssC.StoryCookingStarted:     {Tags: S{ssbase.TagPrompt}},
		ssC.StoryMealReady: {
			Tags:   S{ssbase.TagPrompt, ssbase.TagManual},
			Remove: S{ssC.InputPending},
		},
		ssC.StoryMemoryWipe: {Tags: S{ssbase.TagPrompt}},
		ssC.StoryStartAgain: {Tags: S{ssbase.TagPrompt}},
		// TODO
		// ssC.StorySmallTalk:          {Tags: S{ssbase.TagPrompt}},
		// ssC.StoryWeatherTalk:        {Tags: S{ssbase.TagPrompt}},

		// gen AI

		ssC.RestoreJokes: {
			Auto:    true,
			Require: S{ssC.DBReady, ssC.CharacterReady},
			Remove:  sgC.Jokes,
		},
		ssC.GenJokes: {
			Require: S{ssC.CharacterReady, ssC.DBReady},
			Remove:  sgC.Jokes,
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.JokesReady: {Remove: sgC.Jokes},

		ssC.GenSteps: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted},
			Remove:  S{ssC.StepsReady},
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.StepsReady: {
			Require: S{ssC.RecipeReady},
			Remove:  S{ssC.GenSteps},
		},

		ssC.GenStepComments: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted, ssC.StepsReady},
			Remove:  S{ssC.StepCommentsReady},
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.StepCommentsReady: {Remove: S{ssC.GenStepComments}},

		ssC.Orienting: {
			Multi: true,
			Tags:  S{ssbase.TagPrompt},
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
		Interruptable: SAdd(S{ssC.CheckingMenuRefs}, stories),

		Jokes: S{ssC.JokesReady, ssC.RestoreJokes, ssC.GenJokes},
	}, ssllm.AgentLLMGroups)

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

// TODO schema, define the base memory in ssbase
var MemMealReady = "StepMealReady"
