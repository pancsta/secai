package states

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
)

// ///// ///// /////

// ///// PAGE

// ///// ///// /////

// PageStatesDef contains all the states of the Page state-machine.
type PageStatesDef struct {
	*am.StatesBase

	Data          string
	Config        string
	RPCConnected  string
	RPCConnecting string

	// inherit from BasicStatesDef
	*ssam.BasicStatesDef
	// inherit from rpc/StateSourceStatesDef
	*ssrpc.StateSourceStatesDef
}

// PageGroupsDef contains all the state groups Page state-machine.
type PageGroupsDef struct {
}

// PageSchema represents all relations and properties of PageStates.
var PageSchema = SchemaMerge(
	// inherit from BasicSchema
	ssam.BasicSchema,
	// inherit from rpc/NetSourceSchema
	ssrpc.StateSourceSchema,
	am.Schema{

		ssP.Data:   {Multi: true},
		ssP.Config: {},
		ssP.Ready: {
			Auto:    true,
			Require: S{ssP.RPCConnected, ssP.Config, ssP.Data},
		},

		// piped

		ssP.RPCConnected:  {},
		ssP.RPCConnecting: {},
	})

// EXPORTS AND GROUPS

var (
	ssP = am.NewStates(PageStatesDef{})
	sgP = am.NewStateGroups(PageGroupsDef{})

	// PageStates contains all the states for the Page state-machine.
	PageStates = ssP
	// PageGroups contains all the state groups for the Page state-machine.
	PageGroups = sgP
)

// NewPage creates a new Page state-machine in the most basic form.
func NewPage(ctx context.Context) *am.Machine {
	return am.New(ctx, PageSchema, nil)
}

// ///// ///// /////

// ///// AGENT UI

// ///// ///// /////

// AgentUIStatesDef contains all the states of the Page state-machine.
type AgentUIStatesDef struct {
	*am.StatesBase

	// piped=in from agent base TODO extract mixin

	UIRenderStories string
	UIMsg           string
	UIRenderClock   string
	UICleanOutput   string

	// inherit from PageStatesDef
	*PageStatesDef
}

// AgentUIGroupsDef contains all the state groups AgentUI state-machine.
type AgentUIGroupsDef struct {
}

// AgentUISchema represents all relations and properties of AgentUIStates.
var AgentUISchema = SchemaMerge(
	// inherit from PageSchema
	PageSchema,
	am.Schema{

		// agent UI

		ssA.UIRenderStories: {
			Multi:   true,
			Require: S{ssA.RPCConnected},
		},
		ssA.UIMsg: {
			Multi:   true,
			Require: S{ssA.RPCConnected},
		},
		ssA.UIRenderClock: {
			Multi:   true,
			Require: S{ssA.RPCConnected},
		},
		ssA.UICleanOutput: {
			Multi:   true,
			Require: S{ssA.RPCConnected},
		},

		// piped

		ssA.RPCConnected:  {},
		ssA.RPCConnecting: {},
	})

// EXPORTS AND GROUPS

var (
	ssA = am.NewStates(AgentUIStatesDef{})
	sgA = am.NewStateGroups(AgentUIGroupsDef{})

	// AgentUIStates contains all the states for the AgentUI state-machine.
	AgentUIStates = ssA
	// AgentUIGroups contains all the state groups for the AgentUI state-machine.
	AgentUIGroups = sgA
)

// NewAgentUI creates a new AgentUI state-machine in the most basic form.
func NewAgentUI(ctx context.Context) *am.Machine {
	return am.New(ctx, AgentUISchema, nil)
}
