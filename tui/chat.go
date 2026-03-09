package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/navidys/tvxwidgets"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/cview"
	"github.com/pancsta/tcell-v2"

	"github.com/pancsta/secai/shared"
)

var placeholder = "Enter text here..."

// TODO merge into TUI
type Chat struct {
	msgs      []*shared.Msg
	msgsView  *cview.TextView
	layout    *cview.Flex
	butSend   *cview.Button
	butInter  *cview.Button
	prompt    *cview.InputField
	clockView *tvxwidgets.Plot
	dispose   func() error
	t         *TUI
	machChat  *am.Machine
}

func NewChat(tui *TUI, msgs []*shared.Msg) *Chat {

	c := &Chat{
		t:    tui,
		msgs: msgs,
	}

	return c
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (c *Chat) UICleanOutputState(e *am.Event) {
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

func (c *Chat) UIButtonInterState(e *am.Event) {
	c.t.agent.EvRemove1(e, ss.UIButtonInter, nil)

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

// RequestingState with progress indicator
func (c *Chat) RequestingState(e *am.Event) {
	ctx := c.t.agent.NewStateCtx(ss.Requesting)

	c.machChat.Fork(ctx, e, func() {
		c.requestingProgress(ctx)
	})
}

func (c *Chat) RequestingEnd(e *am.Event) {
	go c.t.app.QueueUpdateDraw(func() {
		c.prompt.SetTitle("Prompt")
	})
}

func (c *Chat) UIMsgEnter(e *am.Event) bool {
	m := ParseArgs(e.Args).Msg
	l := len(c.msgs)

	// skip duplicates
	return l == 0 || !(m.Text == c.msgs[l-1].Text && m.From == c.msgs[l-1].From)
}

func (c *Chat) UIMsgState(e *am.Event) {
	c.msgs = append(c.msgs, ParseArgs(e.Args).Msg)
	text := c.renderMsgs()

	go c.t.app.QueueUpdateDraw(func() {
		c.msgsView.SetText(text)
		c.msgsView.ScrollToEnd()
	})
}

func (c *Chat) InterruptedState(e *am.Event) {
	c.butInter.SetLabel("Resume")
	c.t.Redraw()
}

func (c *Chat) InterruptedEnd(e *am.Event) {
	c.butInter.SetLabel("Interrupt")
	c.t.Redraw()
}

func (c *Chat) PromptState(e *am.Event) {
	// set the ignored prompt back into the UI
	if c.t.agent.Is1(ss.Interrupted) {
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
	c.msgsView.ScrollToEnd()
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

	c.butSend = cview.NewButton("Send Message")
	c.butSend.SetBackgroundColor(themeButtonBg)
	c.butSend.SetBackgroundColorFocused(themeButtonBg)
	c.butSend.SetSelectedFunc(func() {
		c.t.agent.Add1(ss.UIButtonSend, nil)
		c.butSend.SetBackgroundColor(themeButtonBgClicked)
		c.butSend.SetBackgroundColorFocused(themeButtonBgClicked)
		c.t.Redraw()

		// unpressed TODO terrible
		go func() {
			time.Sleep(clickDelay)
			c.butSend.SetBackgroundColor(themeButtonBg)
			c.butSend.SetBackgroundColorFocused(themeButtonBg)
			c.t.Redraw()
		}()
	})
	c.butSend.SetBorder(true)

	c.butInter = cview.NewButton("Interrupt")
	c.butInter.SetBackgroundColor(themeButtonBg)
	c.butInter.SetBackgroundColorFocused(themeButtonBg)
	c.butInter.SetSelectedFunc(func() {
		c.t.agent.Add1(ss.UIButtonInter, nil)
		c.butInter.SetBackgroundColor(themeButtonBgClicked)
		c.butInter.SetBackgroundColorFocused(themeButtonBgClicked)
		c.t.Redraw()

		// unpressed TODO terrible
		go func() {
			time.Sleep(clickDelay)
			c.butInter.SetBackgroundColor(themeButtonBg)
			c.butInter.SetBackgroundColorFocused(themeButtonBg)
			c.t.Redraw()
		}()
	})
	if c.t.agent.Is1(ss.Interrupted) {
		c.butInter.SetLabel("Resume")
	}
	c.butInter.SetBorder(true)

	// LAYOUT

	c.layout = cview.NewFlex()
	c.layout.SetDirection(cview.FlexRow)
	c.layout.AddItem(c.msgsView, 0, 1, false)
	c.layout.AddItem(c.prompt, 3, 1, true)
	c.layout.AddItem(c.butSend, 3, 1, false)
	c.layout.AddItem(c.butInter, 3, 1, false)

	// catch ctrl+c
	c.t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			_ = c.t.Stop()
			return nil
		}

		return event
	})

	// sync
	if c.t.agent.Is1(ss.Requesting) {
		go c.requestingProgress(c.t.agent.NewStateCtx(ss.Requesting))
	}

	return nil
}

func (c *Chat) requestingProgress(ctx context.Context) {
	c.t.app.QueueUpdateDraw(func() {
		c.prompt.SetTitle("   requesting   ")
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
