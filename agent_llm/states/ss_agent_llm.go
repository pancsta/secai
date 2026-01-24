package states

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ss "github.com/pancsta/secai/states"
)

// ///// ///// /////

// ///// STATES

// ///// ///// /////

// AgentLLMStatesDef contains all the states of the LLMAgent state machine. LLMAgent is like the base AgentLLM but
// includes predefined LLM prompts.
type AgentLLMStatesDef struct {
	*am.StatesBase

	// PROMPTS

	// Check if the passed prompt references any of the offered choices.
	CheckingMenuRefs string

	RestoreCharacter string
	GenCharacter     string
	CharacterReady   string

	RestoreResources string
	GenResources     string
	ResourcesReady   string

	// The LLM is given possible moves and checks if the user wants to make any. Orienting usually runs in parallel with other prompts. After de-activation, it leaves results in handler struct `h.oriented`.
	Orienting string
	// OrientingMove performs a move decided upon by Orienting.
	OrientingMove string

	// TODO ideally keep story related prompts here

	*ss.AgentBaseStatesDef
}

// AgentLLMGroupsDef contains all the state groups LLMAgent state machine.
type AgentLLMGroupsDef struct {
	// All the states for the character generation.
	Character S
	// All the states for resource generation.
	Resources S
}

// LLMAgentSchema represents all relations and properties of AgentLLMStates.
var LLMAgentSchema = SchemaMerge(
	// inherit from AgentLLM
	ss.AgentSchema,

	am.Schema{
		ssL.CheckingMenuRefs: {
			Multi:   true,
			Require: S{ssL.Start},
		},

		ssL.RestoreCharacter: {
			Auto:    true,
			Require: S{ssL.BaseDBReady},
			Remove:  sgL.Character,
		},
		ssL.GenCharacter: {
			Require: S{ssL.BaseDBReady},
			Remove:  sgL.Character,
			Tags:    S{ss.TagPrompt, ss.TagTrigger},
		},
		ssL.CharacterReady: {Remove: sgL.Character},

		ssL.RestoreResources: {
			Auto:    true,
			Require: S{ssL.CharacterReady, ssL.BaseDBReady},
			Remove:  sgL.Resources,
		},
		ssL.GenResources: {
			Require: S{ssL.CharacterReady, ssL.BaseDBReady},
			Remove:  sgL.Resources,
			Tags:    S{ss.TagPrompt, ss.TagTrigger},
		},
		ssL.ResourcesReady: {Remove: sgL.Resources},
	})

// EXPORTS AND GROUPS

var (
	ssL = am.NewStates(AgentLLMStatesDef{})
	sgL = am.NewStateGroups(AgentLLMGroupsDef{
		Character: S{ssL.CharacterReady, ssL.RestoreCharacter, ssL.GenCharacter},
		Resources: S{ssL.ResourcesReady, ssL.RestoreResources, ssL.GenResources},
	}, ss.AgentBaseGroups)

	// AgentLLMStates contains all the states for the LLMAgent machine.
	AgentLLMStates = ssL
	// AgentLLMGroups contains all the state groups for the LLMAgent machine.
	AgentLLMGroups = sgL
)

// NewAgentLLM will create the most basic LLMAgent state machine.
func NewAgentLLM(ctx context.Context) *am.Machine {
	return am.New(ctx, LLMAgentSchema, nil)
}
