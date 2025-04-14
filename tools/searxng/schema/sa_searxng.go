package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai/schema"
)

// ///// ///// /////

// ///// STATES

// ///// ///// /////

type StateName = string

// StatesDef contains all the states of the WebSearch state machine.
type StatesDef struct {
	*am.StatesBase

	DockerChecking  string
	DockerAvailable string
	DockerStarting  string

	*schema.ToolStatesDef
}

// GroupsDef contains all the state groups WebSearch state machine.
type GroupsDef struct {
}

// Schema represents all relations and properties of States.
var Schema = SchemaMerge(
	// inherit from Tool
	schema.ToolSchema,

	am.Schema{

		ss.Ready: {
			Require: S{ss.Start},
			Remove:  S{ss.DockerStarting},
		},

		ss.DockerChecking: {
			Require: S{ss.Start},
			Remove:  S{ss.DockerAvailable},
		},
		ss.DockerAvailable: {
			Require: S{ss.Start},
			Remove:  S{ss.DockerChecking},
		},
		ss.DockerStarting: {
			Auto:    true,
			Require: S{ss.DockerAvailable},
		},
	})

// EXPORTS AND GROUPS

var (
	ss = am.NewStates(StatesDef{})
	sg = am.NewStateGroups(GroupsDef{})

	// States contains all the states for the WebSearch state machine.
	States = ss
	// Groups contains all the state groups for the WebSearch state machine.
	Groups = sg
)

// ///// ///// /////

// ///// API

// ///// ///// /////

type Params struct {
	Queries []string `description:"List of search queries."`
	// TODO enum
	Category string `description:"Category of the search queries. Allowed values are 'general', 'news', 'social_media'."`
}

type Result struct {
	Query    string            `description:"The query used to obtain this search result."`
	Results  []*schema.Website `description:"List of search result items."`
	Category string            `description:"The category of the search results."`
}
