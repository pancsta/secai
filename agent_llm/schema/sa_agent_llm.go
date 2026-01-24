// Package schema contains a stateful schema-v2 for AgentLLM.
//
//nolint:lll
package schema

import (
	"fmt"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/agent_llm/states"
	"github.com/pancsta/secai/shared"
)

var ss = states.AgentLLMStates

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////
// Comments are automatically converted to a jsonschema_description tag.

// MENU

type PromptConfigTest = secai.Prompt[struct{}, struct{}]

func NewPromptConfigTest(agent shared.AgentBaseAPI) *PromptConfigTest {
	p := secai.NewPrompt[struct{}, struct{}](
		agent, ss.ConfigValidating, ``, `
			Reply OK.
		`, ``)
	p.HistoryMsgLen = 0

	return p
}

// MENU

type PromptCheckingMenuRefs = secai.Prompt[ParamsCheckingMenuRefs, ResultCheckingMenuRefs]

func NewPromptCheckingMenuRefs(agent shared.AgentBaseAPI) *PromptCheckingMenuRefs {
	p := secai.NewPrompt[ParamsCheckingMenuRefs, ResultCheckingMenuRefs](
		agent, ss.CheckingMenuRefs, `
			- you're a natural language processor
		`, `
			1. Check if the prompt references any of the offered choices.
			2. Consider the index number and the text of each item.
		`, `
			Return a 0-based index number of the referenced choice, or -1 if none.
		`)
	p.HistoryMsgLen = 0

	return p
}

type ParamsCheckingMenuRefs struct {
	Choices []string
	Prompt  string
}

type ResultCheckingMenuRefs struct {
	// The referenced index.
	RefIndex int
}

// CHARACTER

type PromptGenCharacter = secai.Prompt[ParamsGenCharacter, ResultGenCharacter]

func NewPromptGenCharacter(agent shared.AgentBaseAPI) *PromptGenCharacter {
	return secai.NewPrompt[ParamsGenCharacter, ResultGenCharacter](
		agent, ss.GenCharacter, `
			- You're generating a character which will lead a live cooking show.
			- You're being given a vague character's profession and the current year.
		`, `
			1. Generate info related to conversations and cooking.
			2. Add some personality to the character.
			3. Assign a more specific profession from the requested period.
			4. Assign a name.
		`, ``)
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

func NewPromptGenResources(agent shared.AgentBaseAPI) *PromptGenResources {
	return secai.NewPrompt[ParamsGenResources, ResultGenResources](
		agent, ss.GenResources, `
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

// ORIENTING

type PromptOrienting = secai.Prompt[ParamsOrienting, ResultOrienting]

func NewPromptOrienting(agent shared.AgentBaseAPI) *PromptOrienting {
	// TODO add offer menu integration to avoid DUPs
	p := secai.NewPrompt[ParamsOrienting, ResultOrienting](
		agent, ss.Orienting, `
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
	// List of possible agent-specific choices to take.
	MovesAgent map[string]string
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
