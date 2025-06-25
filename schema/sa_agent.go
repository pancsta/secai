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

	// ERRORS

	ErrLLM string
	ErrDB  string
	ErrMem string
	ErrUI  string

	// STATUS

	// Agent is waiting for user input.
	InputPending string
	// User input is blocked by the agent.
	InputBlocked string
	// Agent is currently requesting >=1 tools
	RequestingTool string
	// Agent is currently requesting >=1 LLMs
	RequestingLLM string
	// Requesting implies either RequestingTool or RequestingLLM being active
	Requesting string
	// The machine has been mocked.
	Mock string

	// DB

	BaseDBStarting string
	BaseDBReady    string
	// BaseDBSaving is lazy query execution.
	BaseDBSaving string

	// ACTIONS

	// Loop is the agent-user loop (eg dialogue).
	Loop string
	// Prompt is the text the user has sent us.
	Prompt string
	// Interrupted is when the user interrupts the agent, until Resume.
	Interrupted string
	// Resume is the signal from the user to resume after an Interrupted.
	Resume string
	// Msg will output the passed text into the UI.
	Msg string

	// UI

	UIMode  string
	UIReady string
	// UI button "Send" has been pressed
	UIButtonSend string
	// UI button "Interrupt" has been pressed
	UIButtonIntt   string
	UISaveOutput   string
	UICleanOutput  string
	UISessConn     string
	UISessDisconn  string
	UISrvListening string
	// TODO UISessChange

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
var AgentSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicSchema,
	// inherit from DisposedStruct
	ssam.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.WorkerSchema,
	am.Schema{

		// ERRORS

		ssA.ErrLLM: {
			Multi:   true,
			Require: S{Exception},
		},
		ssA.ErrDB: {
			Multi:   true,
			Require: S{Exception},
		},
		ssA.ErrMem: {
			Multi:   true,
			Require: S{Exception},
		},
		ssA.ErrUI: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},

		// BASIC OVERRIDES

		ssA.Start: {Add: S{ssA.BaseDBStarting}},
		ssA.Ready: {
			Require: S{ssA.Start},
			Add:     S{ssA.Loop},
		},

		// STATUS

		ssA.Loop:         {Require: S{ssA.Ready}},
		ssA.InputPending: {Remove: S{ssA.Prompt}},
		ssA.InputBlocked: {Remove: S{ssA.Prompt}},
		ssA.Requesting:   {},
		ssA.RequestingTool: {
			Multi: true,
			Add:   S{ssA.Requesting},
		},
		ssA.RequestingLLM: {
			Multi: true,
			Add:   S{ssA.Requesting},
		},
		ssA.Mock: {},

		// DB

		ssA.BaseDBStarting: {
			Require: S{ssA.Start},
			Remove:  S{ssA.BaseDBReady},
		},
		ssA.BaseDBReady: {
			Require: S{ssA.Start},
			Remove:  S{ssA.BaseDBStarting},
		},
		ssA.BaseDBSaving: {Multi: true},

		// ACTIONS

		ssA.Prompt: {
			Multi:   true,
			Require: S{ssA.Start},
			Remove:  S{ssA.InputPending},
		},
		ssA.Interrupted: {
			Add:    S{ssA.InputPending},
			Remove: S{ssA.Resume},
		},
		ssA.Resume: {
			Remove: S{ssA.Interrupted},
		},
		ssA.Msg: {
			Multi:   true,
			Require: S{ssA.Start},
		},

		// UI

		ssA.UIMode:       {},
		ssA.UIReady:      {Require: S{ssA.UIMode}},
		ssA.UIButtonSend: {Require: S{ssA.UIMode}},
		ssA.UIButtonIntt: {Require: S{ssA.UIMode}},
		ssA.UISaveOutput: {Require: S{ssA.UIMode}},
		ssA.UICleanOutput: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UISessConn: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UISessDisconn: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UISrvListening: {Require: S{ssA.UIMode}},
	})

// EXPORTS AND GROUPS

// TagPrompt is for states with LLM prompts.
const TagPrompt = "prompt"

// TagManual is for stories that CANNOT be triggered by the LLM orienting story.
const TagManual = "manual"

// TagTrigger is for stories that can be triggered by the LLM orienting story.
const TagTrigger = "trigger"

var (
	ssA = am.NewStates(AgentStatesDef{})
	sgA = am.NewStateGroups(AgentGroupsDef{})

	// AgentStates contains all the states for the Agent machine.
	AgentStates = ssA
	// AgentGroups contains all the state groups for the Agent machine.
	AgentGroups = sgA
)
