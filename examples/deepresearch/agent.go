// Package deepresearch is a port of atomic-agents/deepresearch to secai.
// https://github.com/BrainBlend-AI/atomic-agents/blob/main/atomic-examples/deep-research/deep_research/main.py
package deepresearch

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/deepresearch/schema"
	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tools/colly"
	schemacolly "github.com/pancsta/secai/tools/colly/schema"
	"github.com/pancsta/secai/tools/getter"
	"github.com/pancsta/secai/tools/searxng"
	"github.com/pancsta/secai/tui"
)

// var bug = true
// var id = "deepresearch-bug"

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

type Agent struct {
	*secai.Agent

	tui *tui.Chat

	// tools

	tDate    *getter.Tool
	tSearxng *searxng.Tool
	tColly   *colly.Tool

	// prompts

	pCheckingInfo *secai.Prompt[schema.ParamsCheckingInfo, schema.ResultCheckingInfo]
	pSearchingLLM *secai.Prompt[schema.ParamsSearching, schema.ResultSearching]
	pAnswering    *secai.Prompt[schema.ParamsAnswering, schema.ResultAnswering]
	payloadList   []string
}

func New(ctx context.Context) (*Agent, error) {

	// init the agent along with the base
	a, err := secai.InitAgent(ctx, id, ss.Names(), schema.ResearchSchema, &Agent{
		Agent: &secai.Agent{
			// TODO automate?
			DisposedHandlers: &ssam.DisposedHandlers{},
		},
	})
	if err != nil {
		return nil, err
	}

	// create a date tool
	a.tDate, _ = getter.New(a, "date", "Current date", func() (string, error) {
		format := "Monday, 2 January, 2006"
		return fmt.Sprintf("The current date in the format %s is %s.", format, time.Now().Format(format)), nil
	})

	// init searxng - websearch tool
	a.tSearxng, err = searxng.New(a)
	if err != nil {
		return nil, err
	}

	// init colly - webscrape tool
	a.tColly, err = colly.New(a)
	if err != nil {
		return nil, err
	}

	// init prompts
	a.pCheckingInfo = schema.NewCheckingInfoPrompt(a)
	a.pSearchingLLM = schema.NewSearchingLLMPrompt(a)
	a.pAnswering = schema.NewAnsweringPrompt(a)

	// register tools
	secai.ToolAddToPrompts(a.tSearxng, a.pSearchingLLM, a.pAnswering)
	secai.ToolAddToPrompts(a.tDate, a.pCheckingInfo, a.pSearchingLLM, a.pAnswering)
	secai.ToolAddToPrompts(a.tColly, a.pAnswering)

	return a, nil
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
	a.payloadList = strings.Split(StarterQuestions, "\n")

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
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
			case <-mach.When1(ss.Interrupt, stepCtx):
				// loop again
			// TODO ErrLoop
			// case <-mach.When1(ss.Interrupt, stepCtx):
			// 	// loop again

			// timeout - trigger an interruption
			case <-time.After(timeout):
				done := mach.WhenTicks(ss.Interrupt, 2, stepCtx)
				// TODO typed args
				mach.Add1(ss.Interrupt, am.A{"timeout": true})
				mach.AddErr(am.ErrTimeout, nil)

				// wait for Interrupt to finish
				<-done
			}
			cancel()
		}
	}()
}

func (a *Agent) InterruptState(e *am.Event) {
	// TODO typed args
	timeout, _ := e.Args["timeout"].(bool)

	if timeout {
		a.Output("Interrupted by a timeout", shared.FromSystem)
	} else {
		a.Output("Interrupted by the user", shared.FromSystem)
	}
	a.Mach().Remove1(ss.Interrupt, nil)
}

func (a *Agent) ReadyEnter(e *am.Event) bool {
	// wait for all the tools to be ready
	return a.tSearxng.Mach().Is1(ss.Ready) && a.tDate.Mach().Is1(ss.Ready)
}

// func (a *Agent) CheckingInfoEnter(e *am.Event) bool {
// 	return a.Mach().Is1(ss.Prompt)
// }

func (a *Agent) UIModeState(e *am.Event) {
	mach := a.Mach()
	tui, err := tui.NewChat(mach, []string{WelcomeMessage, StarterQuestions}, true)
	if err != nil {
		mach.AddErr(err, nil)
		return
	}
	a.tui = tui

	// fork tui
	go tui.Run()
}

func (a *Agent) PromptEnter(e *am.Event) bool {

	p, ok := e.Args["prompt"].(string)
	// TODO config, skip when it's a reference
	return ok && len(p) >= 1
}

func (a *Agent) PromptState(e *am.Event) {
	a.UserInput = e.Args["prompt"].(string)
}

func (a *Agent) CheckingInfoState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.CheckingInfo)
	llm := a.pCheckingInfo

	// parse references
	if len(a.payloadList) > 0 {
		num := strings.Trim(a.UserInput, " \n\t.")
		i, err := strconv.Atoi(num)
		// TODO config, read a.ChoiceOpts
		if err == nil && i <= len(a.payloadList) {
			// expand number to value
			// TODO support rich values
			a.UserInput = a.payloadList[i-1]
			// remove prefix
			if strings.HasPrefix(a.UserInput, num) {
				_, a.UserInput, _ = strings.Cut(a.UserInput, num)
			}
			a.UserInput = strings.TrimLeft(a.UserInput, " \n\t.")
		}
	}

	prompt := a.UserInput
	a.Output("Checking...", shared.FromAssistant)

	// TODO detect NLP references to numbers (prompt)

	// unblock
	go func() {

		// run the prompt (checks ctx)
		res, err := llm.Run(schema.ParamsCheckingInfo{
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
		res, err := llm.Run(schema.ParamsSearching{
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

		mach.Log("Queries: %s", strings.Join(res.Queries, ";"))

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
	if !mock {
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
		res, err := llm.Run(schema.ParamsAnswering{Question: input}, "")
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
	// TODO config
	a.payloadList = res.FollowUpQuestions[:min(3, len(res.FollowUpQuestions))]
	// ask again
	a.Mach().EvAdd1(e, ss.InputPending, nil)
}

// REST

func (a *Agent) Output(msg string, from shared.From) {
	if a.Mach().Is1(ss.UIMode) {
		a.tui.AddMsg(msg, from)
	} else {
		shared.P(msg)
	}
}
