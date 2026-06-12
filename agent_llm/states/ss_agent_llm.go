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

	RestoreCharacter string
	GenCharacter     string
	CharacterReady   string

	RestoreResources string
	GenResources     string
	ResourcesReady   string

	// The LLM is given possible moves and checks if the user wants to make any. Orienting usually runs in parallel with
	// other prompts. After reaching the required level of certainty, it fills outs `h.MoveOrienting`.
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

// AgentLLMSchema represents all relations and properties of AgentLLMStates.
// inherit from AgentLLM
var AgentLLMSchema = ss.AgentSchema.Merge(am.Schema{
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

	ssL.Orienting: {
		Require: S{ssL.Start},
		Multi:   true,
		Tags:    S{ss.TagPrompt},
	},

	ssL.OrientingMove: {},
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
	return am.New(ctx, AgentLLMSchema, nil)
}
