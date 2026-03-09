package types

import (
	"encoding/gob"
	"encoding/json"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"
)

const RouteFoo = "/foo"

type DataDashboard struct {
	Metrics *DataMetrics
	Splash  string
}

type DataAgent struct {
	Msgs []*shared.Msg
	// Actions are a list of buttons to be displayed in the UI.
	Actions   []shared.ActionInfo
	Stories   []shared.StoryInfo
	ClockDiff [][]int
}

type DataMetrics struct {
	// active AI reqs
	ReqAIs int8
	// active tools reqs
	ReqTools int8
}

type DataBoostrap struct {
	Config     *shared.Config
	MachSchema am.Schema
	MachStates am.S
}

// ///// ///// /////

// ///// ARGS (BROWSER)

// ///// ///// /////

func init() {
	gob.Register(ARpc{})
	gob.Register(shared.A{})
}

const APrefix = "browser"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	Config *shared.Config
	// TODO log non empty fields with counters
	DataDash *DataDashboard
	// TODO log non empty fields with counters
	DataAgent   *DataAgent
	MachTimeSum uint64

	// non-RPC fields

	// ...
}

// ARpc is a subset of [A] that can be passed over RPC.
type ARpc struct {
	Config *shared.Config
	// TODO log non empty fields with counters
	DataDash *DataDashboard
	// TODO log non empty fields with counters
	DataAgent   *DataAgent
	MachTimeSum uint64
}

// ParseArgs extracts A from [am.Event.Args][APrefix].
func ParseArgs(args am.A) *A {
	if r, ok := args[APrefix].(*ARpc); ok {
		return amhelp.ArgsToArgs(r, &A{})
	} else if r, ok := args[APrefix].(ARpc); ok {
		return amhelp.ArgsToArgs(&r, &A{})
	}
	if a, _ := args[APrefix].(*A); a != nil {
		return a
	}
	return &A{}
}

// Pass prepares [am.A] from A to pass to further mutations.
func Pass(args *A) am.A {
	return am.A{APrefix: args}
}

// PassRpc prepares [am.A] from A to pass over RPC.
func PassRpc(args *A) am.A {
	return am.A{APrefix: amhelp.ArgsToArgs(args, &ARpc{})}
}

// LogArgs is an args logger for A.
func LogArgs(args am.A) map[string]string {
	a := ParseArgs(args)
	if a == nil {
		return nil
	}

	return amhelp.ArgsToLogMap(a, 0)
}

// ParseRpc parses am.A to *ARpc wrapped in am.A. Useful for REPLs.
func ParseRpc(args am.A) am.A {
	ret := am.A{APrefix: &ARpc{}}
	jsonArgs, err := json.Marshal(args)
	if err == nil {
		json.Unmarshal(jsonArgs, ret[APrefix])
	}

	return ret
}
