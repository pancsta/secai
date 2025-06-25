// Package schema contains a stateful schema-v2 for Mem.
// Bootstrapped with am-gen. Edit manually or re-gen & merge.
package schema

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
)

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
