// Package schema contains a stateful schema-v2 for AgentBase, Mem, and Tool.
package states

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
)

// AgentBaseStatesDef contains all the states of the Agent state machine.
type AgentBaseStatesDef struct {
	*am.StatesBase

	// ERRORS

	ErrAI  string
	ErrDB  string
	ErrMem string
	ErrUI  string
	// Generic web UI err
	ErrWeb string
	// Sharing PTY over web err
	ErrWebPTY string

	// STATUS

	ConfigUpdate     string
	ConfigValidating string
	ConfigValid      string
	// Agent is waiting for user input.
	InputPending string
	// User input is blocked by the agent.
	InputBlocked string
	// Requesting implies either RequestingTool or RequestingAI being active
	Requesting string
	// Agent is currently requesting >=1 LLMs
	RequestingAI string
	// AI request ended
	RequestedAI string
	// Agent is currently requesting >=1 tools
	RequestingTool string
	// Tool request ended
	RequestedTool string
	// The machine has been mocked.
	Mock string

	// DB

	BaseDBStarting string
	BaseDBReady    string
	// BaseDBSaving is lazy query execution.
	BaseDBSaving      string
	DBStarting        string
	DBReady           string
	HistoryDBStarting string
	HistoryDBReady    string

	// EVENTS

	// Loop is the agent-user loop (eg dialogue).
	Loop string
	// Prompt is the text the user has sent us.
	Prompt string
	// Interrupted is when the user interrupts the agent, until Resume.
	Interrupted string
	// Resume is the signal from the user to resume after an Interrupted.
	Resume string

	// STORIES

	// Check the status of all the stories.
	CheckStories string
	// At least one of the stories has changed its status (active / inactive).
	StoryChanged string
	// Call an action with a specified ID.
	StoryAction string

	// UI

	UIMode  string
	UIReady string
	// UI button "Send" has been pressed
	UIButtonSend string
	// UI button "Interrupt" has been pressed
	UIButtonInter string
	// TODO
	UISaveOutput  string
	UICleanOutput string
	// UIMsg will output the passed text into the UI.
	UIMsg           string
	UIRenderStories string
	// debounce state for UIRenderClock
	UIUpdateClock string
	UIRenderClock string
	// new SSH session connected
	SSHConn string
	// SSH session disconnected
	SSHDisconn string
	// TODO SSHSessChange?
	// SSH server listening
	SSHReady string
	// web SSH fwrder listening
	WebSSHReady string
	// web server listening
	WebHTTPReady string
	// web aRPC listening for both WebSocket servers
	WebRPCReady string
	// at least one browser is currently connected
	WebConnected string
	// agent is connected to the browser RPC server
	RemoteDashReady string
	RemoteUIReady   string

	// DEBUG

	// embedded am-dbg running
	Debugger string
	// embedded REPL running
	REPL string

	// inherit from BasicStatesDef
	*ssam.BasicStatesDef
	// inherit from DisposedStatesDef
	*ssam.DisposedStatesDef
	// inherit from StateSourceStatesDef
	*ssrpc.StateSourceStatesDef
}

// AgentBaseGroupsDef contains all the state groups Agent state machine.
type AgentBaseGroupsDef struct {
	UI am.S
}

// AgentSchema represents all relations and properties of AgentBaseStates.
var AgentSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicSchema,
	// inherit from DisposedStruct
	ssam.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.StateSourceSchema,
	am.Schema{

		// ERRORS

		ssA.ErrAI: {
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
		ssA.ErrWeb: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.ErrWebPTY: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},

		// BASIC OVERRIDES

		ssA.Start: {Add: S{ssA.BaseDBStarting, ssA.HistoryDBStarting}},
		ssA.Ready: {
			Require: S{ssA.Start},
			Add:     S{ssA.Loop},
		},

		// STATUS

		ssA.ConfigUpdate: {
			Multi:  true,
			Remove: S{ssA.ConfigValid, ssA.ConfigValidating},
		},
		ssA.ConfigValidating: {
			Auto:    true,
			Require: S{ssA.Start},
			Remove:  S{ssA.ConfigValid},
		},
		ssA.ConfigValid: {Remove: S{ssA.ConfigValidating}},

		ssA.Loop:         {Require: S{ssA.Ready}},
		ssA.InputPending: {Remove: S{ssA.Prompt}},
		ssA.InputBlocked: {Remove: S{ssA.Prompt}},
		ssA.Requesting:   {},
		ssA.RequestingAI: {
			Multi:   true,
			Require: S{ssA.Start},
		},
		ssA.RequestedAI: {
			Multi:   true,
			Require: S{ssA.Start},
		},
		ssA.RequestingTool: {
			Multi:   true,
			Require: S{ssA.Start},
		},
		ssA.RequestedTool: {
			Multi:   true,
			Require: S{ssA.Start},
		},
		ssA.Mock: {},

		// DB
		// db states dont require Start

		ssA.BaseDBStarting: {
			Remove: S{ssA.BaseDBReady},
		},
		ssA.BaseDBReady: {
			Remove: S{ssA.BaseDBStarting},
		},
		ssA.BaseDBSaving: {Multi: true},
		ssA.DBStarting: {
			Require: S{ssA.Start},
			Remove:  S{ssA.DBReady},
		},
		ssA.DBReady: {
			Require: S{ssA.Start},
			Remove:  S{ssA.DBStarting},
		},
		ssA.HistoryDBStarting: {
			Remove: S{ssA.HistoryDBReady},
		},
		ssA.HistoryDBReady: {
			Remove: S{ssA.HistoryDBStarting},
		},

		// EVENTS

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
		ssA.UIMsg: {
			Multi:   true,
			Require: S{ssA.Start},
		},

		// STORIES

		ssA.CheckStories: {
			Multi:   true,
			Require: S{ssA.Start},
		},
		ssA.StoryChanged: {
			Multi: true,
			After: S{ssA.CheckStories},
		},

		// UI

		ssA.UIMode:        {Require: S{ssA.Start}},
		ssA.UIReady:       {Require: S{ssA.UIMode}},
		ssA.UIButtonSend:  {Require: S{ssA.UIMode}},
		ssA.UIButtonInter: {Require: S{ssA.UIMode}},
		ssA.UISaveOutput:  {Require: S{ssA.UIMode}},
		ssA.UICleanOutput: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UIRenderStories: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UIRenderClock: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.UIUpdateClock: {
			Require: S{ssA.UIMode},
		},
		ssA.StoryAction: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.SSHConn: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.SSHDisconn: {
			Multi:   true,
			Require: S{ssA.UIMode},
		},
		ssA.SSHReady: {Require: S{ssA.UIMode}},
		ssA.WebSSHReady: {
			Auto:    true,
			Require: S{ssA.SSHReady},
		},

		// debug tools

		ssA.Debugger: {},
		ssA.REPL:     {},

		// piped-in

		ssA.WebHTTPReady: {Require: S{ssA.UIMode}},
		ssA.WebRPCReady:  {Require: S{ssA.UIMode}},
		ssA.WebConnected: {
			Multi:   true,
			Require: S{ssA.WebHTTPReady},
		},
		ssA.RemoteDashReady: {
			Multi:   true,
			Require: S{ssA.WebHTTPReady},
		},
		ssA.RemoteUIReady: {
			Multi:   true,
			Require: S{ssA.WebHTTPReady},
		},
	})

// EXPORTS AND GROUPS

// TagPrompt is for states with LLM prompts.
const TagPrompt = "prompt"

// TagManual is for stories that CANNOT be triggered by the LLM orienting story.
const TagManual = "manual"

// TagTrigger is for stories that can be triggered by the LLM orienting story.
const TagTrigger = "trigger"

var (
	ssA = am.NewStates(AgentBaseStatesDef{})
	sgA = am.NewStateGroups(AgentBaseGroupsDef{
		UI: am.S{ssA.UIMsg, ssA.UIReady, ssA.UIButtonSend, ssA.UIButtonInter, ssA.UISaveOutput, ssA.UICleanOutput,
			ssA.UIRenderStories, ssA.SSHConn, ssA.SSHDisconn, ssA.SSHReady, ssA.WebSSHReady, ssA.WebHTTPReady,
			ssA.WebRPCReady, ssA.WebConnected, ssA.RemoteDashReady, ssA.RemoteUIReady, ssA.UIRenderClock,
			ssA.UIUpdateClock},
	})

	// AgentBaseStates contains all the states for the Agent machine.
	AgentBaseStates = ssA
	// AgentBaseGroups contains all the state groups for the Agent machine.
	AgentBaseGroups = sgA
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
	// inherit from StateSourceStatesDef
	*ssrpc.StateSourceStatesDef
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
	ssrpc.StateSourceSchema,
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
// TODO move to shared

type Website struct {
	URL     string `description:"The URL of the website."`
	Title   string `description:"The title of the website."`
	Content string `description:"The content or a snippet of the website."`
}
