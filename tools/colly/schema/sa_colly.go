package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai/schema"
)

// ///// ///// /////

// ///// STATES

// ///// ///// /////

type StateName = string

// StatesDef contains all the states of the Colly state machine.
type StatesDef struct {
	*am.StatesBase

	// TODO colly states go here

	*schema.ToolStatesDef
}

// GroupsDef contains all the state groups Colly state machine.
type GroupsDef struct {
}

// Schema represents all relations and properties of States.
var Schema = SchemaMerge(
	// inherit from Tool
	schema.ToolSchema,

	am.Schema{

		// TODO colly states go here
	})

// EXPORTS AND GROUPS

var (
	ss = am.NewStates(StatesDef{})
	sg = am.NewStateGroups(GroupsDef{})

	// States contains all the states for the Colly state machine.
	States = ss
	// Groups contains all the state groups for the Colly state machine.
	Groups = sg
)

// ///// ///// /////

// ///// API

// ///// ///// /////

type Params = []ParamsWebsite
type ParamsWebsite struct {
	URL      string
	Selector string
}

func ParamsFromWebsites(websites []*schema.Website) Params {
	var params Params
	for _, w := range websites {
		params = append(params, ParamsWebsite{
			URL: w.URL,
		})
	}
	return params
}

type Result struct {
	Websites []*schema.Website
	Errors   []error
}
