package states

// TODO remove UI prefix

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ss "github.com/pancsta/asyncmachine-go/pkg/states"
)

// ///// ///// /////

// ///// TUI

// ///// ///// /////

// TUIStatesDef contains all the states of the TUI state-machine.
type TUIStatesDef struct {
	*am.StatesBase

	// requests replacement of stories' info and their actions
	ReqReplaceStories string
	// throttled UI update, not to be called directly
	ReplaceStories string

	// inherit from BasicStatesDef
	*ss.BasicStatesDef
	// inherit from DisposedStatesDef
	*ss.DisposedStatesDef
	// inherit from NetSourceStatesDef
	*ssrpc.NetSourceStatesDef
}

// TUIGroupsDef contains all the state groups TUI state-machine.
type TUIGroupsDef struct {
}

// TUISchema represents all relations and properties of TUIStates.
var TUISchema = SchemaMerge(
	// inherit from BasicStruct
	ss.BasicSchema,
	// inherit from DisposedStruct
	ss.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.NetSourceSchema,
	am.Schema{

		ssT.ReqReplaceStories: {Multi: true},
		ssT.ReplaceStories:    {},
	})

// EXPORTS AND GROUPS

var (
	ssT = am.NewStates(TUIStatesDef{})
	sgT = am.NewStateGroups(TUIGroupsDef{})

	// TUIStates contains all the states for the TUI state-machine.
	TUIStates = ssT
	// TUIGroups contains all the state groups for the TUI state-machine.
	TUIGroups = sgT
)
