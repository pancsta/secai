package browser

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
)

var httpToAnchorRe = regexp.MustCompile(`https?://\S+`)
var addAnchorClassRe = regexp.MustCompile(`<a`)

var AnchorUrls = true

func (d *Dashboard) Render() UI {
	if !d.Ready() {
		return d.Spinner()
	}

	return Div().Body(
		d.Header(),
		d.ConfigForm(),
		d.Splash(),
		d.Metrics(),
		d.Footer(),
	)
}

func (d *Dashboard) Header() UI {
	mach := d.Mach
	a := d.AgentClient.NetMach

	var err UI
	if a.IsErr() {
		err = Div().Class("badge mr-1 badge-error").Text("Exception")
	}

	// connection badge
	conn := Div().Class("badge-success").Text("Agent Connected")
	if mach.Not1(ss.RPCConnected) {
		// TODO reconn on click
		conn = Div().Class("badge-error").Text("Agent Missing")
		if mach.Is1(ss.RPCConnecting) {
			conn = Div().Class("badge-primary").Text("Agent Connecting...")
		}
	}
	conn = conn.Class("badge mr-1")

	// config badge
	cfg := Div().Class("badge badge-success").Text("AI Connected")
	if a.Not1(ssA.ConfigValid) {
		cfg = Div().Class("badge badge-primary").Text("AI Connecting...")
		if a.Is1(ssA.ErrAI) {
			cfg = Div().Class("badge badge-error").Text("AI Config Error")
		}
	}
	cfg = cfg.Class("badge")

	// header

	return []UI{

		// <HTML>
		Div().Body(
			H2().Class("card-title text-warning mb-5").Text(d.Boot.Config.Agent.Label),
			Div().Class("card card-border bg-base-100 mb-5").Body(
				Div().Class("card-body").Body(
					Raw(fixHTMLAnchors("<div>"+d.Boot.Config.Agent.IntroDash+"</div>")),
				),
				Div().Class("text-right pb-3 pr-3").Body(err, conn, cfg),
			),
		),

		// </HTML>

	}[0]
}

func (d *Dashboard) ConfigForm() UI {
	a := d.AgentClient.NetMach
	if a.Is1(ssA.ConfigValid) || !a.IsErr() || d.Mach.Transition() != nil {
		return nil
	}

	valid := ""
	if d.FormKey == "" {
		valid = "validator"
	}
	fKey := []UI{
		Input().Class("input " + valid).Type("password").Required(true).Placeholder("API Key").
			Value(d.FormKey).OnChange(d.ValueTo(&d.FormKey)),
		Div().Class("validator-hint").Text("API key not valid"),
	}

	valid = ""
	if d.FormURL == "" {
		valid = "validator"
	}
	fURL := []UI{
		Input().Class("input " + valid).Type("url").Required(true).Placeholder("Base URL").
			Value(d.FormURL).OnChange(d.ValueTo(&d.FormURL)),
		Div().Class("validator-hint").Text("URL not valid"),
	}

	fModel := []UI{
		Input().Class("input").Required(true).Placeholder("Model name").
			Value(d.FormModel).OnChange(d.ValueTo(&d.FormModel)),
	}

	var formFields []UI
	// TODO enum
	switch d.FormBackend {
	case "openai":
		fallthrough
	case "deepseek":
		fallthrough
	case "gemini":
		formFields = fKey
	case "openai-compat":
		formFields = slices.Concat(
			fKey,
			fURL,
			fModel,
		)
	}

	return []UI{

		// <HTML>

		Form().Class("content-center mb-5").
			OnSubmit(d.SubmitConfig).Body(
			FieldSet().Class(
				"fieldset bg-base-100 border-base-300 rounded-box w-xs border p-4 mx-auto").Body(
				Legend().Class("fieldset-legend text-lg").Text("Config"),
				Label().Class("select").Body(
					Span().Class("label").Text("AI backend"),
					Select().Class("select").Body(
						// TODO enum
						Option().Text("OpenAI").Value("openai"),
						Option().Text("DeepSeek").Value("deepseek"),
						Option().Text("Gemini").Value("gemini"),
						Option().Text("OpenAI compatible").Value("openai-compat"),
					).OnChange(d.ValueTo(&d.FormBackend)),
				),

				Div().Body(formFields...),

				Button().Class("btn btn-neutral mt-4").Text("Save"),
			),
		),

		// </HTML>

	}[0]
}

func (d *Dashboard) SubmitConfig(ctx Context, e Event) {
	e.PreventDefault()
	agent := d.AgentClient.NetMach

	// TODO validate
	if d.FormKey == "" {
		return
	}

	// TODO enum
	var args *shared.AConfigUpdate
	switch d.FormBackend {

	case "openai":
		cfg := shared.ConfigDefaultAIOpenAI()
		cfg.Key = d.FormKey
		args = &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{cfg},
			},
		}

	case "deepseek":
		args = &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					ConfigAICommon: shared.ConfigAICommon{
						Key:   d.FormKey,
						Model: "deepseek-chat",
					},
					URL: "https://api.deepseek.com/v1",
				}},
			},
		}

	case "gemini":
		cfg := shared.ConfigDefaultAIGemini()
		cfg.Key = d.FormKey
		args = &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				Gemini: []shared.ConfigAIGemini{cfg},
			},
		}

	case "openai-compat":
		args = &shared.AConfigUpdate{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					ConfigAICommon: shared.ConfigAICommon{
						Key:   d.FormKey,
						Model: d.FormModel,
					},
					URL: d.FormURL,
				}},
			},
		}
	}

	// TODO state?
	d.FormSubmitting = true
	go func() {
		defer func() {
			d.FormSubmitting = false
		}()

		when := agent.When1(ssA.ConfigUpdate, ctx.Context)
		agent.Add1(ssA.ConfigUpdate, am.Pass(args))
		err := amhelp.WaitForAll(ctx.Context, 3*time.Second, when)
		if err != nil {
			d.FormErr = "timeout"
			return
		}
	}()
}

func (d *Dashboard) Metrics() UI {
	a := d.AgentClient
	if a == nil {
		return nil
	}

	reqAI := a.NetMach.Tick(ssA.RequestingAI) - a.NetMach.Tick(ssA.RequestedAI)
	reqAI /= 2
	reqTools := a.NetMach.Tick(ssA.RequestingTool) - a.NetMach.Tick(ssA.RequestedTool)
	reqTools /= 2
	ceil := max(10, reqAI*2, reqTools*2)

	// TODO meter connected TUIs
	return []UI{

		// <HTML>

		Div().Class("mb-5").Body(
			H2().Class("text-xl mb-5").Text("Active Requests"),
			Ul().Class("list bg-base-100 rounded-box").Body(
				Li().Class("list-row").Body(
					Div().Class("size-10 pt-3").Text("AI"),
					Div().Class("tooltip").Attr("data-tip", fmt.Sprintf("%d / %d", reqAI, ceil)).Body(
						Progress().Class("progress mt-4").Value(reqAI).Max(ceil),
					),
					Div().Class("size-3 pt-3").Text(reqAI),
				),
				Li().Class("list-row").Body(
					Div().Class("size-10 pt-3").Text("Tools"),
					Div().Class("tooltip").Attr("data-tip", fmt.Sprintf("%d / %d", reqTools, ceil)).Body(
						Progress().Class("progress mt-4").Value(reqTools).Max(ceil),
					),
					Div().Class("size-3 pt-3").Text(reqTools),
				),
			),
		),

		// </HTML>

	}[0]
}

func (d *Dashboard) Splash() UI {
	if d.Mach.Not1(ss.Data) || d.Mach.Transition() != nil {
		return nil
	}

	// \n to <pre>
	var lines []UI
	splash := httpToAnchor(d.Boot.Config, d.Data.Splash)
	for _, l := range strings.Split(splash, "\n") {
		lines = append(lines, Raw("<pre>"+l+"</pre>"))
	}

	// TODO give names to links
	// TODO black via css
	return Div().Class("mockup-code bg-black w-full mb-5").Body(lines...)
}

func (d *Dashboard) Footer() UI {

	return []UI{

		// <HTML>

		Footer().Class(
			"footer sm:footer-horizontal footer-center rounded-box bg-base-100 text-base-content p-4").Body(
			Aside().Body(
				P().Body(Raw("<span>" + fixHTMLAnchors(d.Boot.Config.Agent.Footer) + "</span>")),
			),
		),

		// </HTML>

	}[0]
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
