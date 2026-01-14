package searxng

import (
	"context"
	"embed"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"
	"golang.org/x/sync/errgroup"

	"github.com/pancsta/secai"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/tools/searxng/schema"
)

var ss = schema.States
var id = "searxng"
var title = "Web Search Results"

type Tool struct {
	*secai.Tool
	*am.ExceptionHandler

	queries []string
	result  *schema.Result
	cfg     shared.ConfigSearXNG
}

//go:embed config
var cfgFolder embed.FS

func New(agent secai.AgentAPI) (*Tool, error) {
	var err error
	t := &Tool{
		cfg: agent.ConfigBase().Tools.SearXNG,
	}
	t.Tool, err = secai.NewTool(agent, id, title, ss.Names(), schema.Schema)
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
	if t.result == nil || len(t.result.Results) == 0 {
		return &doc
	}

	doc.AddPart("QueriesBase: " + strings.Join(t.queries, "; "))
	// TODO config
	for _, r := range t.result.Results[:min(30, len(t.result.Results))] {
		doc.AddPart("- " + r.Title)
	}

	return &doc
}

// Search is a blocking method that performs the search.
func (t *Tool) Search(ctx context.Context, params *schema.Params) (*schema.Result, error) {
	mach := t.Mach()
	mach.Add1(ss.Working, nil)
	defer mach.Add1(ss.Idle, nil)

	g, _ := errgroup.WithContext(ctx)
	// TODO config
	g.SetLimit(5)
	// TODO config
	http.DefaultClient.Timeout = 30 * time.Second
	t.queries = params.Queries

	resPerQuery := make([][]*baseschema.Website, len(params.Queries))

	// build params
	qp := map[string]string{
		"safesearch": "0",
		"format":     "json",
		"language":   "en",
		"engines":    "bing,duckduckgo,google,startpage,yandex",
	}
	if params.Category != "" {
		qp["categories"] = "general"
	}

	// exec in a pool
	for i, q := range params.Queries {

		// complete params for this search
		qp["q"] = q
		sp := url.Values{}
		for k, v := range qp {
			sp.Add(k, v)
		}
		enc := sp.Encode()

		g.Go(func() error {
			u := t.cfg.URL
			if u == "" {
				u = "http://localhost:" + t.cfg.Port
			}
			u += "/search?" + enc
			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return nil
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				mach.AddErr(err, nil)
				return nil
			}
			defer resp.Body.Close()

			var result schema.Result
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				mach.AddErr(err, nil)
				return nil
			}
			resPerQuery[i] = append(resPerQuery[i], result.Results...)

			return nil
		})
	}
	// wait
	_ = g.Wait()

	// merge results fairly (siblings by position) and remove dups
	var merged []*baseschema.Website
	var max int
	for i := range params.Queries {
		if max < len(resPerQuery[i]) {
			max = len(resPerQuery[i])
		}
	}
	for i := range params.Queries {
		for ii := range max {
			if len(resPerQuery[i]) < ii+1 {
				continue
			}
			if slices.Contains(merged, resPerQuery[i][ii]) {
				continue
			}
			merged = append(merged, resPerQuery[i][ii])
		}
	}

	t.result = &schema.Result{
		Results:  merged,
		Category: params.Category,
	}

	return t.result, nil
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (t *Tool) StartState(e *am.Event) {
	mach := t.Mach()

	// existing instance
	if t.cfg.URL != "" {
		mach.EvAdd1(e, ss.Ready, nil)
		return
	}

	// new instance
	mach.Log("Tools.SearXNG.URL empty - starting a local docker instance")
	mach.EvAdd1(e, ss.DockerChecking, nil)
}

func (t *Tool) DockerCheckingState(e *am.Event) {
	mach := t.Mach()
	ctx := mach.NewStateCtx(ss.DockerChecking)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		cmd := exec.CommandContext(ctx, "which", "docker")
		output, err := cmd.Output()
		if err != nil {
			mach.AddErr(err, nil)
			return
		}
		if len(output) == 0 {
			mach.EvAddErr(e, errors.New("docker not available"), nil)
		} else {
			mach.EvAdd1(e, ss.DockerAvailable, nil)
		}
	}()
}

func (t *Tool) DockerStartingState(e *am.Event) {
	mach := t.Mach()
	ctx := mach.NewStateCtx(ss.DockerStarting)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// create tmp dir TODO custom data dir
		tmpDir := filepath.Join(os.TempDir(), "secai-tool-searxng")

		// deploy configs, if missing
		if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
			if err := os.RemoveAll(tmpDir); err != nil {
				mach.AddErr(fmt.Errorf("failed to remove tmp dir: %w", err), nil)
				return
			}
		}

		if err := os.CopyFS(tmpDir, cfgFolder); err != nil {
			mach.AddErr(fmt.Errorf("failed to copy docker files: %w", err), nil)
			return
		}

		// start TODO correct mount perms UID=$(id -u) GID=$(id -g)
		cmd := exec.Command("docker", "compose", "-p", "secai-tool-searxng", "up", "-d")
		cmd.Dir = filepath.Join(tmpDir, "config")
		out, err := cmd.CombinedOutput()
		if err != nil {
			mach.EvAddErr(e, fmt.Errorf("docker compose failed: %w: %s", err, out), nil)
			return
		}
		if ctx.Err() != nil {
			return // expired
		}

		// next
		mach.EvAdd1(e, ss.Ready, nil)
	}()
}
