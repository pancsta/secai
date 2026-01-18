package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/navidys/tvxwidgets"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/cview"

	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui/states"
)

// aliases

type A = shared.A
type S = am.S

var ParseArgs = shared.ParseArgs
var Pass = shared.Pass

var ss = baseschema.AgentBaseStates
var ssT = states.TUIStates
var placeholder = "Enter text here..."

type Chat struct {
	msgs       []*shared.Msg
	msgsView   *cview.TextView
	layout     *cview.Flex
	buttonSend *cview.Button
	prompt     *cview.InputField
	clockView  *tvxwidgets.Plot
	buttonIntt *cview.Button
	dispose    func() error
	t          *Tui
	machChat   *am.Machine
}

func NewChat(tui *Tui, msgs []*shared.Msg) *Chat {

	c := &Chat{
		t:    tui,
		msgs: msgs,
	}

	return c
}

// ///// ///// /////

// ///// HANDLERS (AGENT)

// ///// ///// /////

func (c *Chat) UICleanOutputState(e *am.Event) {
	c.t.agent.Remove1(ss.UICleanOutput, nil)
	c.msgs = nil
	go c.t.app.QueueUpdateDraw(func() {
		c.msgsView.SetText("")
	})
}

func (c *Chat) UIButtonSendState(e *am.Event) {
	c.t.agent.EvAdd1(e, ss.Prompt, Pass(&A{Prompt: c.prompt.GetText()}))
	c.prompt.SetText("")
	c.t.Redraw()
}

func (c *Chat) UIButtonInttState(e *am.Event) {
	c.t.agent.EvRemove1(e, ss.UIButtonIntt, nil)

	if c.t.agent.Is1(ss.Interrupted) {
		c.t.agent.EvAdd1(e, ss.Resume, nil)
	} else {
		c.t.agent.EvAdd1(e, ss.Interrupted, nil)
	}
	c.t.Redraw()
}

func (c *Chat) InputBlockedEnd(e *am.Event) {
	// TODO
	// c.prompt.SetDisabled(true)
	c.prompt.SetPlaceholder("")
	c.t.Redraw()
}

func (c *Chat) InputBlockedState(e *am.Event) {
	// TODO
	// c.prompt.SetDisabled(false)
	c.prompt.SetPlaceholder(placeholder)
	c.t.Redraw()
}

func (c *Chat) RequestingState(e *am.Event) {
	ctx := c.t.Mach().NewStateCtx(ss.Requesting)

	// progress indicator
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		c.t.app.QueueUpdateDraw(func() {
			c.prompt.SetTitle("requesting")
		})

		t := time.NewTicker(1 * time.Second)
		progress := ""
		for {
			select {

			case <-ctx.Done():
				t.Stop()
				return // expired

			case <-t.C:
				progress += "+"
				c.t.app.QueueUpdateDraw(func() {
					c.prompt.SetTitle(fmt.Sprintf(" [yellow]%[1]s[-] requesting [yellow]%[1]s[-] ", progress))
				})
				if progress == "++++" {
					progress = "+"
				}
			}
		}
	}()
}

func (c *Chat) RequestingEnd(e *am.Event) {
	go c.t.app.QueueUpdateDraw(func() {
		c.prompt.SetTitle("Prompt")
	})
}

func (c *Chat) MsgEnter(e *am.Event) bool {
	m := ParseArgs(e.Args).Msg
	l := len(c.msgs)

	// skip duplicates
	return l == 0 || !(m.Text == c.msgs[l-1].Text && m.From == c.msgs[l-1].From)
}

func (c *Chat) MsgState(e *am.Event) {
	c.msgs = append(c.msgs, ParseArgs(e.Args).Msg)
	text := c.renderMsgs()

	go c.t.app.QueueUpdateDraw(func() {
		c.msgsView.SetText(text)
		c.msgsView.ScrollToEnd()
	})
}

func (c *Chat) InterruptedState(e *am.Event) {
	c.buttonIntt.SetLabel("Resume")
	c.t.Redraw()
}

func (c *Chat) InterruptedEnd(e *am.Event) {
	c.buttonIntt.SetLabel("Interrupt")
	c.t.Redraw()
}

func (c *Chat) PromptState(e *am.Event) {
	// set the ignored prompt back into the UI
	if c.t.Mach().Is1(ss.Interrupted) {
		c.prompt.SetText(ParseArgs(e.Args).Prompt)
		return
	}
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (c *Chat) Init() error {
	if err := c.t.agent.BindHandlers(c); err != nil {
		return err
	}

	// messages
	c.msgsView = cview.NewTextView()
	c.msgsView.SetDynamicColors(true)
	c.msgsView.SetRegions(true)
	c.msgsView.SetWordWrap(true)
	c.msgsView.ScrollToEnd()
	c.msgsView.SetText(c.renderMsgs())
	c.msgsView.SetTitle("Messages")
	c.msgsView.SetBorder(true)

	// input TODO port tview TextArea
	c.prompt = cview.NewInputField()
	// c.prompt.SetWrap(false)
	c.prompt.SetPlaceholder(placeholder)
	c.prompt.SetTitle("Prompt")
	c.prompt.SetFieldBackgroundColor(tcell.ColorDefault)
	c.prompt.SetFieldBackgroundColorFocused(tcell.ColorDefault)
	c.prompt.SetBorder(true)
	c.prompt.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {

		// submit TODO UI state
		case tcell.KeyEnter:
			res := c.t.agent.Add1(ss.Prompt, Pass(&A{
				Prompt: c.prompt.GetText(),
			}))
			if res == am.Canceled {
				return nil
			}
			c.prompt.SetText("")
			c.t.Redraw()

			return nil
		}

		// accept key
		return event
	})

	c.buttonSend = cview.NewButton("Send Message")
	c.buttonSend.SetBackgroundColor(themeButtonBg)
	c.buttonSend.SetSelectedFunc(func() {
		c.t.agent.Add1(ss.UIButtonSend, nil)
	})
	c.buttonSend.SetBorder(true)

	c.buttonIntt = cview.NewButton("Interrupt")
	c.buttonIntt.SetBackgroundColor(themeButtonBg)
	c.buttonIntt.
		SetSelectedFunc(func() {
			c.t.agent.Add1(ss.UIButtonIntt, nil)
		})
	if c.t.Mach().Is1(ss.Interrupted) {
		c.buttonIntt.SetLabel("Resume")
	}
	c.buttonIntt.SetBorder(true)

	// LAYOUT

	c.layout = cview.NewFlex()
	c.layout.SetDirection(cview.FlexRow)
	c.layout.AddItem(c.msgsView, 0, 1, false)
	c.layout.AddItem(c.prompt, 3, 1, true)
	c.layout.AddItem(c.buttonSend, 3, 1, false)
	c.layout.AddItem(c.buttonIntt, 3, 1, false)

	// catch ctrl+c
	c.t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			_ = c.t.Stop()
			return nil
		}

		return event
	})

	return nil
}

func (c *Chat) renderMsgs() string {
	return strings.Join(shared.Map(c.msgs, func(m *shared.Msg) string {

		// trim and reset styles
		text := strings.Trim(m.Text, " \n\t") + "[-:-:-]"

		var prefix string
		switch m.From {
		case shared.FromAssistant:
			prefix = "[::ub]Assistant[::-]: \n"
		case shared.FromSystem:
			prefix = "[::ub]System[::-]: \n"
		case shared.FromUser:
			prefix = "[::ub]You[::-]: \n"
		case shared.FromNarrator:
			prefix = "[::ub]Narrator[::-]: \n"
		}

		return prefix + text
	}), "\n\n")
}
