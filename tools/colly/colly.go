package colly

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cixtor/readability"
	"github.com/gocolly/colly/v2"
	html2text "github.com/jaytaylor/html2text"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"golang.org/x/sync/errgroup"

	"github.com/pancsta/secai"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tools/colly/schema"
)

var ss = schema.States
var id = "colly"

// TODO refac header
var title = "Webpages as markdown\n\nContents is indented with 2 tab characters."

// TODO config
// TODO limit words, not chars
var Limit = 1300

type Tool struct {
	*secai.Tool
	*am.ExceptionHandler

	agent  secai.AgentApi
	result schema.Result
	client *http.Client
	c      *colly.Collector
}

func New(agent secai.AgentApi) (*Tool, error) {
	var err error
	t := &Tool{}
	t.Tool, err = secai.NewTool(agent, id, title, ss.Names(), schema.Schema)
	if err != nil {
		return nil, err
	}

	// bind handlers
	err = t.Mach().BindHandlers(t)
	if err != nil {
		return nil, err
	}

	t.agent = agent
	t.result = schema.Result{}

	return t, nil
}

func (t *Tool) Document() *secai.Document {
	doc := t.Doc.Clone()
	doc.Clear()
	if len(t.result.Websites) == 0 {
		return &doc
	}

	for i, r := range t.result.Websites {
		if r == nil {
			continue
		}

		doc.AddPart(shared.Sp(`
			### %d. %s

					%s
		
		
		`, i+1, r.Title, strings.ReplaceAll(r.Content, "\n", "\n\t\t")))
	}

	return &doc
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (t *Tool) StartState(e *am.Event) {
	// TODO start docker, go to Ready
	t.Mach().Add1(ss.Ready, nil)
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (t *Tool) Scrape(ctx context.Context, params schema.Params) (schema.Result, error) {
	mach := t.Mach()
	mach.Add1(ss.Working, nil)
	defer mach.Add1(ss.Idle, nil)

	// config TODO clone the scraper and init earlier, bind ctx each time
	cacheDir := filepath.Join(os.Getenv("SECAI_DIR"), "colly")
	cfg := []colly.CollectorOption{colly.StdlibContext(ctx)}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		mach.AddErr(fmt.Errorf("failed to create cache dir: %s %w",
			os.Getenv("SECAI_DIR"), err), nil)
	} else {
		cfg = append(cfg, colly.CacheDir(cacheDir))
	}

	// init
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(5)
	t.c = colly.NewCollector(cfg...)

	result := schema.Result{
		Websites: make([]*baseschema.Website, len(params)),
		Errors:   make([]error, len(params)),
	}

	// exec in a pool
	for i, site := range params {
		g.Go(func() error {
			item, err := t.scrapeUrl(site.URL, site.Selector)
			if err != nil {
				result.Errors[i] = err

				return nil
			}
			result.Websites[i] = item

			return nil
		})
	}
	_ = g.Wait()

	// memorize for prompts
	t.result = result

	return result, errors.Join(result.Errors...)
}

func (t *Tool) scrapeUrl(url string, cssSelector string) (*baseschema.Website, error) {
	c := t.c.Clone()
	var errs, err error

	ret := baseschema.Website{
		URL: url,
	}

	c.OnHTML("title", func(e *colly.HTMLElement) {
		ret.Title = e.Text
	})

	var out string
	// requested selector (markdown version)
	if cssSelector != "" {
		c.OnHTML(cssSelector, func(e *colly.HTMLElement) {
			out, err = e.DOM.Html()
			if err != nil {
				errs = errors.Join(errs, err)
				return
			}
		})

		// whole page (reader version)
	} else {
		c.OnHTML("html", func(e *colly.HTMLElement) {
			reader := readability.New()
			var err error

			// remove these from the DOM
			_ = e.DOM.Find("script, style, nav, header, footer").Remove()

			// find content
			el := e.DOM.Find("article, main, #content, #main, .content, .main")
			if el.Length() == 0 {
				el = e.DOM.Find("body")
				if el.Length() == 0 {
					errs = errors.Join(errs, fmt.Errorf("no content found"))
					return
				}
			}

			// get HTML and pass through the reader
			// TODO optionally run through an LLM (state prompt)
			html, err := el.Html()
			if err != nil {
				errs = errors.Join(errs, err)
				return
			}
			mini, err := reader.Parse(strings.NewReader(html), url)
			if err != nil {
				return
			}

			if strings.TrimSpace(mini.Content) != "" {
				out = mini.Content
			} else {
				out = html
			}
		})
	}

	err = c.Visit(url)
	if err != nil {
		errs = errors.Join(errs, err)
		return nil, err
	}

	t.Mach().Log("Converting %d long HTML to MD", len(out))
	txt, err := sanitize(out)
	if err != nil {
		errs = errors.Join(errs, err)
	}
	ret.Content = txt

	return &ret, errs
}

func sanitize(html string) (string, error) {
	// TODO domain, keep links
	t, err := html2text.FromString(html)
	if err != nil {
		return "", err
	}

	// sanitize
	t = strings.ReplaceAll(t, "```", " ")
	t = strings.TrimSpace(t)

	return t[:min(Limit, len(t))], nil
}
