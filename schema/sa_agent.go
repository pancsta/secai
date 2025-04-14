// Package schema contains a stateful schema-v2 for Agent.
// Bootstrapped with am-gen. Edit manually or re-gen & merge.
package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
)

// AgentStatesDef contains all the states of the Agent state machine.
type AgentStatesDef struct {
	*am.StatesBase

	// STATUS

	InputPending string
	// Agent is currently requesting >=1 tools
	RequestingTool string
	// Agent is currently requesting >=1 LLMs
	RequestingLLM string
	// Requesting implies either RequestingTool or RequestingLLM being active
	Requesting string

	// DB

	DBStarting string
	DBReady    string
	DBSaving   string

	// SCENARIOS

	// ScenarioMatch    string
	// ScenarioQuestion string
	// ScenarioTool     string

	// ACTIONS

	// Loop is the main agent loop
	Loop      string
	Prompt    string
	Interrupt string

	// UI

	UIMode        string
	UIReady       string
	UIButtonPress string
	UISaveOutput  string
	UICleanOutput string
	UIErr         string

	// inherit from BasicStatesDef
	*ssam.BasicStatesDef
	// inherit from DisposedStatesDef
	*ssam.DisposedStatesDef
	// inherit from WorkerStatesDef
	*ssrpc.WorkerStatesDef
}

// AgentGroupsDef contains all the state groups Agent state machine.
type AgentGroupsDef struct{}

// AgentSchema represents all relations and properties of AgentStates.
// TODO refac Struct -> Schema
var AgentSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicStruct,
	// inherit from DisposedStruct
	ssam.DisposedStruct,
	// inherit from WorkerStates
	ssrpc.WorkerStruct,
	am.Schema{

		// BASIC OVERRIDES

		ssA.Start: {Add: S{ssA.DBStarting}},
		ssA.Ready: {
			Require: S{ssA.Start},
			Add:     S{ssA.Loop},
		},

		// STATUS

		ssA.Loop:         {Require: S{ssA.Ready}},
		ssA.InputPending: {Remove: S{ssA.Prompt}},
		ssA.Requesting:   {},
		ssA.RequestingTool: {
			Multi: true,
			Add:   S{ssA.Requesting},
		},
		ssA.RequestingLLM: {
			Multi: true,
			Add:   S{ssA.Requesting},
		},

		// DB

		ssA.DBStarting: {
			Require: S{ssA.Start},
			Remove:  S{ssA.DBReady},
		},
		ssA.DBReady: {
			Require: S{ssA.Start},
			Remove:  S{ssA.DBStarting},
		},
		ssA.DBSaving: {Multi: true},

		// TODO SCENARIOS

		// ssA.ScenarioMatch:    {},
		// ssA.ScenarioQuestion: {},
		// ssA.ScenarioTool:     {},

		// ACTIONS

		ssA.Prompt: {
			Require: S{ssA.Start},
			Remove:  S{ssA.InputPending},
		},
		ssA.Interrupt: {
			Add:    S{ssA.InputPending},
			Remove: S{ssA.Prompt},
		},

		// UI

		ssA.UIMode:        {},
		ssA.UIReady:       {Require: S{ssA.UIMode}},
		ssA.UIButtonPress: {Require: S{ssA.UIMode}},
		ssA.UISaveOutput:  {Require: S{ssA.UIMode}},
		ssA.UICleanOutput: {Require: S{ssA.UIMode}},
		ssA.UIErr:         {Require: S{ssA.UIMode}},
	})

// EXPORTS AND GROUPS

var (
	ssA = am.NewStates(AgentStatesDef{})
	sgA = am.NewStateGroups(AgentGroupsDef{})

	// AgentStates contains all the states for the Agent machine.
	AgentStates = ssA
	// AgentGroups contains all the state groups for the Agent machine.
	AgentGroups = sgA
)
