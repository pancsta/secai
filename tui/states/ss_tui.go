package states

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ss "github.com/pancsta/asyncmachine-go/pkg/states"
)

// ///// ///// /////

// ///// BASE UI

// ///// ///// /////

// UIBaseStatesDef contains all the states of the UIBase state-machine.
type UIBaseStatesDef struct {
	*am.StatesBase

	// chat states

	// inherit from BasicStatesDef
	*ss.BasicStatesDef
	// inherit from DisposedStatesDef
	*ss.DisposedStatesDef
	// inherit from WorkerStatesDef
	*ssrpc.WorkerStatesDef
}

// UIBaseGroupsDef contains all the state groups UIBase state-machine.
type UIBaseGroupsDef struct {
}

// UIBaseSchema represents all relations and properties of UIBaseStates.
var UIBaseSchema = SchemaMerge(
	// inherit from BasicStruct
	ss.BasicSchema,
	// inherit from DisposedStruct
	ss.DisposedSchema,
	// inherit from WorkerStates
	ssrpc.WorkerSchema,
	am.Schema{
		// chat states
	})

// EXPORTS AND GROUPS

var (
	ssB = am.NewStates(UIBaseStatesDef{})
	sgB = am.NewStateGroups(UIBaseGroupsDef{})

	// UIBaseStates contains all the states for the UIBase state-machine.
	UIBaseStates = ssB
	// UIBaseGroups contains all the state groups for the UIBase state-machine.
	UIBaseGroups = sgB
)

// ///// ///// /////

// ///// CHAT

// ///// ///// /////

// UIChatStatesDef contains all the states of the UIChat state-machine.
type UIChatStatesDef struct {
	*am.StatesBase

	// chat states

	// inherit from BasicStatesDef
	*UIBaseStatesDef
}

// UIChatGroupsDef contains all the state groups UIChat state-machine.
type UIChatGroupsDef struct {
}

// UIChatSchema represents all relations and properties of UIChatStates.
var UIChatSchema = SchemaMerge(
	// inherit from UIBase
	UIBaseSchema,
	am.Schema{
		// chat states
	})

// EXPORTS AND GROUPS

var (
	ssC = am.NewStates(UIChatStatesDef{})
	sgC = am.NewStateGroups(UIChatGroupsDef{})

	// UIChatStates contains all the states for the UIChat state-machine.
	UIChatStates = ssC
	// UIChatGroups contains all the state groups for the UIChat state-machine.
	UIChatGroups = sgC
)

// NewUIChat creates a new UIChat state-machine in the most basic form.
func NewUIChat(ctx context.Context) *am.Machine {
	return am.New(ctx, UIChatSchema, nil)
}

// ///// ///// /////

// ///// STORIES

// ///// ///// /////

// UIStoriesStatesDef contains all the states of the UIStories state-machine.
type UIStoriesStatesDef struct {
	*am.StatesBase

	ReqReplaceContent string
	ReplaceContent    string

	// inherit from UIBaseStatesDef
	*UIBaseStatesDef
}

// UIStoriesGroupsDef contains all the state groups UIStories state-machine.
type UIStoriesGroupsDef struct {
}

// UIStoriesSchema represents all relations and properties of UIStoriesStates.
var UIStoriesSchema = SchemaMerge(
	// inherit from UIBase
	UIBaseSchema,
	am.Schema{

		ssU.ReqReplaceContent: {Multi: true},
		ssU.ReplaceContent:    {},
	})

// EXPORTS AND GROUPS

var (
	ssU = am.NewStates(UIStoriesStatesDef{})
	sgU = am.NewStateGroups(UIStoriesGroupsDef{})

	// UIStoriesStates contains all the states for the UIStories state-machine.
	UIStoriesStates = ssU
	// UIStoriesGroups contains all the state groups for the UIStories state-machine.
	UIStoriesGroups = sgU
)

// NewUIStories creates a new UIStories state-machine in the most basic form.
func NewUIStories(ctx context.Context) *am.Machine {
	return am.New(ctx, UIStoriesSchema, nil)
}

// ///// ///// /////

// ///// CLOCK

// ///// ///// /////

// UIClockStatesDef contains all the states of the UIClock state-machine.
type UIClockStatesDef struct {
	*am.StatesBase

	ReqReplaceButtons string
	ReplaceButtons    string

	// inherit from UIBaseStatesDef
	*UIBaseStatesDef
}

// UIClockGroupsDef contains all the state groups UIClock state-machine.
type UIClockGroupsDef struct {
}

// UIClockSchema represents all relations and properties of UIClockStates.
var UIClockSchema = SchemaMerge(
	// inherit from UIBase
	UIBaseSchema,
	am.Schema{

		ssL.ReqReplaceButtons: {Multi: true},
		ssL.ReplaceButtons:    {},
	})

// EXPORTS AND GROUPS

var (
	ssL = am.NewStates(UIClockStatesDef{})
	sgL = am.NewStateGroups(UIClockGroupsDef{})

	// UIClockStates contains all the states for the UIClock state-machine.
	UIClockStates = ssL
	// UIClockGroups contains all the state groups for the UIClock state-machine.
	UIClockGroups = sgL
)

// NewUIClock creates a new UIClock state-machine in the most basic form.
func NewUIClock(ctx context.Context) *am.Machine {
	return am.New(ctx, UIClockSchema, nil)
}
