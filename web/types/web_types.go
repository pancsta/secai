package types

import (
	"encoding/gob"

	"github.com/orsinium-labs/enum"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/web/browser/states"
)

// ID enum
type ID enum.Member[string]

var (
	IDDashboardPage  = ID{"dash"}
	IDDashboardAgent = ID{IDDashboardPage.Value + "-agent"}
	IDAgentUIPage    = ID{"agentui"}
	IDAgentUIAgent   = ID{IDAgentUIPage.Value + "-agent"}

	MachIDEnum = enum.New(IDAgentUIPage, IDDashboardPage, IDDashboardAgent, IDAgentUIAgent)
)

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

// GenBrowserID returns an ID for a browser-side state machine.
func GenBrowserID(typeID ID, agentID string, suffix string) string {
	id := "bro-" + typeID.Value
	if agentID != "" {
		id += "-" + agentID
	}
	if suffix != "" {
		id += "-" + suffix
	}

	return id
}

// GenServerID returns an ID for a server-side state machine.
func GenServerID(typeID ID, agentID string, suffix string) string {
	id := "srv-" + typeID.Value
	if agentID != "" {
		id += "-" + agentID
	}
	if suffix != "" {
		id += "-" + suffix
	}

	return id
}

// ///// ///// /////

// ///// ARGS (BROWSER)

// ///// ///// /////

const APrefix = "browser"

type Args struct {
	am.ArgsBase
}

func (Args) ArgsPrefix() string {
	return APrefix
}

// -----

type AConfig struct {
	Args

	Config *shared.Config
}

func (AConfig) ArgsState() string {
	return states.PageStates.Config
}

// -----

type AData struct {
	Args

	DataDash *DataDashboard
	// TODO log non empty fields with counters
	DataAgent   *DataAgent
	MachTimeSum uint64
}

func (AData) ArgsState() string {
	return states.PageStates.Data
}

// ----- RPC boilerplate

func init() {
	for _, arg := range ArgsRPC {
		gob.Register(arg)
	}
}

var ArgsRPC = []am.ArgsApi{AConfig{}, AData{}}
