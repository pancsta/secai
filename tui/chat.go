package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/navidys/tvxwidgets"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	"github.com/rivo/tview"

	"github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
)

var ss = schema.AgentStates
var placeholder = "Enter text here..."

const clockHistSize = 5

type Chat struct {
	mach         *am.Machine
	app          *tview.Application
	msgs         []Msg
	msgsView     *tview.TextView
	button       *tview.Button
	layout       *tview.Flex
	prompt       *tview.TextArea
	hist         *amhist.History
	clockView    *tvxwidgets.Plot
	clockHeight  int
	clockEnabled bool
}

type Msg struct {
	From      shared.From
	Content   string
	CreatedAt time.Time
}

func NewChat(mach *am.Machine, sysMsgs []string, enableClockmoji bool) (*Chat, error) {

	c := &Chat{
		mach: mach,
		app:  tview.NewApplication(),
		msgs: shared.Map(sysMsgs, func(m string) Msg {
			return Msg{
				From:      shared.FromSystem,
				Content:   m,
				CreatedAt: time.Now(),
			}
		}),
		clockHeight:  4,
		clockEnabled: enableClockmoji,
	}

	// TODO cut better
	trackedStates := mach.StateNames()
	trackedStates = trackedStates[0:slices.Index(trackedStates, "UIErr")]

	c.hist = amhist.Track(mach, trackedStates, clockHistSize)
	c.initUI()

	// bind UI states
	err := mach.BindHandlers(c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (c *Chat) UIButtonPressState(e *am.Event) {
	c.mach.Remove1(ss.UIButtonPress, nil)

	if c.mach.Is1(ss.Requesting) {
		c.mach.EvAdd1(e, ss.Interrupt, nil)
		return
	}

	c.mach.EvAdd1(e, ss.Prompt, am.A{"prompt": c.prompt.GetText()})
	c.prompt.SetText("", false)
	c.redraw()
}

func (c *Chat) InputPendingEnd(e *am.Event) {
	c.prompt.SetDisabled(true)
	c.prompt.SetPlaceholder("")
	c.redraw()
}

func (c *Chat) InputPendingState(e *am.Event) {
	c.prompt.SetDisabled(false)
	c.prompt.SetPlaceholder(placeholder)
	c.redraw()
}

func (c *Chat) UIReadyState(e *am.Event) {
	c.mach.Add1(ss.Ready, nil)
}

func (c *Chat) AnsweredState(e *am.Event) {
	c.redraw()
}

func (c *Chat) PromptEnter(e *am.Event) bool {
	_, ok := e.Args["prompt"].(string)
	return ok
}

func (c *Chat) PromptState(e *am.Event) {
	c.AddMsg(e.Args["prompt"].(string), shared.FromUser)
}

func (c *Chat) RequestingState(e *am.Event) {
	c.prompt.SetTitle("Requesting...")
	c.button.SetLabel("STOP")
	c.redraw()
}

func (c *Chat) RequestingEnd(e *am.Event) {
	c.prompt.SetTitle("Prompt")
	c.button.SetLabel("Send Message")
	c.redraw()
}

func (c *Chat) AnyState(e *am.Event) {
	c.updateClock()
	// TODO optimize
	c.redraw()
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (c *Chat) updateClock() {
	if !c.clockEnabled {
		return
	}

	ticks := make(am.Time, len(c.hist.States))
	for _, e := range c.hist.Entries {
		ticks = ticks.Add(e.MTimeDiff[0:len(c.hist.States)])
	}

	var maxC uint64
	for _, c := range ticks {
		if c > maxC {
			maxC = c
		}
	}
	div := float64(c.clockHeight) / float64(maxC)
	plot := [][]float64{make([]float64, len(ticks))}

	for i, t := range ticks {
		plot[0][i] = float64(t) / div
	}

	c.clockView.SetData(plot)
}

func (c *Chat) initUI() {

	// clock
	var clockLayout *tview.Flex
	if c.clockEnabled {
		c.clockView = tvxwidgets.NewPlot()
		c.clockView.SetLineColor([]tcell.Color{
			tcell.ColorGold,
			tcell.ColorLightSkyBlue,
		})
		c.clockView.SetDrawXAxisLabel(false)
		c.clockView.SetDrawYAxisLabel(false)
		c.clockView.SetDrawAxes(false)
		clockCols := len(c.hist.States)
		c.clockView.SetData([][]float64{make([]float64, clockCols)})
		c.clockView.SetMarker(tvxwidgets.PlotMarkerBraille)
		// TODO fix empty line between clock and msgs

		clockLayout = tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(c.clockView, clockCols, 1, false).
			AddItem(tview.NewBox(), 0, 1, false)
	}

	// messages
	c.msgsView = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			c.redraw()
		}).
		SetText(c.renderMsgs())
	c.msgsView.SetTitle("Messages").SetBorder(true)

	// input
	c.prompt = tview.NewTextArea().
		SetWrap(false).
		SetPlaceholder(placeholder)
	c.prompt.SetTitle("Prompt").SetBorder(true).
		SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			switch event.Key() {

			// submit TODO state
			case tcell.KeyEnter:
				res := c.mach.Add1(ss.Prompt, am.A{"prompt": c.prompt.GetText()})
				if res == am.Canceled {
					return nil
				}
				c.prompt.SetText("", false)
				c.redraw()

				return nil
			}

			// accept key
			return event
		})

	c.button = tview.NewButton("Send Message").
		SetSelectedFunc(func() {
			c.mach.Add1(ss.UIButtonPress, nil)
		})

	// LAYOUT

	c.layout = tview.NewFlex().SetDirection(tview.FlexRow)
	if c.clockEnabled {
		c.layout.AddItem(clockLayout, c.clockHeight, 1, false)
	}
	c.layout.
		AddItem(c.msgsView, 0, 1, false).
		AddItem(c.prompt, 5, 1, true).
		AddItem(c.button, 3, 1, false)
	c.app = tview.NewApplication().SetRoot(c.layout, true).EnableMouse(true)

	// tab navigation
	focusable := []tview.Primitive{c.msgsView, c.prompt, c.button}
	c.layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			cycleFocus(c.app, focusable, false)
			return nil
		} else if event.Key() == tcell.KeyBacktab {
			cycleFocus(c.app, focusable, true)
			return nil
		}

		return event
	})
}

func (c *Chat) Run() error {
	// start the UI loop
	go c.mach.Add1(ss.UIReady, nil)
	err := c.app.Run()
	if err != nil {
		c.mach.AddErrState(ss.UIErr, err, nil)
	}
	c.mach.Add1(ssam.DisposedStates.Disposing, nil)

	return err
}

func (c *Chat) AddMsg(msg string, from shared.From) {
	c.msgs = append(c.msgs, Msg{
		From: from,
		// trim and reset styles
		Content:   strings.Trim(msg, " \n\t") + "[-:-:-]",
		CreatedAt: time.Now(),
	})
	c.msgsView.SetText(c.renderMsgs())
	c.msgsView.ScrollToEnd()
	c.redraw()
}

func (c *Chat) redraw() {
	go c.app.QueueUpdateDraw(func() {})
}

func (c *Chat) renderMsgs() string {
	return strings.Join(shared.Map(c.msgs, func(m Msg) string {
		var prefix string
		switch m.From {
		case shared.FromAssistant:
			prefix = "[::ub]Assistant[::-]: \n"
		case shared.FromSystem:
			prefix = "[::ub]System[::-]: \n"
		case shared.FromUser:
			prefix = "[::ub]You[::-]: \n"
		}

		return prefix + m.Content
	}), "\n\n")
}

func cycleFocus(app *tview.Application, elements []tview.Primitive, reverse bool) {
	for i, el := range elements {
		if !el.HasFocus() {
			continue
		}

		if reverse {
			i = i - 1
			if i < 0 {
				i = len(elements) - 1
			}
		} else {
			i = i + 1
			i = i % len(elements)
		}

		app.SetFocus(elements[i])
		return
	}
}
