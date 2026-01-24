package getter

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/states"
)

var ss = states.ToolStates
var idPrefix = "getter-"

type Tool struct {
	*secai.Tool
	*am.ExceptionHandler

	getter func() (string, error)
}

func New(agent shared.AgentBaseAPI, id, title string, getter func() (string, error)) (*Tool, error) {
	var err error
	t := &Tool{
		getter: getter,
	}
	t.Tool, err = secai.NewTool(agent, idPrefix+id, title, ss.Names(), states.ToolSchema)
	if err != nil {
		return nil, err
	}

	// bind handlers
	err = t.Mach().BindHandlers(t)
	if err != nil {
		return nil, err
	}

	return t, nil
}

func (t *Tool) Document() *secai.Document {
	doc := t.Doc.Clone()
	doc.Clear()

	val, err := t.getter()
	if err == nil {
		doc.AddPart(val)
	}

	return &doc
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (t *Tool) StartState(e *am.Event) {
	t.Mach().Add1(ss.Ready, nil)
}

func (t *Tool) WorkingEnter(e *am.Event) bool {
	// check params

	return true
}

func (t *Tool) WorkingState(e *am.Event) {
	// make the request, go to Idle
}
