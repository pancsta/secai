package tui

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"
	"github.com/gliderlabs/ssh"
	"github.com/navidys/tvxwidgets"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/rivo/tview"

	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui/states"
)

// aliases

type A = shared.A
type S = am.S

var ParseArgs = shared.ParseArgs
var Pass = shared.Pass

var ss = baseschema.AgentStates
var ssChat = states.UIChatStates
var placeholder = "Enter text here..."

type Chat struct {
	agent  *am.Machine
	logger *slog.Logger
	app    *tview.Application
	msgs   []*shared.Msg

	msgsView   *tview.TextView
	buttonSend *tview.Button
	layout     *tview.Flex
	prompt     *tview.TextArea
	hist       *amhist.Memory
	clockView  *tvxwidgets.Plot
	buttonIntt *tview.Button
	dispose    func() error
	mach       *am.Machine
}

var _ shared.UI = &Chat{}

func NewChat(mach *am.Machine, logger *slog.Logger, msgs []*shared.Msg) *Chat {

	c := &Chat{
		agent:  mach,
		logger: logger,
		app:    tview.NewApplication(),
		msgs:   msgs,
	}

	return c
}

// ///// ///// /////

// ///// HANDLERS (AGENT)

// ///// ///// /////

func (c *Chat) UICleanOutputState(e *am.Event) {
	c.agent.Remove1(ss.UICleanOutput, nil)
	c.msgs = nil
	go c.app.QueueUpdateDraw(func() {
		c.msgsView.SetText("")
	})
}

func (c *Chat) UIButtonSendState(e *am.Event) {
	c.agent.EvAdd1(e, ss.Prompt, Pass(&A{Prompt: c.prompt.GetText()}))
	c.prompt.SetText("", false)
	c.Redraw()
}

func (c *Chat) UIButtonInttState(e *am.Event) {
	c.agent.EvRemove1(e, ss.UIButtonIntt, nil)

	if c.agent.Is1(ss.Interrupted) {
		c.agent.EvAdd1(e, ss.Resume, nil)
	} else {
		c.agent.EvAdd1(e, ss.Interrupted, nil)
	}
	c.Redraw()
}

func (c *Chat) InputBlockedEnd(e *am.Event) {
	c.prompt.SetDisabled(true)
	c.prompt.SetPlaceholder("")
	c.Redraw()
}

func (c *Chat) InputBlockedState(e *am.Event) {
	c.prompt.SetDisabled(false)
	c.prompt.SetPlaceholder(placeholder)
	c.Redraw()
}

func (c *Chat) RequestingState(e *am.Event) {
	ctx := c.Mach().NewStateCtx(ss.Requesting)

	// progress indicator
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		c.app.QueueUpdateDraw(func() {
			c.prompt.SetTitle("requesting")
		})

		t := time.NewTicker(1 * time.Second)
		dots := ""
		for {
			select {

			case <-ctx.Done():
				t.Stop()
				return // expired

			case <-t.C:
				dots += "."
				if len(dots) > 3 {
					dots = ""
				}
				c.app.QueueUpdateDraw(func() {
					c.prompt.SetTitle(dots + "requesting" + dots)
				})
			}
		}
	}()
}

func (c *Chat) RequestingEnd(e *am.Event) {
	go c.app.QueueUpdateDraw(func() {
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

	go c.app.QueueUpdateDraw(func() {
		c.msgsView.SetText(text)
		c.msgsView.ScrollToEnd()
	})
}

func (c *Chat) InterruptedState(e *am.Event) {
	c.buttonIntt.SetLabel("Resume")
	c.Redraw()
}

func (c *Chat) InterruptedEnd(e *am.Event) {
	c.buttonIntt.SetLabel("Interrupt")
	c.Redraw()
}

func (c *Chat) PromptState(e *am.Event) {
	// set the ignored prompt back into the UI
	if c.Mach().Is1(ss.Interrupted) {
		c.prompt.SetText(ParseArgs(e.Args).Prompt, false)
		return
	}
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (c *Chat) Init(sub shared.UI, screen tcell.Screen, name string) error {

	id := "tui-chat-" + c.agent.Id() + "-" + name
	uiMach, err := am.NewCommon(c.agent.NewStateCtx(ss.UIMode), id, states.UIChatSchema, ssChat.Names(), nil, c.agent, nil)
	if err != nil {
		return err
	}
	uiMach.SetGroups(states.UIChatGroups, states.UIChatStates)
	shared.MachTelemetry(uiMach, nil)
	c.mach = uiMach
	mach := c.Mach()

	// TODO cut better
	trackedStates := mach.StateNames()
	lastState := slices.Index(trackedStates, baseschema.AgentStates.UICleanOutput)
	trackedStates = trackedStates[0 : lastState+1]

	c.InitComponents()
	if screen != nil {
		screen.EnableMouse(tcell.MouseMotionEvents)
		// TODO enable paste?
		c.app.SetScreen(screen)
	}

	return sub.BindHandlers()
}

func (c *Chat) Logger() *slog.Logger {
	return c.logger
}

func (c *Chat) Mach() *am.Machine {
	return c.agent
}

func (c *Chat) UIMach() *am.Machine {
	return c.mach
}

// BindHandlers binds transition handlers to the state machine. Overwrite it to bind methods from a subclass.
func (c *Chat) BindHandlers() error {
	return c.Mach().BindHandlers(c)
}

func (c *Chat) InitComponents() {

	// messages
	c.msgsView = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true).
		ScrollToEnd().
		SetText(c.renderMsgs())
	c.msgsView.SetTitle("Messages").SetBorder(true)

	// input
	c.prompt = tview.NewTextArea().
		SetWrap(false).
		SetPlaceholder(placeholder)
	c.prompt.SetTitle("Prompt").SetBorder(true).
		SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			switch event.Key() {

			// submit TODO UI state
			case tcell.KeyEnter:
				res := c.agent.Add1(ss.Prompt, Pass(&A{
					Prompt: c.prompt.GetText(),
				}))
				if res == am.Canceled {
					return nil
				}
				c.prompt.SetText("", false)
				c.Redraw()

				return nil
			}

			// accept key
			return event
		})

	c.buttonSend = tview.NewButton("Send Message").
		SetSelectedFunc(func() {
			c.agent.Add1(ss.UIButtonSend, nil)
		})
	c.buttonSend.SetBorder(true)

	c.buttonIntt = tview.NewButton("Interrupt").
		SetSelectedFunc(func() {
			c.agent.Add1(ss.UIButtonIntt, nil)
		})
	if c.Mach().Is1(ss.Interrupted) {
		c.buttonIntt.SetLabel("Resume")
	}
	c.buttonIntt.SetBorder(true)

	// LAYOUT

	c.layout = tview.NewFlex().SetDirection(tview.FlexRow)
	c.layout.
		AddItem(c.msgsView, 0, 1, false).
		AddItem(c.prompt, 5, 1, true).
		AddItem(c.buttonSend, 3, 1, false).
		AddItem(c.buttonIntt, 3, 1, false)
	c.app = tview.NewApplication().SetRoot(c.layout, true).EnableMouse(true)

	// tab navigation
	focusable := []tview.Primitive{c.msgsView, c.prompt, c.buttonSend, c.buttonIntt}
	// TODO reuse across tviews
	c.layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			cycleFocus(c.app, focusable, false)
			return nil
		} else if event.Key() == tcell.KeyBacktab {
			cycleFocus(c.app, focusable, true)
			return nil
		}

		// data
		c.msgsView.SetText(c.renderMsgs())
		c.Redraw()

		return event
	})

	// catch ctrl+c
	c.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			_ = c.Stop()
			return nil
		}

		return event
	})
}

// Start starts the UI and optionally returns the error and mutates with UIErr.
func (c *Chat) Start(dispose func() error) error {
	c.dispose = dispose
	// start the UI loop
	c.UIMach().Add(S{ssStories.Start, ssStories.Ready}, nil)
	go c.agent.Add1(ss.UIReady, nil)
	err := c.app.Run()
	if err != nil && err.Error() != "EOF" {
		c.agent.AddErrState(ss.ErrUI, err, nil)
	}

	return err
}

func (c *Chat) Stop() error {
	_ = c.dispose()
	c.app.Stop()

	return nil
}

func (c *Chat) Redraw() {
	go c.app.QueueUpdateDraw(func() {})
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

// ///// ///// /////

// ///// SSH

// ///// ///// /////

func NewSessionScreen(s ssh.Session) (tcell.Screen, error) {
	pi, ch, ok := s.Pty()
	if !ok {
		return nil, errors.New("no pty requested")
	}
	ti, err := terminfo.LookupTerminfo(pi.Term)
	if err != nil {
		return nil, err
	}

	t := &tty{
		Session: s,
		ch:      ch,
	}
	t.size.Store(&pi.Window)
	screen, err := tcell.NewTerminfoScreenFromTtyTerminfo(t, ti)
	if err != nil {
		return nil, err
	}

	return screen, nil
}

type tty struct {
	ssh.Session
	size     atomic.Pointer[ssh.Window]
	ch       <-chan ssh.Window
	resizecb func()
	mu       sync.Mutex
}

func (t *tty) Start() error {
	go func() {
		for win := range t.ch {
			t.size.Store(&win)
			t.notifyResize()
		}
	}()

	return nil
}

func (t *tty) Stop() error {
	return nil
}

func (t *tty) Drain() error {
	return nil
}

func (t *tty) WindowSize() (window tcell.WindowSize, err error) {
	return tcell.WindowSize{
		Width:  t.size.Load().Width,
		Height: t.size.Load().Height,
	}, nil
}

func (t *tty) NotifyResize(cb func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resizecb = cb
}

func (t *tty) notifyResize() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resizecb != nil {
		t.resizecb()
	}
}
