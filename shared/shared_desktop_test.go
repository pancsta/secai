//go:build !wasm

package shared

import (
	"context"
	"reflect"
	"testing"

	"github.com/pancsta/instructor-go/pkg/instructor/core"
)

type dummyReq struct {
	Query string `json:"query"`
}

type dummyRes struct {
	Answer string `json:"answer"`
}

func TestPromptGodoc(t *testing.T) {
	p := NewPrompt[dummyReq, dummyRes](nil, "testState", "", "", "")

	if p.Godoc == nil {
		t.Fatal("expected Godoc map to be initialized")
	}

	doc1 := map[string]string{
		"pkg.Type.Field1": "desc1",
	}
	p.AddGodoc(doc1)
	if p.Godoc["pkg.Type.Field1"] != "desc1" {
		t.Errorf("expected desc1, got %q", p.Godoc["pkg.Type.Field1"])
	}

	doc2 := map[string]string{
		"pkg.Type.Field2": "desc2",
	}
	p.AddGodoc(doc2)
	if p.Godoc["pkg.Type.Field1"] != "desc1" || p.Godoc["pkg.Type.Field2"] != "desc2" {
		t.Errorf("expected merged map with both desc1 and desc2, got %v", p.Godoc)
	}

	doc3 := map[string]string{
		"pkg.Type.Field3": "desc3",
	}
	p.SetGodoc(doc3)
	if len(p.Godoc) != 1 || p.Godoc["pkg.Type.Field3"] != "desc3" {
		t.Errorf("expected replaced map with desc3, got %v", p.Godoc)
	}

	// Context propagation test
	ctx := context.Background()
	if len(p.Godoc) > 0 {
		ctx = core.ContextWithGodoc(ctx, p.Godoc)
	}
	retrieved := core.GodocFromContext(ctx)
	if !reflect.DeepEqual(retrieved, p.Godoc) {
		t.Errorf("expected %v from context, got %v", p.Godoc, retrieved)
	}
}
