// Package browser contains the server-rendered (livepage) web UIs of secai,
// built with go-app components. The components are bound to the local agent
// machine, just like the TUI, and are served by the local web server.
package browser

import (
	"context"
	_ "embed"
	"html"
	"net/http"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	. "github.com/pancsta/go-app/pkg/app"
	lp "github.com/pancsta/livepage"

	"github.com/pancsta/secai/shared"
	ssbase "github.com/pancsta/secai/states"
)

//go:embed browser.js
var browserJS string

var ssA = ssbase.AgentBaseStates

var AppClass = "p-4"

// ///// ///// /////

// ///// PAGES

// ///// ///// /////

const (
	// DefaultDashPrefix is the default URL prefix of the dashboard.
	DefaultDashPrefix = ""
	// DefaultAgentUIPrefix is the default URL prefix of the agent UI.
	DefaultAgentUIPrefix = "/agent"
	// defaultDebounce is the default delay of pushing HTML to the clients.
	defaultDebounce = 100 * time.Millisecond
)

// Model is the local agent model rendered by the web UIs. It's implemented by
// [shared.AgentAPI].
type Model interface {
	Mach() *am.Machine
	ConfigBase() *shared.Config
	Store() *shared.AgentStore
	Msgs() []*shared.Msg
	Stories() []shared.StoryInfo
	Actions() []shared.ActionInfo
	Splash() string
}

// Opts are the options of [NewPages].
type Opts struct {
	// DashPrefix is the URL prefix of the dashboard. Defaults to the root.
	DashPrefix string
	// AgentUIPrefix is the URL prefix of the agent UI. Defaults to
	// [DefaultAgentUIPrefix].
	AgentUIPrefix string
	// Debounce is the delay of pushing HTML to the clients.
	Debounce time.Duration
}

// PageRef is a livepage page of a component.
type PageRef interface {
	HRefresh()
	Clients() int
}

// PageSetter is implemented by extendable page components.
type PageSetter interface {
	SetPage(PageRef)
}

// RefreshPage re-renders and pushes a page, if it has clients.
func RefreshPage(page PageRef) {
	if page == nil || page.Clients() == 0 {
		return
	}

	page.HRefresh()
}

// Pages are the server-rendered web UIs of an agent. They read the model
// directly, are bound to its machine, and are served by the local web server.
type Pages[D lp.CompoLiveApi, A lp.CompoLiveApi] struct {
	Dashboard *lp.Page[D]
	AgentUI   *lp.Page[A]

	mach          *am.Machine
	dashBind      string
	agentBind     string
	dashPrefix    string
	agentUIPrefix string
}

// NewPages creates the web UIs for the passed model. The UIs read the model
// directly and refresh on transitions of its machine.
func NewPages(ctx context.Context, model Model, opts Opts) (*Pages[*Dashboard, *AgentUI], error) {
	dash := &Dashboard{
		BasePage:    NewBasePage(model),
		FormBackend: "openai",
	}
	agentUI := &AgentUI{BasePage: NewBasePage(model)}

	return NewPagesCustom(ctx, model, dash, agentUI, opts)
}

// NewPagesCustom creates the web UIs for custom components. The components
// should embed [Dashboard] and [AgentUI] (or at least implement [PageSetter]),
// and can override their Render methods.
func NewPagesCustom[D lp.CompoLiveApi, A lp.CompoLiveApi](
	ctx context.Context, model Model, dash D, agentUI A, opts Opts,
) (*Pages[D, A], error) {
	if opts.DashPrefix == "/" {
		opts.DashPrefix = DefaultDashPrefix
	}
	if opts.AgentUIPrefix == "" {
		opts.AgentUIPrefix = DefaultAgentUIPrefix
	}
	if opts.Debounce <= 0 {
		opts.Debounce = defaultDebounce
	}
	pageOpts := func(prefix, bodyClass string) lp.Opts {
		return lp.Opts{
			Prefix:   prefix,
			Debounce: opts.Debounce,
			DiffHtml: true,
			HtmlPage: htmlPage(model, bodyClass),
		}
	}

	// dashboard
	dashPage, err := lp.New(ctx, dash, pageOpts(opts.DashPrefix, AppClass))
	if err != nil {
		return nil, err
	}
	setPage(dash, dashPage)
	dashBind, err := model.Mach().HandlersBind(dash, am.BindOpts{Id: "browser.dash"})
	if err != nil {
		dashPage.Dispose()
		return nil, err
	}

	// agent UI
	agentPage, err := lp.New(ctx, agentUI,
		pageOpts(opts.AgentUIPrefix, "h-screen w-screen overflow-hidden "+AppClass))
	if err != nil {
		_ = model.Mach().HandlersDetach(dashBind)
		dashPage.Dispose()
		return nil, err
	}
	setPage(agentUI, agentPage)
	agentBind, err := model.Mach().HandlersBind(agentUI, am.BindOpts{Id: "browser.agentui"})
	if err != nil {
		agentPage.Dispose()
		_ = model.Mach().HandlersDetach(dashBind)
		dashPage.Dispose()
		return nil, err
	}

	return &Pages[D, A]{
		Dashboard:     dashPage,
		AgentUI:       agentPage,
		mach:          model.Mach(),
		dashBind:      dashBind,
		agentBind:     agentBind,
		dashPrefix:    opts.DashPrefix,
		agentUIPrefix: opts.AgentUIPrefix,
	}, nil
}

// setPage sets the livepage page on a [PageSetter] component.
func setPage(compo lp.CompoLiveApi, page PageRef) {
	if s, ok := compo.(PageSetter); ok {
		s.SetPage(page)
	}
}

// Mount mounts the pages on the passed local mux.
func (p *Pages[D, A]) Mount(mux *http.ServeMux) {
	mux.Handle(p.dashPrefix+"/", p.Dashboard.NewHandler())
	mux.Handle(p.agentUIPrefix+"/", p.AgentUI.NewHandler())
}

// Dispose stops the pages and detaches their machine handlers.
func (p *Pages[D, A]) Dispose() {
	if p.Dashboard != nil {
		p.Dashboard.Dispose()
	}
	if p.AgentUI != nil {
		p.AgentUI.Dispose()
	}
	_ = p.mach.HandlersDetach(p.dashBind)
	_ = p.mach.HandlersDetach(p.agentBind)
}

// valueTo captures the value forwarded by a livepage DOM event.
func valueTo(field *string) EventHandler {
	return func(_ Context, e Event) {
		*field = e.Data["value"]
	}
}

// toRef dumps a reference to a DOM node for further processing in a defer. It
// keeps the DOM chain in one piece.
func toRef[T UI](target *T, elem T) T {
	return lp.ToRef(target, elem)
}

// htmlPage wraps the rendered component into a whole HTML document and loads
// the shared styles, chart, and livepage hooks.
func htmlPage(model Model, bodyClass string) func(body string) string {
	return func(body string) string {
		cfg := model.ConfigBase()
		title := html.EscapeString(cfg.Agent.Label)
		pre := "/web/assets/deps/"

		return `<!DOCTYPE html>
<html lang="en" data-theme="business" class="bg-base-300">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + title + `</title>
<style>
html, body { height: 100%; margin: 0; }
*, *::before, *::after { box-sizing: border-box; }
</style>
<link rel="stylesheet" href="` + pre + `daisyui@5.css">
<link rel="stylesheet" href="` + pre + `themes.css">
<script src="` + pre + `tailwind@4.js"></script>
<script src="` + pre + `chart.js"></script>
<script src="` + pre + `utils.js"></script>
</head>
<body class="bg-base-300 ` + bodyClass + `">` + body + `
<script>` + browserJS + `</script>
<script>` + livepageJS + `</script>
</body>
</html>`
	}
}

// livepageJS bridges the livepage DOM morphs with the browser-only JS helpers
// in browser.js (chart, scroll, and form field drafts).
const livepageJS = `
(function () {
  function msgs() { return document.getElementById('msgs') }

  var stick = true
  document.addEventListener('scroll', function (e) {
    var m = msgs()
    if (m && e.target === m) {
      stick = m.scrollHeight - m.scrollTop - m.clientHeight < 40
    }
  }, true)

  // the clock chart is updated from the server-rendered JSON
  function clock() {
    var canvas = document.getElementById('clockmoji')
    if (!canvas) { return }
    if (!window.clockmojiChart || window.clockmojiChart.canvas !== canvas) {
      if (window.clockmojiChart) { window.clockmojiChart.destroy() }
      window.clockmojiChart = null
      clockmojiInit()
    }
    var diff
    try {
      diff = JSON.parse(canvas.getAttribute('data-diff'))
    } catch (e) {
      return
    }
    if (diff && diff.length) { clockmojiUpdate(diff) }
  }

  // form values live in the DOM only, so morphs reset and drafts restore them
  function isField(el) {
    if (!el) { return false }
    if (el.tagName === 'TEXTAREA') { return true }
    if (el.tagName !== 'INPUT') { return false }
    var t = (el.getAttribute('type') || 'text').toLowerCase()
    return t === 'text' || t === 'password' || t === 'url' ||
      t === 'email' || t === 'search' || t === 'tel' || t === 'number'
  }

  function fields() {
    // server-side resets
    var els = document.querySelectorAll('[data-reset]')
    for (var i = 0; i < els.length; i++) {
      var resetEl = els[i]
      var gen = resetEl.getAttribute('data-reset')
      if (resetEl.__lpReset !== gen) {
        resetEl.__lpReset = gen
        if (gen !== '0') {
          resetEl.value = ''
          resetEl.__lpValue = ''
        }
      }
    }

    // unsent drafts
    els = document.querySelectorAll('input, textarea')
    for (var j = 0; j < els.length; j++) {
      var el = els[j]
      if (!isField(el)) { continue }
      if (el.__lpValue !== undefined && el.value !== el.__lpValue) {
        el.value = el.__lpValue
      }
    }
  }

  function draft(e) {
    var t = e && e.target
    if (isField(t)) { t.__lpValue = t.value }
  }

  document.addEventListener('input', draft, true)
  document.addEventListener('change', draft, true)

  window.addEventListener('livepage:morph', function () {
    var m = msgs()
    if (m && stick) { m.scrollTop = m.scrollHeight }
    fields()
    clock()
  })

  // keep the tailwind runtime styles, which the document morph removes
  new MutationObserver(function (mutations) {
    mutations.forEach(function (mutation) {
      mutation.removedNodes.forEach(function (node) {
        if (node.nodeType === 1 && node.tagName === 'STYLE' &&
          node.textContent.indexOf('--tw-') !== -1) {
          document.head.appendChild(node)
        }
      })
    })
  }).observe(document.head, {childList: true})

  fields()
  clock()
})()
`

// ///// ///// /////

// ///// PAGE BASE

// ///// ///// /////

// BasePage is the base for the server-rendered web UIs. It binds the go-app
// component to the local agent machine.
type BasePage struct {
	lp.Compo

	model Model
}

// NewBasePage creates a base page bound to the model. It's meant for custom
// components embedding [Dashboard] or [AgentUI].
func NewBasePage(model Model) BasePage {
	return BasePage{model: model}
}

// Mach returns the local agent machine.
func (p *BasePage) Mach() *am.Machine {
	return p.model.Mach()
}

// Ready returns true when the agent machine has started.
func (p *BasePage) Ready() bool {
	return p.model.Mach().Is1(ssA.Start)
}

// Spinner returns a loading indicator.
func (p *BasePage) Spinner() UI {
	return Progress().Class("progress w-56")
}

// ///// ///// /////

// ///// DASHBOARD

// ///// ///// /////

// Dashboard is the web dashboard, bound to the local agent machine.
type Dashboard struct {
	BasePage

	// Dashboard state
	FormBackend string
	FormKey     string
	FormURL     string
	FormModel   string

	// Page is the livepage page of this component, set by [NewPagesCustom].
	Page PageRef
}

var _ lp.CompoLiveApi = &Dashboard{}
var _ PageSetter = &Dashboard{}

// SetPage sets the livepage page of this component.
func (d *Dashboard) SetPage(page PageRef) {
	d.Page = page
}

var _ = am.StateAny

// AnyState re-renders the page on transitions of the local machine.
func (d *Dashboard) AnyState(e *am.Event) {
	// ignore health-only txs
	called := e.Transition().TimeIndexCalled()
	if called.Any1(ssA.Healthcheck, ssA.Heartbeat) {
		return
	}

	RefreshPage(d.Page)
}

// ///// ///// /////

// ///// AGENT UI

// ///// ///// /////

// AgentUI is the web agent UI, bound to the local agent machine.
type AgentUI struct {
	BasePage

	// Agent UI state
	formPrompt    string
	promptReset   int
	buttonClicked string

	// Page is the livepage page of this component, set by [NewPagesCustom].
	Page PageRef
}

var _ lp.CompoLiveApi = &AgentUI{}
var _ PageSetter = &AgentUI{}

// SetPage sets the livepage page of this component.
func (a *AgentUI) SetPage(page PageRef) {
	a.Page = page
}

var _ = am.StateAny

// AnyState re-renders the page on transitions of the local machine.
func (a *AgentUI) AnyState(e *am.Event) {
	// ignore health-only txs
	called := e.Transition().TimeIndexCalled()
	if called.Any1(ssA.Healthcheck, ssA.Heartbeat) {
		return
	}

	RefreshPage(a.Page)
}
