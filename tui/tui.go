package tui

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/terminfo"
	"github.com/gliderlabs/ssh"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	"github.com/pancsta/cview"

	baseschema "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui/states"
)

type Tui struct {
	MachTUI *am.Machine

	chat    *Chat
	clock   *Clock
	stories *Stories

	agent        *am.Machine
	logger       *slog.Logger
	app          *cview.Application
	layout       *cview.Grid
	focusManager *cview.FocusManager
	dispose      func() error
	clockStates  am.S
	cfg          *shared.Config
}

// theme

var (
	themeButtonBg         = tcell.ColorBlue
	themeButtonBgClicked  = tcell.ColorGreen
	themeButtonBgDisabled = tcell.ColorDarkGray
)

func NewTui(mach *am.Machine, logger *slog.Logger, config *shared.Config) *Tui {

	c := &Tui{
		agent:  mach,
		logger: logger,
		app:    cview.NewApplication(),
		cfg:    config,
	}

	return c
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (t *Tui) Init(
	screen tcell.Screen, name string, stories *Stories, clock *Clock, chat *Chat,
) error {

	t.stories = stories
	t.clock = clock
	t.chat = chat

	id := "tui-" + t.agent.Id() + "-" + name
	machTUI, err := am.NewCommon(t.agent.NewStateCtx(ss.UIMode), id, states.TUISchema, ssT.Names(), nil, t.agent, nil)
	if err != nil {
		return err
	}
	machTUI.SetGroups(states.TUIGroups, states.TUIStates)
	shared.MachTelemetry(machTUI, nil)
	t.MachTUI = machTUI
	mach := t.Mach()
	if t.cfg.Debug.REPL {
		opts := arpc.ReplOpts{
			AddrDir:    t.cfg.Agent.Dir,
			ArgsPrefix: shared.APrefix,
			Args:       shared.ARpc{},
		}
		if err := arpc.MachRepl(mach, "", &opts); err != nil {
			return err
		}
	}

	// TODO read from groups and schema org
	trackedStates := mach.StateNames()
	lastState := slices.Index(trackedStates, baseschema.AgentBaseStates.UICleanOutput)
	trackedStates = trackedStates[0 : lastState+1]

	if err := t.stories.Init(); err != nil {
		return err
	}
	if err := t.clock.Init(); err != nil {
		return err
	}
	if err := t.chat.Init(); err != nil {
		return err
	}
	t.InitComponents()

	// WASM or test screen
	if screen != nil {
		screen.EnableMouse(tcell.MouseMotionEvents)
		// TODO enable paste?
		t.app.SetScreen(screen)
	}

	return nil
}

func (t *Tui) InitComponents() {
	leftColumn := cview.NewFlex()
	leftColumn.SetDirection(cview.FlexRow)
	leftColumn.AddItem(t.clock.layout, 5, 1, false)
	leftColumn.AddItem(t.chat.layout, 0, 1, true)

	t.layout = cview.NewGrid()
	t.layout.SetBackgroundTransparent(false)
	t.layout.AddItem(leftColumn,
		0, 0, 1, 1, 0, 0, false)
	t.layout.AddItem(t.stories.layout,
		0, 1, 1, 1, 0, 0, false)

	t.app = cview.NewApplication()
	t.app.SetRoot(t.layout, true)
	t.app.EnableMouse(true)

	// tab navigation TODO tab manager
	// focusable := []cview.Primitive{t.msgsView, t.prompt, t.buttonSend, t.buttonIntt}
	// // TODO reuse across cviews
	// t.layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
	// 	if event.Key() == tcell.KeyTab {
	// 		cycleFocus(t.app, focusable, false)
	// 		return nil
	// 	} else if event.Key() == tcell.KeyBacktab {
	// 		cycleFocus(t.app, focusable, true)
	// 		return nil
	// 	}
	//
	// 	// data
	// 	t.msgsView.SetText(t.renderMsgs())
	// 	t.Redraw()
	//
	// 	return event
	// })

	// catch ctrl+c
	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			_ = t.Stop()
			return nil
		}

		return event
	})
}

func (t *Tui) Stop() error {
	_ = t.dispose()
	t.app.Stop()

	return nil
}

func (t *Tui) Redraw() {
	t.app.SetAfterDrawFunc(func(screen tcell.Screen) {
		t.clock.Redraw()
	})
	go t.app.QueueUpdateDraw(func() {})
}

// Start starts the UI and optionally returns the error and mutates with UIErr.
func (t *Tui) Start(dispose func() error) error {
	t.dispose = dispose

	// start the UI
	t.MachTUI.Add(S{ssT.Start, ssT.Ready}, nil)
	go t.agent.Add1(ss.UIReady, nil)

	// block on UI loop
	err := t.app.Run()
	if err != nil && err.Error() != "EOF" {
		t.agent.AddErrState(ss.ErrUI, err, nil)
	}

	return err
}

func (t *Tui) Logger() *slog.Logger {
	return t.logger
}

// Mach returns the agent machine.
func (t *Tui) Mach() *am.Machine {
	return t.agent
}

// BindHandlers binds transition handlers to the state machine. Overwrite it to bind methods from a subclass.
func (t *Tui) BindHandlers() error {
	return t.Mach().BindHandlers(t)
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

// ///// ///// /////

// ///// FOCUS

// ///// ///// /////

// TODO cview.FocusManager
// func cycleFocus(app *cview.Application, elements []cview.Primitive, reverse bool) {
// 	for i, el := range elements {
// 		if !el.HasFocus() {
// 			continue
// 		}
//
// 		if reverse {
// 			i = i - 1
// 			if i < 0 {
// 				i = len(elements) - 1
// 			}
// 		} else {
// 			i = i + 1
// 			i = i % len(elements)
// 		}
//
// 		app.SetFocus(elements[i])
// 		return
// 	}
// }

// func wrap(f func()) func(ev *tcell.EventKey) *tcell.EventKey {
// 	return func(ev *tcell.EventKey) *tcell.EventKey {
// 		f()
// 		return nil
// 	}
// }
