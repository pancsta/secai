package browser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
	ssbase "github.com/pancsta/secai/states"
)

type testModel struct {
	mach    *am.Machine
	cfg     *shared.Config
	store   *shared.AgentStore
	msgs    []*shared.Msg
	stories []shared.StoryInfo
	actions []shared.ActionInfo
	splash  string
}

func (m *testModel) Mach() *am.Machine            { return m.mach }
func (m *testModel) ConfigBase() *shared.Config   { return m.cfg }
func (m *testModel) Store() *shared.AgentStore    { return m.store }
func (m *testModel) Msgs() []*shared.Msg          { return m.msgs }
func (m *testModel) Stories() []shared.StoryInfo  { return m.stories }
func (m *testModel) Actions() []shared.ActionInfo { return m.actions }
func (m *testModel) Splash() string               { return m.splash }

func newTestModel(t *testing.T) *testModel {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mach, err := am.NewCommon(ctx, "test-web", ssbase.AgentSchema, ssA.Names(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mach.Dispose)
	mach.Add1(ssA.Start, nil)

	return &testModel{
		mach: mach,
		cfg: &shared.Config{Agent: shared.ConfigAgent{
			Label:     "Test Agent",
			Intro:     "intro",
			IntroDash: "<b>intro</b>",
			Footer:    "footer",
		}},
		store: &shared.AgentStore{
			ClockDiff: [][]int{{1, 2, 3}, {4, 5, 6}},
		},
		msgs:    []*shared.Msg{shared.NewMsg("hello there", shared.FromAssistant)},
		stories: []shared.StoryInfo{{State: ssA.Start, Title: "Start", Desc: "started"}},
		actions: []shared.ActionInfo{{ID: "a1", Label: "Do it", Action: true, VisibleAgent: true, VisibleMem: true}},
		splash:  "splash\ntext",
	}
}

func newTestServer(t *testing.T, model Model) (*Pages[*Dashboard, *AgentUI], *httptest.Server) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pages, err := NewPages(ctx, model, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pages.Dispose)

	mux := http.NewServeMux()
	pages.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return pages, srv
}

func getBody(t *testing.T, url string) string {
	t.Helper()

	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want 200", url, res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func TestPagesRender(t *testing.T) {
	model := newTestModel(t)
	_, srv := newTestServer(t, model)

	// dashboard
	body := getBody(t, srv.URL+"/")
	if !strings.Contains(body, `<body class="bg-base-300 p-4">`) {
		t.Error("dashboard body is not a scrolling page")
	}
	for _, want := range []string{
		"Test Agent",
		"footer",
		"<b>intro</b>",
		"Active Requests",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}
	// no config form without an error
	if strings.Contains(body, "fieldset-legend") {
		t.Error("dashboard shows the config form without an error")
	}

	// agent UI
	body = getBody(t, srv.URL+"/agent/")
	if !strings.Contains(body, `<body class="bg-base-300 h-screen w-screen overflow-hidden p-4">`) {
		t.Error("agent UI body is not full-screen")
	}
	for _, want := range []string{
		"Messages",
		"hello there",
		"Stories",
		"Do it",
		`id="clockmoji"`,
		`data-reset="0"`,
		"Type your message here...",
		"[1,2,3]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent UI is missing %q", want)
		}
	}
	// livepage assets
	for _, path := range []string{"/assets/livepage.js", "/agent/assets/livepage.js"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, res.StatusCode)
		}
	}
}

func TestConfigFormSelected(t *testing.T) {
	model := newTestModel(t)
	model.mach.AddErr(errors.New("test err"), nil)

	pages, srv := newTestServer(t, model)

	body := getBody(t, srv.URL+"/")
	if !strings.Contains(body, "fieldset-legend") {
		t.Fatal("dashboard is missing the config form")
	}
	// a false bool would render selected="false", which selects the option
	if got := strings.Count(body, "selected"); got != 1 {
		t.Errorf("selected attributes = %d, want 1", got)
	}
	if !strings.Contains(body, `selected value="openai"`) {
		t.Error("the openai option is not selected")
	}

	// selected backend
	pages.Dashboard.Compo().FormBackend = "gemini"
	pages.Dashboard.HRefresh()
	html, ok := pages.Dashboard.HTML()
	if !ok {
		t.Fatal("dashboard render timed out")
	}
	if !strings.Contains(html, `selected value="gemini"`) {
		t.Error("the gemini option is not selected")
	}
	if got := strings.Count(html, "selected"); got != 1 {
		t.Errorf("selected attributes = %d, want 1", got)
	}

	// server-side form values are rendered into the inputs
	pages.Dashboard.Compo().FormKey = "sk-test"
	pages.Dashboard.HRefresh()
	html, ok = pages.Dashboard.HTML()
	if !ok {
		t.Fatal("dashboard render timed out")
	}
	if !strings.Contains(html, `value="sk-test"`) {
		t.Error("the API key input is missing its value")
	}
}

func TestPromptSubmit(t *testing.T) {
	model := newTestModel(t)
	pages, _ := newTestServer(t, model)

	ch := make(chan string, 1)
	_, err := model.mach.HandlersBind(&promptCapture{ch: ch})
	if err != nil {
		t.Fatal(err)
	}

	ui := pages.AgentUI.Compo()
	ui.formPrompt = "pasta"
	ui.promptSubmit(Context{}, Event{})

	select {
	case got := <-ch:
		if got != "pasta" {
			t.Errorf("prompt = %q, want pasta", got)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt was not added")
	}

	if ui.formPrompt != "" {
		t.Errorf("prompt = %q, want empty", ui.formPrompt)
	}
	if ui.promptReset != 1 {
		t.Errorf("promptReset = %d, want 1", ui.promptReset)
	}
}

type promptCapture struct {
	ch chan string
}

func (h *promptCapture) PromptState(e *am.Event) {
	h.ch <- am.ParseArgs[shared.APrompt](e.Args).Prompt
}

func TestClockDiff(t *testing.T) {
	model := newTestModel(t)
	ui := &AgentUI{BasePage: BasePage{model: model}}

	diff := ui.clockDiff()
	if len(diff) != clockWidth {
		t.Fatalf("clock series = %d, want %d", len(diff), clockWidth)
	}
	if !reflect.DeepEqual(diff[0], []int{1, 2, 3}) {
		t.Errorf("clock series 0 = %v", diff[0])
	}
	if !reflect.DeepEqual(diff[1], []int{4, 5, 6}) {
		t.Errorf("clock series 1 = %v", diff[1])
	}
	for i := 2; i < clockWidth; i++ {
		if len(diff[i]) != 0 {
			t.Errorf("clock series %d = %v, want empty", i, diff[i])
		}
	}
}

func TestConfigArgs(t *testing.T) {
	if args := configArgs("openai", "", "", ""); args != nil {
		t.Error("empty key is not nil")
	}

	args := configArgs("openai", "key", "", "")
	if args == nil || len(args.ConfigAI.OpenAI) != 1 || args.ConfigAI.OpenAI[0].Key != "key" {
		t.Errorf("openai args = %+v", args)
	}

	args = configArgs("deepseek", "key", "", "")
	if args == nil || args.ConfigAI.OpenAI[0].Model == "" {
		t.Errorf("deepseek args = %+v", args)
	}

	args = configArgs("gemini", "key", "", "")
	if args == nil || len(args.ConfigAI.Gemini) != 1 {
		t.Errorf("gemini args = %+v", args)
	}

	args = configArgs("openai-compat", "key", "http://url", "model")
	if args == nil || args.ConfigAI.OpenAI[0].URL != "http://url" ||
		args.ConfigAI.OpenAI[0].Model != "model" {
		t.Errorf("openai-compat args = %+v", args)
	}

	if args := configArgs("unknown", "key", "", ""); args != nil {
		t.Errorf("unknown backend args = %+v", args)
	}
}
