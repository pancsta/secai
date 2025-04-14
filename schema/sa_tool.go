// Package schema contains a stateful schema-v2 for Tool.
// Bootstrapped with am-gen. Edit manually or re-gen & merge.
package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
)

type ToolState = string

// ToolStatesDef contains all the states of the Tool state machine.
type ToolStatesDef struct {
	*am.StatesBase

	// STATUS

	Working ToolState
	Idle    ToolState

	// inherit from BasicStatesDef
	*ssam.BasicStatesDef
	// inherit from DisposedStatesDef
	*ssam.DisposedStatesDef
	// inherit from WorkerStatesDef
	*ssrpc.WorkerStatesDef
}

// ToolGroupsDef contains all the state groups Tool state machine.
type ToolGroupsDef struct {
}

// ToolSchema represents all relations and properties of ToolStates.
// TODO
var ToolSchema = SchemaMerge(
	// inherit from BasicStruct
	ssam.BasicStruct,
	// inherit from DisposedStruct
	ssam.DisposedStruct,
	// inherit from WorkerStates
	ssrpc.WorkerStruct,
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
