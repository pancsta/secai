package browser

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
)

var httpToAnchorRe = regexp.MustCompile(`https?://\S+`)
var addAnchorClassRe = regexp.MustCompile(`<a`)

func (d *Dashboard) Render() UI {
	if !d.Ready() {
		return d.spinner()
	}

	return Div().ID("app").Body(
		d.header(),
		d.configForm(),
		d.splash(),
		d.metrics(),
		d.footer(),
	)
}

func (d *Dashboard) header() UI {
	mach := d.mach
	a := d.agentClient.NetMach

	var err UI
	if a.IsErr() {
		err = Div().Class("badge mr-1 badge-error").Text("Exception")
	}
	// TODO Err badge

	// connection badge
	conn := Div().Class("badge-success").Text("Connection OK")
	if mach.Not1(ss.RPCConnected) {
		conn = Div().Class("badge-error").Text("Connection Error")
		if mach.Is1(ss.RPCConnecting) {
			conn = Div().Class("badge-primary").Text("Connecting...")
		}
	}
	conn = conn.Class("badge mr-1")

	// config badge
	cfg := Div().Class("badge badge-success").Text("Config OK")
	if a.Not1(ssA.ConfigValid) {
		cfg = Div().Class("badge badge-error").Text("Config Error")
		if a.Not1(ssA.Exception) {
			cfg = Div().Class("badge badge-primary").Text("Checking Config...")
		}
	}
	cfg = cfg.Class("badge")

	// header

	return []UI{

		// <HTML>

		Div().Class("card card-border bg-base-100 mb-5").Body(
			Div().Class("card-body").Body(
				H2().Class("card-title text-warning").Text(fmt.Sprintf(
					"%s Dashboard", d.boot.Config.Agent.Label)),
				P().Body(
					Raw(fixHTMLAnchors("<span>"+d.boot.Config.Agent.IntroDash+"</span>")),
				),
			),
			Div().Class("text-right pb-3 pr-3").Body(err, conn, cfg),
		),

		// </HTML>

	}[0]
}

func (d *Dashboard) configForm() UI {
	a := d.agentClient.NetMach
	if a.Is1(ssA.ConfigValid) || !a.IsErr() || d.mach.Transition() != nil {
		return nil
	}

	valid := ""
	if d.formKey == "" {
		valid = "validator"
	}
	fKey := []UI{
		Input().Class("input " + valid).Type("password").Required(true).Placeholder("API Key").
			Value(d.formKey).OnChange(d.ValueTo(&d.formKey)),
		Div().Class("validator-hint").Text("API key not valid"),
	}

	valid = ""
	if d.formURL == "" {
		valid = "validator"
	}
	fURL := []UI{
		Input().Class("input " + valid).Type("url").Required(true).Placeholder("Base URL").
			Value(d.formURL).OnChange(d.ValueTo(&d.formURL)),
		Div().Class("validator-hint").Text("URL not valid"),
	}

	fModel := []UI{
		Input().Class("input").Required(true).Placeholder("Model name").
			Value(d.formModel).OnChange(d.ValueTo(&d.formModel)),
	}

	var formFields []UI
	// TODO enum
	switch d.formBackend {
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
			OnSubmit(d.submitConfig).Body(
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
					).OnChange(d.ValueTo(&d.formBackend)),
				),

				Div().Body(formFields...),

				Button().Class("btn btn-neutral mt-4").Text("Save"),
			),
		),

		// </HTML>

	}[0]
}

func (d *Dashboard) submitConfig(ctx Context, e Event) {
	e.PreventDefault()
	agent := d.agentClient.NetMach

	// TODO validate
	if d.formKey == "" {
		return
	}

	// TODO enum
	var args *ABase
	switch d.formBackend {

	case "openai":
		cfg := shared.ConfigDefaultOpenAI()
		cfg.Key = d.formKey
		args = &ABase{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{cfg},
			},
		}

	case "deepseek":
		args = &ABase{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					Key:   d.formKey,
					URL:   "https://api.deepseek.com/v1",
					Model: "deepseek-chat",
				}},
			},
		}

	case "gemini":
		cfg := shared.ConfigDefaultGemini()
		cfg.Key = d.formKey
		args = &ABase{
			ConfigAI: &shared.ConfigAI{
				Gemini: []shared.ConfigAIGemini{cfg},
			},
		}

	case "openai-compat":
		args = &ABase{
			ConfigAI: &shared.ConfigAI{
				OpenAI: []shared.ConfigAIOpenAI{{
					Key:   d.formKey,
					URL:   d.formURL,
					Model: d.formModel,
				}},
			},
		}
	}

	// TODO state?
	d.formSubmitting = true
	go func() {
		defer func() {
			d.formSubmitting = false
		}()

		when := agent.When1(ssA.ConfigUpdate, ctx.Context)
		agent.Add1(ssA.ConfigUpdate, PassRpcBase(args))
		err := amhelp.WaitForAll(ctx.Context, 3*time.Second, when)
		if err != nil {
			d.formErr = "timeout"
			return
		}
	}()
}

func (d *Dashboard) metrics() UI {
	a := d.agentClient
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

func (d *Dashboard) splash() HTML {
	if d.mach.Not1(ss.Data) || d.mach.Transition() != nil {
		return nil
	}

	// \n to <pre>
	var lines []UI
	splash := httpToAnchor(d.boot.Config, d.data.Splash)
	for _, l := range strings.Split(splash, "\n") {
		lines = append(lines, Raw("<pre>"+l+"</pre>"))
	}

	// TODO give names to links

	return Div().Class("mockup-code w-full mb-5").Body(lines...)
}

func (d *Dashboard) footer() UI {

	return []UI{

		// <HTML>

		Footer().Class(
			"footer sm:footer-horizontal footer-center rounded-box bg-base-100 text-base-content p-4").Body(
			Aside().Body(
				P().Body(Raw("<span>" + fixHTMLAnchors(d.boot.Config.Agent.Footer) + "</span>")),
			),
		),

		// </HTML>

	}[0]
}

func httpToAnchor(cfg *shared.Config, html string) string {
	html = httpToAnchorRe.ReplaceAllString(html, `<a href="$0" class="text-info hover:underline" target="_blank">$0</a>`)
	html = strings.ReplaceAll(html, cfg.Web.DashURL()+"</a>", "Dashboard</a>")
	html = strings.ReplaceAll(html, cfg.Web.AgentURL()+"</a>", "Agent UI</a>")

	return html
}

// TODO css
func fixHTMLAnchors(html string) string {
	return addAnchorClassRe.ReplaceAllString(html, `<a class="text-info hover:underline"`)
}
