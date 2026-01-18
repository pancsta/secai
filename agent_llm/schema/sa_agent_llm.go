// Package schema contains a stateful schema-v2 for AgentLLM.
//
//nolint:lll
package schema

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	ss "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
)

var Sp = shared.Sp

// ///// ///// /////

// ///// STATES

// ///// ///// /////

// LLMAgentStatesDef contains all the states of the LLMAgent state machine. LLMAgent is like the base AgentLLM but
// includes predefined LLM prompts.
type LLMAgentStatesDef struct {
	*am.StatesBase

	// PROMPTS

	// Check if the passed prompt references any of the offered choices.
	CheckingOfferRefs string

	// TODO ideally keep story related prompts here

	*ss.AgentBaseStatesDef
}

type StepsStatesDef struct {
}

// LLMAgentGroupsDef contains all the state groups LLMAgent state machine.
type LLMAgentGroupsDef struct {
}

// LLMAgentSchema represents all relations and properties of LLMAgentStates.
var LLMAgentSchema = SchemaMerge(
	// inherit from AgentLLM
	ss.AgentSchema,

	am.Schema{
		ssL.CheckingOfferRefs: {
			Multi:   true,
			Require: S{ssL.Start},
		},
	})

// EXPORTS AND GROUPS

var (
	ssL = am.NewStates(LLMAgentStatesDef{})
	sgL = am.NewStateGroups(LLMAgentGroupsDef{})

	// LLMAgentStates contains all the states for the LLMAgent machine.
	LLMAgentStates = ssL
	// LLMAgentGroups contains all the state groups for the LLMAgent machine.
	LLMAgentGroups = sgL
)

// NewLLMAgent will create the most basic LLMAgent state machine.
func NewLLMAgent(ctx context.Context) *am.Machine {
	return am.New(ctx, LLMAgentSchema, nil)
}

// ///// ///// /////

// ///// PROMPTS

// ///// ///// /////
// Comments are automatically converted to a jsonschema_description tag.

// CheckingOfferRefs

type PromptCheckingOfferRefs = secai.Prompt[ParamsCheckingOfferRefs, ResultCheckingOfferRefs]

func NewPromptCheckingOfferRefs(agent secai.AgentAPI) *PromptCheckingOfferRefs {
	p := secai.NewPrompt[ParamsCheckingOfferRefs, ResultCheckingOfferRefs](
		agent, ssL.CheckingOfferRefs, `
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

type ParamsCheckingOfferRefs struct {
	Choices []string
	Prompt  string
}

type ResultCheckingOfferRefs struct {
	// The referenced index.
	RefIndex int
}
