package browser

import (
	"fmt"
	"regexp"
	"strings"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
)

var httpToAnchorRe = regexp.MustCompile(`https?://\S+`)
var addAnchorClassRe = regexp.MustCompile(`<a`)

var AnchorUrls = true

// backends are the AI backend options of the config form.
var backends = []struct{ label, value string }{
	{"OpenAI", "openai"},
	{"DeepSeek", "deepseek"},
	{"Gemini", "gemini"},
	{"OpenAI compatible", "openai-compat"},
}

func (d *Dashboard) Render() UI {
	return If(!d.Ready(), func() UI {
		return d.Spinner()
	}).Else(d.renderPage)
}

func (d *Dashboard) renderPage() UI {
	return Div().Body(
		d.Header(),
		d.ConfigForm(),
		d.Splash(),
		d.Metrics(),
		d.Footer(),
	)
}

func (d *Dashboard) Header() UI {
	cfg := d.model.ConfigBase()
	class, label := d.configBadge()

	var errBadge HTMLDiv
	defer func() {
		if d.model.Mach().IsErr() {
			errBadge.Class("badge mr-1 badge-error").Text("Exception")
		}
	}()

	return Div().Body(
		H2().Class("card-title text-warning mb-5").Text(cfg.Agent.Label),
		Div().Class("card card-border bg-base-100 mb-5").Body(
			Div().Class("card-body").Body(
				Raw(fixHTMLAnchors("<div>"+cfg.Agent.IntroDash+"</div>")),
			),
			Div().Class("text-right pb-3 pr-3").Body(
				toRef(&errBadge, Div()),
				Div().Class(class).Text(label),
			),
		),
	)
}

// configBadge returns the class and the label of the AI config badge.
func (d *Dashboard) configBadge() (class, label string) {
	mach := d.model.Mach()

	switch {
	case mach.Is1(ssA.ConfigValid):
		return "badge mr-1 badge-success", "AI Connected"
	case mach.Is1(ssA.ErrAI):
		return "badge mr-1 badge-error", "AI Config Error"
	default:
		return "badge mr-1 badge-primary", "AI Connecting..."
	}
}

func (d *Dashboard) ConfigForm() UI {
	mach := d.model.Mach()
	if mach.Is1(ssA.ConfigValid) || !mach.IsErr() {
		return nil
	}

	var (
		inKey   HTMLInput
		inURL   HTMLInput
		inModel HTMLInput
	)
	defer func() {
		inKey.Class("input " + validatorClass(d.FormKey)).
			Type("password").
			Required(true).
			Placeholder("API Key").
			Value(d.FormKey).
			OnChange(valueTo(&d.FormKey))

		if d.FormBackend != "openai-compat" {
			return
		}
		inURL.Class("input " + validatorClass(d.FormURL)).
			Type("url").
			Required(true).
			Placeholder("Base URL").
			Value(d.FormURL).
			OnChange(valueTo(&d.FormURL))
		inModel.Class("input").
			Required(true).
			Placeholder("Model name").
			Value(d.FormModel).
			OnChange(valueTo(&d.FormModel))
	}()

	return Form().Class("content-center mb-5").OnSubmit(d.SubmitConfig).Body(
		FieldSet().Class("fieldset bg-base-100 border-base-300 rounded-box w-xs border p-4 mx-auto").Body(
			Legend().Class("fieldset-legend text-lg").Text("Config"),
			Label().Class("select").Body(
				Span().Class("label").Text("AI backend"),
				// TODO enum
				Select().Class("select").OnChange(valueTo(&d.FormBackend)).Body(
					Range(backends).Slice(func(i int) UI {
						return d.backendOption(i)
					}),
				),
			),

			// API key
			Div().Body(
				toRef(&inKey, Input()),
				Div().Class("validator-hint").Text("API key not valid"),
			),

			// OpenAI compatible fields
			If(d.FormBackend == "openai-compat", func() UI {
				return Div().Body(
					toRef(&inURL, Input()),
					Div().Class("validator-hint").Text("URL not valid"),
				)
			}),
			If(d.FormBackend == "openai-compat", func() UI {
				return Div().Body(
					toRef(&inModel, Input()),
				)
			}),

			Button().Class("btn btn-neutral mt-4").Text("Save"),
		),
	)
}

func (d *Dashboard) backendOption(i int) UI {
	b := backends[i]
	opt := Option().Text(b.label).Value(b.value)
	// Selected(false) would render selected="false", which selects the option
	if d.FormBackend == b.value {
		opt.Selected(true)
	}

	return opt
}

func (d *Dashboard) SubmitConfig(_ Context, _ Event) {
	// TODO validate
	args := configArgs(d.FormBackend, d.FormKey, d.FormURL, d.FormModel)
	if args == nil {
		return
	}

	d.model.Mach().Add1(ssA.ConfigUpdate, am.Pass(args))
}

func (d *Dashboard) Metrics() UI {
	reqAI, reqTools, ceil := d.requestMetrics()

	rows := []struct {
		label string
		value uint64
	}{
		{"AI", reqAI},
		{"Tools", reqTools},
	}

	// TODO meter connected TUIs
	return Div().Class("mb-5").Body(
		H2().Class("text-xl mb-5").Text("Active Requests"),
		Ul().Class("list bg-base-100 rounded-box").Body(
			Range(rows).Slice(func(i int) UI {
				row := rows[i]
				return Li().Class("list-row").Body(
					Div().Class("size-10 pt-3").Text(row.label),
					Div().Class("tooltip").
						Attr("data-tip", fmt.Sprintf("%d / %d", row.value, ceil)).Body(
						Progress().Class("progress mt-4").Value(row.value).Max(ceil),
					),
					Div().Class("size-3 pt-3").Text(row.value),
				)
			}),
		),
	)
}

// requestMetrics returns the active AI and tool requests, and the bar ceiling.
func (d *Dashboard) requestMetrics() (reqAI, reqTools, ceil uint64) {
	mach := d.model.Mach()

	reqAI = (mach.Tick(ssA.RequestingAI) - mach.Tick(ssA.RequestedAI)) / 2
	reqTools = (mach.Tick(ssA.RequestingTool) - mach.Tick(ssA.RequestedTool)) / 2
	ceil = max(10, reqAI*2, reqTools*2)

	return reqAI, reqTools, ceil
}

func (d *Dashboard) Splash() UI {
	splash := d.model.Splash()
	if splash == "" {
		return nil
	}

	lines := strings.Split(httpToAnchor(d.model.ConfigBase(), splash), "\n")

	// TODO give names to links
	// TODO black via css
	return Div().Class("mockup-code bg-black w-full mb-5").Body(
		Range(lines).Slice(func(i int) UI {
			return Raw("<pre>" + lines[i] + "</pre>")
		}),
	)
}

func (d *Dashboard) Footer() UI {
	cfg := d.model.ConfigBase()

	return Footer().Class(
		"footer sm:footer-horizontal footer-center rounded-box bg-base-100 text-base-content p-4").Body(
		Aside().Body(
			P().Body(Raw("<span>" + fixHTMLAnchors(cfg.Agent.Footer) + "</span>")),
		),
	)
}

// validatorClass returns the input class of a required field.
func validatorClass(value string) string {
	if value == "" {
		return "validator"
	}

	return ""
}

// configArgs converts the config form to config update args.
func configArgs(backend, key, url, model string) *shared.AConfigUpdate {
	// TODO validate
	if key == "" {
		return nil
	}

	// TODO enum
	switch backend {

	case "openai":
		cfg := shared.ConfigDefaultAIOpenAI()
		cfg.Key = key
		return &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{cfg},
			},
		}

	case "deepseek":
		return &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					ConfigAICommon: shared.ConfigAICommon{
						Key:   key,
						Model: "deepseek-v4.1-flash",
						URL:   "https://api.deepseek.com/v1",
					},
				}},
			},
		}

	case "gemini":
		cfg := shared.ConfigDefaultAIGemini()
		cfg.Key = key
		return &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				Gemini: []shared.ConfigAIGemini{cfg},
			},
		}

	case "openai-compat":
		return &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					ConfigAICommon: shared.ConfigAICommon{
						Key:   key,
						Model: model,
						URL:   url,
					},
				}},
			},
		}
	}

	return nil
}

func httpToAnchor(cfg *shared.Config, html string) string {
	if !AnchorUrls {
		return html
	}

	html = httpToAnchorRe.ReplaceAllString(html, `<a href="$0" class="text-info hover:underline" target="_blank">$0</a>`)
	html = strings.ReplaceAll(html, cfg.Web.DashURL()+"</a>", "Dashboard</a>")
	html = strings.ReplaceAll(html, cfg.Web.AgentURL()+"</a>", "Agent UI</a>")

	return html
}

// TODO css
func fixHTMLAnchors(html string) string {
	return addAnchorClassRe.ReplaceAllString(html, `<a class="text-info hover:underline"`)
}
