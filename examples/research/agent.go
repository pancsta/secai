// Package deepresearch is a port of atomic-agents/deepresearch to secai.
// https://github.com/BrainBlend-AI/atomic-agents/blob/main/atomic-examples/deep-research/deep_research/main.py
package research

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gliderlabs/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/research/schema"
	llmagent "github.com/pancsta/secai/llm_agent"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tools/colly"
	schemacolly "github.com/pancsta/secai/tools/colly/schema"
	"github.com/pancsta/secai/tools/getter"
	"github.com/pancsta/secai/tools/searxng"
	"github.com/pancsta/secai/tui"
)

const id = "deepresearch"
const mock = false

var ss = schema.ResearchStates
var Sp = shared.Sp
var Sl = shared.Sl
var Sj = shared.Sj

type S = am.S

var WelcomeMessage = Sj(
	"Welcome to Deep Research - your AI-powered research assistant! I can help you explore and ",
	"understand any topic through detailed research and interactive discussion.",
)

var StarterQuestions = Sp(`
    1. Can you help me research the latest AI news?
    2. Who won the Nobel Prize in Physics this year?
    3. Where can I learn more about quantum computing?
`)

type Config struct {

	// SECAI TODO extract

	OpenAIAPIKey   string `arg:"env:OPENAI_API_KEY" help:"OpenAI API key."`
	DeepseekAPIKey string `arg:"env:DEEPSEEK_API_KEY" help:"DeepSeek API key."`
	TUIPort        int    `arg:"env:SECAI_TUI_PORT" help:"SSH port for the TUI." default:"7854"`
	TUIHost        string `arg:"env:SECAI_TUI_HOST" help:"SSH host for the TUI." default:"localhost"`
	Mock           bool   `arg:"env:SECAI_MOCK" help:"Enable scenario mocking."`
	ReqLimit       int    `arg:"env:SECAI_REQ_LIMIT" help:"Max LLM requests per session." default:"1000"`
}

type Agent struct {
	// inherit from LLM Agent
	*llmagent.Agent

	Config Config
	TUIs   []shared.UI
	srvUI  *ssh.Server
	Msgs   []*shared.Msg

	// tools

	tDate    *getter.Tool
	tSearxng *searxng.Tool
	tColly   *colly.Tool

	// prompts

	pCheckingInfo *secai.Prompt[schema.ParamsCheckingInfo, schema.ResultCheckingInfo]
	pSearchingLLM *secai.Prompt[schema.ParamsSearching, schema.ResultSearching]
	pAnswering    *secai.Prompt[schema.ParamsAnswering, schema.ResultAnswering]
}

// NewResearch returns a preconfigured instance of Agent.
func NewResearch(ctx context.Context, config Config) (*Agent, error) {
	a := New(ctx, id, ss.Names(), schema.ResearchSchema)
	if err := a.Init(a); err != nil {
		return nil, err
	}
	a.Config = config

	return a, nil
}

// New returns a custom instance of Agent.
func New(
	ctx context.Context, id string, states am.S, machSchema am.Schema,
) *Agent {

	a := &Agent{
		Agent: llmagent.New(ctx, id, states, machSchema),
	}

	// predefined msgs
	a.Msgs = append(a.Msgs,
		shared.NewMsg(WelcomeMessage, shared.FromSystem),
		shared.NewMsg(StarterQuestions, shared.FromSystem),
	)

	return a
}

func (a *Agent) Init(agent secai.AgentAPI) error {
	// call super
	err := a.Agent.Init(agent)
	if err != nil {
		return err
	}
	mach := a.Mach()

	// args mapper for logging
	mach.SetLogArgs(LogArgs)

	// create a date tool
	a.tDate, _ = getter.New(a, "date", "Current date", func() (string, error) {
		format := "Monday, 2 January, 2006"
		return fmt.Sprintf("The current date in the format %s is %s.", format, time.Now().Format(format)), nil
	})

	// init searxng - websearch tool
	a.tSearxng, err = searxng.New(a)
	if err != nil {
		return err
	}

	// init colly - webscrape tool
	a.tColly, err = colly.New(a)
	if err != nil {
		return err
	}

	// init prompts
	a.pCheckingInfo = schema.NewCheckingInfoPrompt(a)
	a.pSearchingLLM = schema.NewSearchingLLMPrompt(a)
	a.pAnswering = schema.NewAnsweringPrompt(a)

	// register tools
	secai.ToolAddToPrompts(a.tSearxng, a.pSearchingLLM, a.pAnswering)
	secai.ToolAddToPrompts(a.tDate, a.pCheckingInfo, a.pSearchingLLM, a.pAnswering)
	secai.ToolAddToPrompts(a.tColly, a.pAnswering)

	return nil
}

func (a *Agent) nextUIName(uiType string) string {
	i := 0
	// TODO enum
	switch uiType {
	case "chat":
		for _, ui := range a.TUIs {
			if _, ok := ui.(*tui.Chat); ok {
				i++
			}
		}

	case "clock":
		for _, ui := range a.TUIs {
			if _, ok := ui.(*tui.Clock); ok {
				i++
			}
		}
	}

	return strconv.Itoa(i)
}

// clockStates returns the list of states monitored in the chat's clock emoji chart.
func (a *Agent) clockStates() S {
	trackedStates := a.Mach().StateNames()

	// dont track a global handler
	// trackedStates = shared.SlicesWithout(trackedStates, ss.CheckStories)

	return trackedStates
}

// HANDLERS

func (a *Agent) ExceptionState(e *am.Event) {
	// call base handler
	a.ExceptionHandler.ExceptionState(e)
	args := am.ParseArgs(e.Args)

	// show the error
	a.Output(fmt.Sprintf("ERROR: %s", args.Err), shared.FromSystem)

	// TODO remove empty errors (eg Scraping) and add ErrLoop for breaking errs
}

func (a *Agent) StartState(e *am.Event) {
	// parent handler
	a.Agent.StartState(e)

	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Start)

	// start the UI
	mach.Add(S{ss.InputPending, ss.UIMode}, nil)
	a.OfferList = strings.Split(StarterQuestions, "\n")

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// wait for tools
		<-mach.When1(ss.Ready, ctx)

		// start the chat
		a.Mach().Add1(ss.CheckingInfo, nil)
	}()
}

func (a *Agent) LoopState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Loop)

	// unblock
	go func() {
		for {
			if ctx.Err() != nil {
				return // expired
			}

			// wait for the user
			<-mach.When1(ss.Prompt, ctx)
			mach.Add1(ss.CheckingInfo, nil)

			// wait for the agent with a timeout
			timeout := time.Minute
			if amhelp.IsDebug() {
				timeout *= 10
			}

			// step ctx (for this select only)
			stepCtx, cancel := context.WithCancel(ctx)
			select {

			case <-ctx.Done():
				// expired
				cancel()
				return

			case <-mach.When1(ss.Answered, stepCtx):
				// loop again
			case <-mach.When1(ss.Interrupted, stepCtx):
				// loop again
			// TODO ErrLoop
			// case <-mach.When1(ss.Interrupt, stepCtx):
			// 	// loop again

			// timeout - trigger an interruption
			case <-time.After(timeout):
				done := mach.WhenTicks(ss.Interrupted, 2, stepCtx)
				// TODO typed args
				mach.Add1(ss.Interrupted, am.A{"timeout": true})
				mach.AddErr(am.ErrTimeout, nil)

				// wait for Interrupt to finish
				<-done
			}
			cancel()
		}
	}()
}

func (a *Agent) InterruptedState(e *am.Event) {
	// call super
	a.Agent.InterruptedState(e)

	// TODO typed args
	timeout, _ := e.Args["timeout"].(bool)

	if timeout {
		a.Output("Interrupted by a timeout", shared.FromSystem)
	} else {
		a.Output("Interrupted by the user", shared.FromSystem)
	}
	a.Mach().Remove1(ss.Interrupted, nil)
}

func (a *Agent) ReadyEnter(e *am.Event) bool {
	// wait for all the tools to be ready
	return a.tSearxng.Mach().Is1(ss.Ready) && a.tDate.Mach().Is1(ss.Ready)
}

// func (a *Agent) CheckingInfoEnter(e *am.Event) bool {
// 	return a.Mach().Is1(ss.Prompt)
// }

func (a *Agent) UIModeState(e *am.Event) {
	mach := e.Machine()
	ctx := mach.NewStateCtx(ss.UIMode)

	// new session handler passing to UINewSess state
	var handlerFn ssh.Handler = func(sess ssh.Session) {
		srcAddr := sess.RemoteAddr().String()
		done := make(chan struct{})
		mach.EvAdd1(e, ss.UISessConn, PassAA(&AA{
			SSHSess: sess,
			ID:      sess.User(),
			Addr:    srcAddr,
			Done:    done,
		}))

		// TODO WhenArgs for typed args
		// amhelp.WaitForAll(ctx, time.Hour*9999, mach.WhenArgs(ss.UISessDisconn, am.A{}))

		// keep this session alive
		select {
		case <-ctx.Done():
		case <-done:
		}
	}

	// start the server
	go func() {
		// save srv ref
		optSrv := func(s *ssh.Server) error {
			mach.EvAdd1(e, ss.UISrvListening, PassAA(&AA{
				SSHServer: s,
			}))
			return nil
		}

		addr := a.Config.TUIHost + ":" + strconv.Itoa(a.Config.TUIPort)
		a.Log("SSH UI listening", "addr", addr)
		err := ssh.ListenAndServe(addr, handlerFn, optSrv)
		if err != nil {
			mach.EvAddErrState(e, ss.ErrUI, err, nil)
		}
	}()
}

func (a *Agent) UIModeEnd(e *am.Event) {
	// TUIs
	for _, ui := range a.TUIs {
		_ = ui.Stop()
	}
	a.TUIs = nil

	// SSHs
	if a.srvUI != nil {
		_ = a.srvUI.Close()
	}
}

func (a *Agent) UIReadyEnter(e *am.Event) bool {
	for _, ui := range a.TUIs {
		if ui.UIMach().Not1(ss.Ready) {
			return false
		}
	}

	return true
}

// TODO enter

func (a *Agent) UISessConnState(e *am.Event) {
	mach := e.Machine()
	args := ParseArgs(e.Args)
	sess := args.SSHSess
	done := args.Done
	ctx := mach.NewStateCtx(ss.UIMode)
	var ui shared.UI
	uiType := sess.User()

	// wait for the new UI for UIReady
	mach.Remove1(ss.UIReady, nil)

	screen, err := tui.NewSessionScreen(sess)
	if err != nil {
		err = fmt.Errorf("unable to create screen: %w", err)
		mach.EvAddErrState(e, ss.ErrUI, err, nil)
		return
	}

	// TODO enum
	switch uiType {
	case "chat":
		// init the UI
		mach.Remove1(ss.UIReady, nil)
		ui = tui.NewChat(mach, a.Logger(), slices.Clone(a.Msgs))
		err := ui.Init(ui, screen, a.nextUIName(uiType))
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

	case "clock":
		// init the UI
		mach.Remove1(ss.UIReady, nil)
		ui = tui.NewClock(mach, a.Logger(), a.clockStates())
		err := ui.Init(ui, screen, a.nextUIName(uiType))
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

	default:
		mach.EvAddErrState(e, ss.ErrUI, fmt.Errorf("unknown user: %s", uiType), nil)
		return
	}

	// register
	a.TUIs = append(a.TUIs, ui)

	// start the UI
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		err = ui.Start(sess.Close)
		// TODO log err if not EOF?

		close(done)
		mach.EvAdd1(e, ss.UISessDisconn, PassAA(&AA{
			UI: ui,
		}))
	}()
}

func (a *Agent) UISessDisconnState(e *am.Event) {
	ui := ParseArgs(e.Args).UI

	a.TUIs = shared.SlicesWithout(a.TUIs, ui)
}

func (a *Agent) CheckingInfoState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.CheckingInfo)
	llm := a.pCheckingInfo

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// dereference the prompt TODO extract
		retOffer := make(chan *shared.OfferRef, 1)
		mach.EvAdd1(e, ss.CheckingOfferRefs, PassAA(&AA{
			Prompt:      a.UserInput,
			RetOfferRef: retOffer,
			CheckLLM:    true,
		}))
		select {
		case ret := <-retOffer:
			if ret != nil {
				a.UserInput = a.OfferList[ret.Index]
			}
		case <-ctx.Done():
			return
		}

		prompt := a.UserInput
		a.Output("Checking...", shared.FromAssistant)

		// run the prompt (checks ctx)
		res, err := llm.Run(e, schema.ParamsCheckingInfo{
			UserMessage: prompt,
			DecisionType: Sp(`
				Should we perform a new web search? TRUE if we need new or updated information, FALSE if existing 
				context is sufficient. Consider: 1) Is the context empty? 2) Is the existing information relevant? 
				3) Is the information recent enough?
			`),
		}, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		mach.Log("Need more info: %v", res.Decision)
		mach.Log("Reasoning: %v", res.Reasoning)

		// go to next step, based on the decision from LLM
		if res.Decision {
			mach.EvAdd1(e, ss.NeedMoreInfo, nil)
			a.Output("I need to look into it deeper, just a moment...", shared.FromAssistant)
		} else {
			mach.EvAdd1(e, ss.Answering, nil)
		}
	}()
}

func (a *Agent) SearchingLLMState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.SearchingLLM)
	input := a.UserInput
	llm := a.pSearchingLLM

	// unblock
	go func() {
		// llm indicator
		defer mach.EvRemove1(e, ss.RequestingLLM, nil)
		mach.EvAdd1(e, ss.RequestingLLM, nil)

		// ask LLM for relevant links
		res, err := llm.Run(e, schema.ParamsSearching{
			Instruction: input,
			NumQueries:  3,
		}, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		mach.Log("BaseQueries: %s", strings.Join(res.Queries, ";"))

		// show results to the user
		msg := Sl("[green::b]🔍 Generated search queries:[-::-]")
		for _, q := range res.Queries {
			msg += Sl("  - %s", q)
		}
		a.Output(msg, shared.FromAssistant)

		// next
		mach.EvAdd1(e, ss.SearchingWeb, am.A{"ResultSearching": res})
	}()
}

// InputPendingState is a test mocking handler.
func (a *Agent) InputPendingState(e *am.Event) {
	if !mock || os.Getenv("SECAI_MOCK") == "" {
		return
	}

	switch a.Mach().Tick(ss.InputPending) {
	case 1:
		a.Mach().EvAdd1(e, ss.Prompt, am.A{"prompt": "1"})
	}
}

// TODO SearchingWebEnter

func (a *Agent) SearchingWebState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.SearchingWeb)
	promptParams := e.Args["ResultSearching"].(*schema.ResultSearching)

	// unblock
	go func() {
		// tool indicator
		defer mach.EvRemove1(e, ss.RequestingTool, nil)
		mach.EvAdd1(e, ss.RequestingTool, nil)

		// run a web search for relevant links
		msg := Sl("[yellow::b]🌐 Searching across the web using SearXNG...[-::-]")
		search, err := a.tSearxng.Search(ctx, promptParams)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		// select top results TODO config
		selected := search.Results[:min(3, len(search.Results))]
		mach.Log("Results: %s", shared.Map(selected, func(s *baseschema.Website) string {
			return s.URL
		}))

		// show results to the user
		msg += Sl("[green::b]📑 Found relevant web pages:[-::-]")
		for _, result := range selected {
			msg += Sl("  - [::i:%s]%s[:::-]", result.URL, result.Title)
		}
		msg += "\n[yellow:b]📥 Extracting content from web pages..."
		a.Output(msg, shared.FromAssistant)

		// next
		mach.EvAdd1(e, ss.Scraping, am.A{"[]*Website": selected})
	}()
}

func (a *Agent) ScrapingState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Scraping)
	// TODO typed args
	websites := e.Args["[]*Website"].([]*baseschema.Website)

	// unblock
	go func() {
		// tool indicator
		defer mach.EvRemove1(e, ss.RequestingTool, nil)
		mach.EvAdd1(e, ss.RequestingTool, nil)

		// ignore the result, as the document is already bound to the Answering prompt
		// TODO retry logic
		_, err := a.tColly.Scrape(ctx, schemacolly.ParamsFromWebsites(websites))
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			// failed scraping doesnt stop the flow
			mach.EvAddErr(e, err, nil)
		}

		// next
		mach.EvAdd1(e, ss.Answering, nil)
	}()
}

func (a *Agent) DisposedState(e *am.Event) {
	// the end
	os.Exit(0)
}

func (a *Agent) AnsweringState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Answering)
	llm := a.pAnswering
	input := a.UserInput

	// unblock
	go func() {
		// ask LLM for relevant links
		res, err := llm.Run(e, schema.ParamsAnswering{Question: input}, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		// next TODO typed args
		a.Mach().EvAdd1(e, ss.Answered, am.A{
			"ResultAnswering": res,
		})
	}()
}

func (a *Agent) AnsweredEnter(e *am.Event) bool {
	// TODO typed params
	_, ok := e.Args["ResultAnswering"].(*schema.ResultAnswering)

	return ok
}

func (a *Agent) AnsweredState(e *am.Event) {
	// TODO typed params
	res := e.Args["ResultAnswering"].(*schema.ResultAnswering)

	// the end
	a.Output(res.Answer, shared.FromAssistant)
	// TODO set these questions as payload list
	msg := "Follow up questions:\n"
	for i, q := range res.FollowUpQuestions {
		// TODO config, pass to LLM
		msg += fmt.Sprintf("[::b]%d.[::-] %s\n", i+1, q[:min(100, len(q))])
	}

	a.Output(msg, shared.FromAssistant)
	// TODO config min
	a.OfferList = res.FollowUpQuestions[:min(3, len(res.FollowUpQuestions))]
	// ask again
	a.Mach().EvAdd1(e, ss.InputPending, nil)

	// TODO fix in TUI?
	for _, ui := range a.TUIs {
		ui.Redraw()
	}
}

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

// aliases

type AA = shared.A
type AARpc = shared.ARpc

var PassAA = shared.Pass

const APrefix = "cook"

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// base args of the framework
	*shared.A

	// agent's args
	// TODO

	// agent's non-RPC args
	// TODO
}

// ARpc is a subset of [am.A], that can be passed over RPC (eg no channels, instances, etc)
type ARpc struct {
	// base args of the framework
	*shared.A

	// agent's args
	// TODO
}

// ParseArgs extracts A from [am.Event.Args][APrefix] (decoder).
func ParseArgs(args am.A) *A {
	// RPC-only args (pointer)
	if r, ok := args[APrefix].(*ARpc); ok {
		a := amhelp.ArgsToArgs(r, &A{})
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// RPC-only args (value, eg from a network transport)
	if r, ok := args[APrefix].(ARpc); ok {
		a := amhelp.ArgsToArgs(&r, &A{})
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// regular args (pointer)
	if a, _ := args[APrefix].(*A); a != nil {
		// decode base args
		a.A = shared.ParseArgs(args)

		return a
	}

	// defaults
	return &A{
		A: shared.ParseArgs(args),
	}
}

// Pass prepares [am.A] from A to be passed to further mutations (encoder).
func Pass(args *A) am.A {
	// dont nest in plain maps
	clone := *args
	clone.A = nil
	// ref the clone
	out := am.A{APrefix: &clone}

	// merge with base args
	return am.AMerge(out, shared.Pass(args.A))
}

// PassRpc is a network-safe version of Pass. Use it when mutating aRPC workers.
func PassRpc(args *A) am.A {
	// dont nest in plain maps
	clone := *amhelp.ArgsToArgs(args, &ARpc{})
	clone.A = nil
	out := am.A{APrefix: clone}

	// merge with base args
	return am.AMerge(out, shared.PassRpc(args.A))
}

// LogArgs is an args logger for A and [secai.A].
func LogArgs(args am.A) map[string]string {
	a1 := shared.ParseArgs(args)
	a2 := ParseArgs(args)
	if a1 == nil && a2 == nil {
		return nil
	}

	return am.AMerge(amhelp.ArgsToLogMap(a1, 0), amhelp.ArgsToLogMap(a2, 0))
}
