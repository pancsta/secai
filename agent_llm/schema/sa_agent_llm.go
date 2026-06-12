// Package schema contains a stateful schema-v2 for AgentLLM.
//
//nolint:lll
package schema

import (
	"fmt"

	"github.com/pancsta/secai/agent_llm/states"
	"github.com/pancsta/secai/shared"
)

var ss = states.AgentLLMStates

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////
// Comments are automatically converted to a jsonschema_description tag.

// Test AI connection.

type PromptConfigTest = shared.Prompt[struct{}, struct{}]

func NewPromptConfigTest(agent shared.AgentBaseAPI) *PromptConfigTest {
	p := shared.NewPrompt[struct{}, struct{}](
		agent, ss.ConfigValidating, ``, `
			Reply OK.
		`, ``)
	p.HistoryMsgLen = 0

	return p
}

// MENU

type PromptCheckingMenuRefs = shared.Prompt[ParamsCheckingMenuRefs, ResultCheckingMenuRefs]

func NewPromptCheckingMenuRefs(agent shared.AgentBaseAPI) *PromptCheckingMenuRefs {
	p := shared.NewPrompt[ParamsCheckingMenuRefs, ResultCheckingMenuRefs](
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

type PromptGenCharacter = shared.Prompt[ParamsGenCharacter, ResultGenCharacter]

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

type PromptGenResources = shared.Prompt[ParamsGenResources, ResultGenResources]

type ParamsGenResources struct {
	Phrases map[string]string
}

type ResultGenResources struct {
	Phrases map[string][]string
}

// ORIENTING

type PromptOrienting = shared.Prompt[ParamsOrienting, ResultOrienting]

type ParamsOrienting struct {
	Prompt string
	// List of possible agent-specific choices to take.
	MovesWorkflow map[string]string
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
	Certainty float32
}

func (r ResultOrienting) String() string {
	return fmt.Sprintf("%s@%.2f", r.Move, r.Certainty)
}
