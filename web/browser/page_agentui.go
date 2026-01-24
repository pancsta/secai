package browser

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
)

const idMsgs = "msgs"

func (a *AgentUI) Render() UI {
	a.Dump("Render", nil)
	if !a.Ready() {
		return a.spinner()
	}

	a.updateClock()

	// TODO Dispatch is enough?
	if a.msgsScrollPending {
		a.scrollMsgs()
		a.msgsScrollPending = false
	}

	cfg := a.boot.Config
	return []UI{

		// <HTML>

		Div().Class("grid grid-cols-10 gap-4 h-full max-h-full").Body(

			// LEFT COLUMN
			Div().Class("col-span-5 flex flex-col gap-4 h-full overflow-hidden").Body(

				a.renderClock(),
				a.renderMsgs(),
				a.renderPromptForm(),
			),

			// RIGHT COLUMN
			Div().Class("col-span-5 flex flex-col gap-4 overflow-hidden").Body(

				// agent Card
				Div().Class("card bg-base-100 rounded-lg border border-base-200 max-h-24").Body(
					Div().Class("card-body p-4 h-full").Body(
						H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider self-center").Text(
							cfg.Agent.Label),
						P().Class("text-xs text-base-content/70 overflow-y-auto").
							Text(cfg.Agent.Intro),
					),
				),

				a.renderStories(),
				a.renderActions(),
			),
		),

		// </HTML>

	}[0]
}

func (a *AgentUI) renderStories() UI {
	storiesDivs := make([]UI, 0, len(a.data.Stories))
	for i, story := range a.data.Stories {
		// conditions
		badge := Div().Class("text-xs")

		if !story.DeactivatedAt.IsZero() {
			badge.Text(fmt.Sprintf("%.0fm ago for t%d",
				time.Since(story.DeactivatedAt).Minutes(), story.LastActiveTicks))
		}
		class := "opacity-60"
		if a.agent.Is1(story.State) {
			class = "bg-success/10 p-2 rounded-lg border border-success/30"
			badge = Div().Class("badge badge-success badge-sm animate-pulse").Text("Active")
		}

		storiesDivs = append(storiesDivs, []UI{

			// <HTML>

			Div().Class(class).Body(
				Div().Class("flex justify-between items-center mb-1").Body(
					Span().Class("font-semibold text-sm").Text(fmt.Sprintf("%d. %s", i+1, story.Title)),
					badge,
				),
				P().Class("text-xs pl-4 border-l-2 border-base-300").Text(story.Desc),
			),

			// </HTML>
		}...)
	}

	return []UI{

		// <HTML>

		Div().Class("card bg-base-100 rounded-lg flex-1 overflow-hidden border border-base-200").Body(
			Div().Class("card-body p-4 flex flex-col h-full").Body(
				H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider mb-2 self-center").Text(
					"Stories"),
				Div().Class("flex-1 overflow-y-auto space-y-3 pr-2").Body(
					storiesDivs...,
				),
			),
		),

		// </HTML>

	}[0]
}

func (a *AgentUI) renderActions() UI {
	rows := make([]UI, len(a.data.Actions))
	for i, action := range a.data.Actions {
		if !action.VisibleMem || !action.VisibleAgent {
			continue
		}
		enabled := !action.IsDisabled

		if action.ValueEnd > 0 {
			rows[i] = a.renderProgress(action, enabled)
		} else {
			rows[i] = a.renderButton(action, enabled)
		}
	}

	return []UI{

		// <HTML>

		Div().Class("card bg-base-100 flex-1 rounded-lg border border-base-200 overflow-y-auto").Body(
			Div().Class("card-body p-4").Body(
				rows...,
			),
		),

		// </HTML>

	}[0]
}

func (a *AgentUI) renderButton(action shared.ActionInfo, enabled bool) UI {
	button := Button().Class("btn rounded-lg btn-outline btn-sm w-full text-base-content/60").Text(
		action.Label)

	if a.buttonClicked == action.ID {
		button.Class("btn-secondary")
	}

	if action.Action && enabled {
		button.OnClick(func(ctx Context, e Event) {
			a.agent.Add1(ssA.StoryAction, PassRpcBase(&ABase{
				ID: action.ID,
			}))
			a.buttonClicked = action.ID
			go func() {
				time.Sleep(time.Millisecond * 500)
				if a.buttonClicked == action.ID {
					a.buttonClicked = ""
				}
			}()
		})
	}

	if !enabled {
		button.Disabled(true)
	}

	return button
}

func (a *AgentUI) renderProgress(action shared.ActionInfo, enabled bool) UI {
	return []UI{

		// <HTML>

		Div().Body(
			Div().Class("flex justify-between text-xs text-base-content/70").Body(
				Span().Text(action.Label),
				Span().Text(strconv.Itoa(action.Value)+" / "+strconv.Itoa(action.ValueEnd)),
			),
			Progress().Class("progress progress-primary w-full h-3").Value(action.Value).Max(action.ValueEnd),
		),

		// </HTML>

	}[0]
}

func (a *AgentUI) renderPromptForm() UI {
	agent := a.agentClient.NetMach

	// init
	textarea := Textarea()
	btnSend := Button()
	btnInter := Button()

	ret := []UI{

		// <HTML>

		Form().Class("mt-2 flex flex-col gap-2").Body(
			textarea.Class("textarea w-full shadow-sm grow rounded-lg").
				OnKeyDown(a.promptCtrlEnter).
				OnInput(a.ValueTo(&a.formPrompt)).
				Placeholder("Type your message here...").Text(a.formPrompt),
			btnSend.Class("btn btn-primary w-full flex-none rounded-lg").Text("Send"),
			btnInter.Class("btn btn-error btn-outline w-full flex-none rounded-lg").OnClick(a.clickInterrupt).Text(
				"Interrupt"),
		).
			OnSubmit(a.promptSubmit),

		// </HTML>

	}[0]

	// conditions
	if a.formErr != "" {
		textarea.Class("validator")
	}
	if a.formSubmitting {
		textarea.Disabled(true)
	}
	if agent.Is1(ssA.Interrupted) {
		btnSend.Disabled(true)
		btnInter.Text("Resume")
		textarea.Disabled(true)
	}
	if agent.Is1(ssA.Requesting) {
		btnSend.Body(
			Span().Class("loading loading-spinner loading-md"),
			Span().Text(" Sending "),
			Span().Class("loading loading-spinner loading-md"),
		)
	}

	return ret
}

func (a *AgentUI) renderMsgs() UI {
	rows := make([]UI, len(a.data.Msgs))
	for i, m := range a.data.Msgs {

		// init TODO sanitize
		txt := m.Text
		// TODO format HTML
		txt = strings.ReplaceAll(txt, "\n", "<br/>")
		divFrom := Div()
		divText := Div()

		// msg template
		el := []HTMLDiv{

			// <HTML>

			Div().Class("chat").Body(
				divFrom.Class("chat-header opacity-50 text-xs mb-1 capitalize").Text(
					m.From.Value,
				),
				divText.Class("chat-bubble italic text-sm").Body(Raw(
					"<div>"+txt+"</div>",
				)),
			),

			// </HTML>

		}[0]

		// conditions
		switch m.From {
		case shared.FromUser:
			el.Class("chat-end")
			divText.Class("chat-bubble-secondary")
		case shared.FromNarrator:
			el.Class("chat-start")
			divText.Class("chat-bubble-neutral")
		case shared.FromAssistant:
			el.Class("chat-start")
			divText.Class("chat-bubble-primary")
		}

		rows[i] = el
	}

	return []UI{

		// <HTML>

		Div().Class("card bg-base-100 rounded-lg flex-1 overflow-hidden border border-base-200").Body(
			Div().Class("card-body p-4 flex flex-col h-full").Body(
				H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider mb-2 self-center").Text("Messages"),
				Div().ID(idMsgs).Class("flex-1 overflow-y-auto").Body(
					rows...,
				),
			),
		),

		// </HTML>

	}[0]
}

func (a *AgentUI) renderClock() UI {
	return []HTMLDiv{

		// <HTML>

		Div().Class("card bg-base-100 h-16 border border-base-200 rounded-lg items-center").Body(
			Div().Class("card-body p-2 relative h-full w-40").Body(
				Canvas().ID("clockmoji"),
			),
		),

		// </HTML>

	}[0]
}

// UI UTILS

func (a *AgentUI) scrollMsgs() {
	a.app.Dispatch(func(ctx Context) {
		Window().Call("scrollMsgs")
	})
}

func (a *AgentUI) msgsScrolled() bool {
	return Window().Call("isMsgsScrolledEnd").Bool()
}

// HANDLERS

// TODO state
func (a *AgentUI) clickInterrupt(ctx Context, e Event) {
	e.PreventDefault()
	e.StopImmediatePropagation()
	agent := a.agentClient.NetMach

	a.formSubmitting = true
	go func() {
		defer func() {
			a.formSubmitting = false
		}()

		when := agent.WhenTicks(ssA.Interrupted, 1, ctx.Context)
		agent.Toggle1(ssA.Interrupted, nil)
		err := amhelp.WaitForAll(ctx.Context, 3*time.Second, when)
		if err != nil {
			a.formErr = "timeout"
			return
		}
	}()
}

func (a *AgentUI) promptCtrlEnter(ctx Context, e Event) { // 1. Check if the pressed key is "Enter"
	isEnter := e.Get("key").String() == "Enter"
	isCtrlOrCmd := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()
	if isEnter && isCtrlOrCmd {
		a.Dump("promptCtrlEnter", nil)
		a.promptSubmit(ctx, e)
	}
}

// TODO state
func (a *AgentUI) promptSubmit(ctx Context, e Event) {
	a.Dump("promptSubmit", a.formPrompt)
	e.PreventDefault()
	agent := a.agentClient.NetMach

	// validate
	if a.formPrompt == "" {
		return
	}

	args := &ABase{
		Prompt: a.formPrompt,
	}

	a.formSubmitting = true
	go func() {
		defer func() {
			a.formSubmitting = false
		}()

		when := agent.WhenTicks(ssA.Prompt, 1, ctx.Context)
		agent.Add1(ssA.Prompt, PassRpcBase(args))
		err := amhelp.WaitForAll(ctx.Context, 3*time.Second, when)
		if err != nil {
			a.Dump("promptSubmit/timeout", nil)
			a.formErr = "timeout"
			return
		}
		a.formPrompt = " "
		a.scrollMsgs()
	}()
}
