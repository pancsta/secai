// Package schema contains a stateful schema-v2 for AgentBase, Mem, and Tool.
package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
)

// AgentBaseStatesDef contains all the states of the AgentLLM state machine.
type AgentBaseStatesDef struct {
	*am.StatesBase

	// ERRORS

	ErrLLM string
	ErrDB  string
	ErrMem string
	ErrUI  string

	// STATUS

	// AgentLLM is waiting for user input.
	InputPending string
	// User input is blocked by the agent.
	InputBlocked string
	// AgentLLM is currently requesting >=1 tools
	RequestingTool string
	// AgentLLM is currently requesting >=1 LLMs
	RequestingLLM string
	// Requesting implies either RequestingTool or RequestingLLM being active
	Requesting string
	// The machine has been mocked.
	Mock string

	// DB

	BaseDBStarting string
	BaseDBReady    string
	// BaseDBSaving is lazy query execution.
	BaseDBSaving      string
	HistoryDBStarting string
	HistoryDBReady    string

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
	// inherit from NetSourceStatesDef
	*ssrpc.NetSourceStatesDef
}

// AgentBaseGroupsDef contains all the state groups AgentLLM state machine.
type AgentBaseGroupsDef struct{}

// AgentSchema represents all relations and properties of AgentBaseStates.
var AgentSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicSchema,
	// inherit from DisposedStruct
	ssam.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.NetSourceSchema,
	am.Schema{

		// ERRORS

		ssAB.ErrLLM: {
			Multi:   true,
			Require: S{Exception},
		},
		ssAB.ErrDB: {
			Multi:   true,
			Require: S{Exception},
		},
		ssAB.ErrMem: {
			Multi:   true,
			Require: S{Exception},
		},
		ssAB.ErrUI: {
			Multi:   true,
			Require: S{ssAB.UIMode},
		},

		// BASIC OVERRIDES

		ssAB.Start: {Add: S{ssAB.BaseDBStarting, ssAB.HistoryDBStarting}},
		ssAB.Ready: {
			Require: S{ssAB.Start},
			Add:     S{ssAB.Loop},
		},

		// STATUS

		ssAB.Loop:         {Require: S{ssAB.Ready}},
		ssAB.InputPending: {Remove: S{ssAB.Prompt}},
		ssAB.InputBlocked: {Remove: S{ssAB.Prompt}},
		ssAB.Requesting:   {},
		ssAB.RequestingTool: {
			Multi: true,
			Add:   S{ssAB.Requesting},
		},
		ssAB.RequestingLLM: {
			Multi: true,
			Add:   S{ssAB.Requesting},
		},
		ssAB.Mock: {},

		// DB

		ssAB.BaseDBStarting: {
			Require: S{ssAB.Start},
			Remove:  S{ssAB.BaseDBReady},
		},
		ssAB.BaseDBReady: {
			Require: S{ssAB.Start},
			Remove:  S{ssAB.BaseDBStarting},
		},
		ssAB.BaseDBSaving: {Multi: true},
		ssAB.HistoryDBStarting: {
			Require: S{ssAB.Start},
			Remove:  S{ssAB.HistoryDBReady},
		},
		ssAB.HistoryDBReady: {
			Require: S{ssAB.Start},
			Remove:  S{ssAB.HistoryDBStarting},
		},

		// ACTIONS

		ssAB.Prompt: {
			Multi:   true,
			Require: S{ssAB.Start},
			Remove:  S{ssAB.InputPending},
		},
		ssAB.Interrupted: {
			Add:    S{ssAB.InputPending},
			Remove: S{ssAB.Resume},
		},
		ssAB.Resume: {
			Remove: S{ssAB.Interrupted},
		},
		ssAB.Msg: {
			Multi:   true,
			Require: S{ssAB.Start},
		},

		// UI

		ssAB.UIMode:       {},
		ssAB.UIReady:      {Require: S{ssAB.UIMode}},
		ssAB.UIButtonSend: {Require: S{ssAB.UIMode}},
		ssAB.UIButtonIntt: {Require: S{ssAB.UIMode}},
		ssAB.UISaveOutput: {Require: S{ssAB.UIMode}},
		ssAB.UICleanOutput: {
			Multi:   true,
			Require: S{ssAB.UIMode},
		},
		ssAB.UISessConn: {
			Multi:   true,
			Require: S{ssAB.UIMode},
		},
		ssAB.UISessDisconn: {
			Multi:   true,
			Require: S{ssAB.UIMode},
		},
		ssAB.UISrvListening: {Require: S{ssAB.UIMode}},
	})

// EXPORTS AND GROUPS

// TagPrompt is for states with LLM prompts.
const TagPrompt = "prompt"

// TagManual is for stories that CANNOT be triggered by the LLM orienting story.
const TagManual = "manual"

// TagTrigger is for stories that can be triggered by the LLM orienting story.
const TagTrigger = "trigger"

var (
	ssAB = am.NewStates(AgentBaseStatesDef{})
	sgAB = am.NewStateGroups(AgentBaseGroupsDef{})

	// AgentBaseStates contains all the states for the AgentLLM machine.
	AgentBaseStates = ssAB
	// AgentBaseGroups contains all the state groups for the AgentLLM machine.
	AgentBaseGroups = sgAB
)

// ///// ///// /////

// ///// MEMORY

// ///// ///// /////

// MemStatesDef contains all the states of the Mem state machine.
type MemStatesDef struct {
	*am.StatesBase
}

// MemGroupsDef contains all the state groups Mem state machine.
type MemGroupsDef struct {
}

// MemSchema represents all relations and properties of MemStates.
var MemSchema = am.Schema{
	// memory has no schema (except Exception)
}

// EXPORTS AND GROUPS

var (
	ssM = am.NewStates(MemStatesDef{})
	sgM = am.NewStateGroups(MemGroupsDef{})

	// MemStates contains all the states for the Mem machine.
	MemStates = ssM
	// MemGroups contains all the state groups for the Mem machine.
	MemGroups = sgM
)

// ///// ///// /////

// ///// TOOL

// ///// ///// /////

// ToolStatesDef contains all the states of the Tool state machine.
type ToolStatesDef struct {
	*am.StatesBase

	// STATUS

	Working string
	Idle    string

	// inherit from BasicStatesDef
	*ssam.BasicStatesDef
	// inherit from DisposedStatesDef
	*ssam.DisposedStatesDef
	// inherit from NetSourceStatesDef
	*ssrpc.NetSourceStatesDef
}

// ToolGroupsDef contains all the state groups Tool state machine.
type ToolGroupsDef struct {
}

// ToolSchema represents all relations and properties of ToolStates.
var ToolSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicSchema,
	// inherit from DisposedStruct
	ssam.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.NetSourceSchema,
	am.Schema{

		// status

		ssT.Working: {
			Require: S{ssT.Ready},
			Remove:  S{ssT.Idle},
		},
		ssT.Idle: {
			Auto:    true,
			Require: S{ssT.Ready},
			Remove:  S{ssT.Working},
		},
	})

// EXPORTS AND GROUPS

var (
	ssT = am.NewStates(ToolStatesDef{})
	sgT = am.NewStateGroups(ToolGroupsDef{})

	// ToolStates contains all the states for the Tool machine.
	ToolStates = ssT
	// ToolGroups contains all the state groups for the Tool machine.
	ToolGroups = sgT
)

// ///// ///// /////

// ///// COMMON APIS

// ///// ///// /////

type Website struct {
	URL     string `description:"The URL of the website."`
	Title   string `description:"The title of the website."`
	Content string `description:"The content or a snippet of the website."`
}
