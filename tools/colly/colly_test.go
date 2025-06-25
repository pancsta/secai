package colly

import (
	"context"
	"testing"

	"github.com/gocolly/colly/v2"
	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/research/schema"
)

func TestReader(t *testing.T) {
	ctx := context.Background()
	cfg := []colly.CollectorOption{colly.StdlibContext(ctx)}
	ct := &Tool{Tool: &secai.Tool{}}
	ct.SetMach(schema.NewResearch(ctx))
	ct.c = colly.NewCollector(cfg...)

	_, err := ct.scrapeUrl("https://blog.google/technology/ai/google-ai-updates-february-2025/", "")
	if err != nil {
		t.Fatal(err)
	}

	t.Log(ct.result)
}
