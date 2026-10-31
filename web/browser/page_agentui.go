package browser

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	. "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/shared"
)

const idMsgs = "msgs"

// clockWidth is the number of series rendered by the clock chart.
const clockWidth = 4

var Pass = am.Pass

func (a *AgentUI) Render() UI {
	return If(!a.Ready(), func() UI {
		return a.Spinner()
	}).Else(a.renderPage)
}

func (a *AgentUI) renderPage() UI {
	cfg := a.model.ConfigBase()

	return Div().Class("grid grid-cols-10 gap-4 h-full max-h-full").Body(

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
					H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider self-center").
						Text(cfg.Agent.Label),
					P().Class("text-xs text-base-content/70 overflow-y-auto").Body(
						Raw(introHTML(cfg.Agent.Intro)),
					),
				),
			),

			a.renderStories(),
			a.renderActions(),
		),
	)
}

func (a *AgentUI) renderStories() UI {
	stories := a.model.Stories()

	return Div().Class("card bg-base-100 rounded-lg flex-1 overflow-hidden border border-base-200").Body(
		Div().Class("card-body p-4 flex flex-col h-full").Body(
			H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider mb-2 self-center").
				Text("Stories"),
			Div().Class("flex-1 overflow-y-auto space-y-3 pr-2").Body(
				Range(stories).Slice(func(i int) UI {
					return a.renderStory(i, stories[i])
				}),
			),
		),
	)
}

func (a *AgentUI) renderStory(i int, story shared.StoryInfo) UI {
	class, active, ago := a.storyState(story)

	var badge HTMLDiv
	defer func() {
		if !active {
			badge.Text(ago)
			return
		}
		badge.Class("badge badge-success badge-sm animate-pulse").Text("Active")
	}()

	return Div().Class(class).Body(
		Div().Class("flex justify-between items-center mb-1").Body(
			Span().Class("font-semibold text-sm").Textf("%d. %s", i+1, story.Title),
			toRef(&badge, Div().Class("text-xs")),
		),
		P().Class("text-xs pl-4 border-l-2 border-base-300").Text(story.Desc),
	)
}

// storyState returns the story card class, whether it's active, and the
// deactivation label.
func (a *AgentUI) storyState(story shared.StoryInfo) (class string, active bool, ago string) {
	if a.model.Mach().Is1(story.State) {
		return "bg-success/10 p-2 rounded-lg border border-success/30", true, ""
	}
	if !story.DeactivatedAt.IsZero() {
		ago = fmt.Sprintf("%.0fm ago for t%d",
			time.Since(story.DeactivatedAt).Minutes(), story.LastActiveTicks)
	}

	return "opacity-60", false, ago
}

func (a *AgentUI) renderActions() UI {
	actions := a.model.Actions()

	return Div().Class("card bg-base-100 flex-1 rounded-lg border border-base-200 overflow-y-auto").Body(
		Div().Class("card-body p-4").Body(
			Range(actions).Slice(func(i int) UI {
				return a.renderAction(actions[i])
			}),
		),
	)
}

// renderAction returns a button or a progress bar for the action, or nil when
// it's hidden.
func (a *AgentUI) renderAction(action shared.ActionInfo) UI {
	if !action.VisibleMem || !action.VisibleAgent {
		return nil
	}

	if action.ValueEnd > 0 {
		return a.renderProgress(action)
	}

	return a.renderButton(action)
}

func (a *AgentUI) renderButton(action shared.ActionInfo) UI {
	enabled := !action.IsDisabled

	var btn HTMLButton
	defer func() {
		if action.Action && enabled {
			btn.OnClick(func(_ Context, _ Event) {
				a.clickAction(action.ID)
			})
		}
		if a.buttonClicked == action.ID {
			btn.Class("btn-secondary")
		}
		if !enabled {
			btn.Disabled(true)
		}
	}()

	return toRef(&btn, Button().
		Class("btn rounded-lg btn-outline btn-sm w-full text-base-content/60").
		Text(action.Label))
}

func (a *AgentUI) renderProgress(action shared.ActionInfo) UI {
	return Div().Body(
		Div().Class("flex justify-between text-xs text-base-content/70").Body(
			Span().Text(action.Label),
			Span().Textf("%d / %d", action.Value, action.ValueEnd),
		),
		Progress().Class("progress progress-primary w-full h-3").
			Value(action.Value).
			Max(action.ValueEnd),
	)
}

func (a *AgentUI) renderPromptForm() UI {
	mach := a.model.Mach()

	var (
		textarea HTMLTextarea
		btnSend  HTMLButton
		btnInter HTMLButton
	)
	defer func() {
		if mach.Is1(ssA.Interrupted) {
			btnSend.Disabled(true)
			btnInter.Text("Resume")
			textarea.Disabled(true)
		}
		if mach.Is1(ssA.Requesting) {
			btnSend.Body(
				Span().Class("loading loading-spinner loading-md"),
				Span().Text(" Sending "),
				Span().Class("loading loading-spinner loading-md"),
			)
		}
	}()

	return Form().Class("mt-2 flex flex-col gap-2").OnSubmit(a.promptSubmit).Body(

		// the prompt value lives in the DOM only, so it's reset via data-reset
		toRef(&textarea, Textarea().
			Class("textarea w-full shadow-sm grow rounded-lg").
			DataSet("reset", strconv.Itoa(a.promptReset)).
			Placeholder("Type your message here...").
			OnInput(func(_ Context, e Event) {
				a.formPrompt = e.Data["value"]
			})),

		toRef(&btnSend, Button().
			Class("btn btn-primary w-full flex-none rounded-lg").
			Text("Send")),

		toRef(&btnInter, Button().
			Class("btn btn-error btn-outline w-full flex-none rounded-lg").
			Text("Interrupt").
			OnClick(a.interrupt)),
	)
}

func (a *AgentUI) renderMsgs() UI {
	msgs := a.model.Msgs()

	return Div().Class("card bg-base-100 rounded-lg flex-1 overflow-hidden border border-base-200").Body(
		Div().Class("card-body p-4 flex flex-col h-full").Body(
			H2().Class("card-title text-sm text-base-content/50 uppercase tracking-wider mb-2 self-center").
				Text("Messages"),
			Div().ID(idMsgs).Class("flex-1 overflow-y-auto").Body(
				Range(msgs).Slice(func(i int) UI {
					return a.renderMsg(msgs[i])
				}),
			),
		),
	)
}

func (a *AgentUI) renderMsg(m *shared.Msg) UI {
	// TODO sanitize
	txt := strings.ReplaceAll(m.Text, "\n", "<br/>")
	align, bubble := msgState(m.From)

	var header HTMLDiv
	defer func() {
		// TODO day on old dates
		date := Time().Class("text-xs opacity-50").Text(m.CreatedAt.Format(time.Kitchen))
		from := Text(m.From.Value)
		if m.From == shared.FromUser {
			header.Body(date, from)
			return
		}
		header.Body(from, date)
	}()

	return Div().Class("chat "+align).Body(
		toRef(&header, Div().Class("chat-header text-xs mb-1 capitalize")),
		Div().Class("chat-bubble italic text-sm overflow-x-scroll "+bubble).Body(
			// TODO format HTML
			Raw("<div>"+txt+"</div>"),
		),
	)
}

// msgState returns the row alignment and the bubble class for a message.
func msgState(from shared.From) (align, bubble string) {
	switch from {
	case shared.FromUser:
		return "chat-end", "chat-bubble-secondary"
	case shared.FromNarrator:
		return "chat-start", "chat-bubble-neutral"
	case shared.FromAssistant:
		return "chat-start", "chat-bubble-primary"
	}

	return "chat-start", ""
}

// renderClock renders the clock canvas. The chart itself is drawn by chart.js
// from the server-rendered diff in the data-diff attribute.
func (a *AgentUI) renderClock() UI {
	return Div().Class("card bg-base-100 h-16 border border-base-200 rounded-lg items-center").Body(
		Div().Class("card-body p-2 relative h-full w-40").Body(
			Canvas().ID("clockmoji").DataSet("diff", a.clockDiffJSON()),
		),
	)
}

// clockDiffJSON returns the clock diff as JSON, padded to the chart series.
func (a *AgentUI) clockDiffJSON() string {
	diff, _ := json.Marshal(a.clockDiff())
	return string(diff)
}

// clockDiff returns the clock diff padded to the number of chart series.
func (a *AgentUI) clockDiff() [][]int {
	diff := a.model.Store().ClockDiff
	ret := make([][]int, clockWidth)
	for i := range ret {
		ret[i] = []int{}
		if i < len(diff) {
			ret[i] = diff[i]
		}
	}

	return ret
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (a *AgentUI) promptSubmit(_ Context, _ Event) {
	if a.formPrompt == "" {
		return
	}

	a.model.Mach().Add1(ssA.Prompt, Pass(&shared.APrompt{
		Prompt: a.formPrompt,
	}))
	a.formPrompt = ""
	a.promptReset++
}

func (a *AgentUI) interrupt(_ Context, _ Event) {
	a.model.Mach().Toggle1(ssA.Interrupted, nil)
}

func (a *AgentUI) clickAction(id string) {
	mach := a.model.Mach()
	mach.Add1(ssA.StoryAction, Pass(&shared.AStoryAction{
		ID: id,
	}))
	a.buttonClicked = id

	// reset the button state on the queue
	time.AfterFunc(500*time.Millisecond, func() {
		mach.Eval("browser-agentui-reset", func() {
			if a.buttonClicked != id {
				return
			}
			a.buttonClicked = ""
			RefreshPage(a.Page)
		}, mach.Context())
	})
}

// introHTML converts a plain text intro to HTML.
func introHTML(intro string) string {
	return "<span>" + strings.ReplaceAll(intro, "\n", "<br>") + "</span>"
}
