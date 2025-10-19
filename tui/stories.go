package tui

import (
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"code.rocketnine.space/tslocum/cbind"
	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/cview"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui/states"
)

var ssStories = states.UIStoriesStates

const clickDelay = time.Second

type Stories struct {
	agent          *am.Machine
	logger         *slog.Logger
	app            *cview.Application
	storiesList    *cview.TextView
	layout         *cview.Flex
	buttonsView    *cview.ScrollView
	dispose        func() error
	mach           *am.Machine
	replacePending bool
	buttons        []shared.StoryButton
	stories        []shared.StoryInfo
	focusManager   *cview.FocusManager
	handlers       *StoriesHandlers
	header         *cview.TextView
}

var _ shared.UI = &Stories{}

// NewStories returns a new TUI dedicated to showing stories and their progress (as buttons).
func NewStories(
	mach *am.Machine, logger *slog.Logger, buttons []shared.StoryButton, stories []shared.StoryInfo,
) *Stories {
	return &Stories{
		agent:   mach,
		logger:  logger,
		buttons: buttons,
		stories: stories,
	}
}

// ///// ///// /////

// ///// HANDLERS (STORIES)

// ///// ///// /////

func (s *Stories) ReqReplaceContentState(e *am.Event) {
	args := ParseArgs(e.Args)
	s.replacePending = true
	s.buttons = args.Buttons
	s.stories = args.Stories
	s.mach.Add1(ssStories.ReplaceContent, nil)
}

func (s *Stories) ReplaceContentState(e *am.Event) {
	mach := s.Mach()
	buts := s.buttons
	s.replacePending = false

	s.storiesList.SetText(s.renderStories())
	s.app.QueueUpdateDraw(func() {

		// TODO restore selection to the same button, not index
		selIdx := s.focusManager.GetFocusIndex()
		s.focusManager.Reset()
		s.focusManager.Add(s.storiesList)
		s.ClearButtons()

		for _, but := range buts {
			err := s.AddButton(but)
			if err != nil {
				mach.EvAddErrState(e, ss.ErrUI, err, nil)
				return
			}
		}

		s.focusManager.FocusAt(min(selIdx, s.focusManager.Len()-1))

		// deactivate after drawing
		s.app.SetAfterDrawFunc(func(_ tcell.Screen) {
			s.mach.Remove1(ssStories.ReplaceContent, nil)
			s.app.SetAfterDrawFunc(nil)
		})
	})
}

func (s *Stories) ReplaceContentEnd(e *am.Event) {
	if s.replacePending {
		s.mach.Add1(ssStories.ReplaceContent, nil)
	}
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (s *Stories) Init(sub shared.UI, screen tcell.Screen, name string) error {

	id := "tui-stories-" + s.agent.Id() + "-" + name
	uiMach, err := am.NewCommon(s.agent.NewStateCtx(ss.UIMode), id, states.UIStoriesSchema,
		ssStories.Names(), nil, s.agent, nil)
	if err != nil {
		return err
	}
	uiMach.SetGroups(states.UIStoriesGroups, states.UIStoriesStates)
	s.mach = uiMach

	s.InitComponents()
	if screen != nil {
		screen.EnableMouse(tcell.MouseMotionEvents)
		s.app.SetScreen(screen)
	}
	err = sub.BindHandlers()
	if err != nil {
		return err
	}

	// TODO dispose on DisposingState?
	s.handlers = &StoriesHandlers{s: s}
	err = s.Mach().BindHandlers(s.handlers)
	if err != nil {
		return err
	}

	shared.MachTelemetry(uiMach, shared.LogArgs)
	return nil
}

func (s *Stories) Logger() *slog.Logger {
	return s.logger
}

func (s *Stories) Start(dispose func() error) error {
	s.dispose = dispose
	// start the UI loop
	s.UIMach().Add(S{ssStories.Start, ssStories.Ready}, nil)
	go s.UIMach().Add1(ssStories.Ready, nil)
	err := s.app.Run()
	if err != nil && err.Error() != "EOF" {
		s.agent.AddErrState(ss.ErrUI, err, nil)
	}

	return err
}

func (s *Stories) Stop() error {
	_ = s.dispose()
	s.app.Stop()

	return nil
}

func (s *Stories) Mach() *am.Machine {
	return s.agent
}

func (s *Stories) UIMach() *am.Machine {
	return s.mach
}

// BindHandlers binds transition handlers to the state machine. Overwrite it to bind methods from a subclass.
func (s *Stories) BindHandlers() error {
	return s.mach.BindHandlers(s)
}

func (s *Stories) Redraw() {
	go s.app.QueueUpdateDraw(func() {})
}

func (s *Stories) InitComponents() {
	s.app = cview.NewApplication()
	s.app.EnableMouse(true)

	// header
	s.header = cview.NewTextView()
	label := os.Getenv("SECAI_LABEL")
	intro := os.Getenv("SECAI_INTRO")
	if intro != "" && label != "" {
		s.header.SetDynamicColors(true)
		s.header.SetTitle(label)
		s.header.SetBorder(true)
		s.header.SetText(intro)
	}

	// stories
	s.storiesList = cview.NewTextView()
	s.storiesList.SetDynamicColors(true)
	s.storiesList.SetChangedFunc(func() {
		s.app.Draw()
	})
	s.storiesList.SetTitle("Stories")
	s.storiesList.SetBorder(true)

	// buttons
	s.buttonsView = cview.NewScrollView()

	// LAYOUT

	s.layout = cview.NewFlex()
	s.layout.SetDirection(cview.FlexRow)
	if intro != "" && label != "" {
		s.layout.AddItem(s.header, 5, 1, false)
	}
	s.layout.AddItem(s.storiesList, 0, 1, false)
	s.layout.AddItem(s.buttonsView, 0, 1, true)
	s.app.SetRoot(s.layout, true)

	s.focusManager = cview.NewFocusManager(s.app.SetFocus)
	s.focusManager.SetWrapAround(true)
	s.focusManager.Add(s.storiesList)

	inputHandler := cbind.NewConfiguration()
	for _, key := range cview.Keys.MovePreviousField {
		err := inputHandler.Set(key, wrap(s.focusManager.FocusPrevious))
		s.UIMach().AddErr(err, nil)
	}
	for _, key := range cview.Keys.MoveNextField {
		err := inputHandler.Set(key, wrap(s.focusManager.FocusNext))
		s.UIMach().AddErr(err, nil)
	}
	s.app.SetInputCapture(inputHandler.Capture)

	// data
	for _, but := range s.buttons {
		err := s.AddButton(but)
		if err != nil {
			// TODO pipe local exception
			s.Mach().AddErrState(ss.ErrUI, err, nil)
			break
		}
	}
	s.storiesList.SetText(s.renderStories())

	// catch ctrl+c
	s.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			_ = s.Stop()
			return nil
		}

		return inputHandler.Capture(event)
	})

}

func wrap(f func()) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		f()
		return nil
	}
}

// ClearButtons replaces the whole button view with a new one. This method CANT be called while rendering, as
// replacing flexview items will deadlock.
func (s *Stories) ClearButtons() {
	for _, prim := range s.buttonsView.GetItems() {
		s.buttonsView.RemoveItem(prim)
	}
}

func (s *Stories) AddButton(button shared.StoryButton) error {
	var clicked atomic.Bool
	// TODO config?
	bg := tcell.ColorBlue
	bgClicked := tcell.ColorGreen
	bgDisabled := tcell.ColorDarkGray
	enabled := true
	if button.IsDisabled != nil {
		enabled = !button.IsDisabled()
	}

	// progress button
	if button.ValueEnd != nil {
		v := button.Value()

		// TODO cview hangs with 0-0 ranges
		end := max(1, button.ValueEnd())
		p := cview.NewProgressBar()

		p.SetMax(end)
		p.SetProgress(v)
		p.SetBorder(true)
		if v == end && button.LabelEnd != "" {
			p.SetTitle(button.LabelEnd)
		} else {
			p.SetTitle(button.Label)
		}
		s.buttonsView.AddItem(p, 3, false)
		s.focusManager.Add(p)
		p.SetBackgroundColor(bg)

		// click
		if button.Action != nil && enabled {
			p.SetMouseCapture(func(action cview.MouseAction, event *tcell.EventMouse) (
				cview.MouseAction, *tcell.EventMouse,
			) {
				if action != cview.MouseLeftClick {
					return action, event
				}

				// pressed
				if !clicked.Load() {
					p.SetBackgroundColor(bgClicked)
					s.Redraw()
					clicked.Store(true)
				}
				button.Action()

				// unpressed
				go func() {
					time.Sleep(clickDelay)
					if clicked.Load() {
						p.SetBackgroundColor(bg)
						s.Redraw()
						clicked.Store(false)
					}
				}()

				return 0, nil

			})

			// TODO enter / space key
		} else {
			p.SetBackgroundColor(bgDisabled)
		}

		return nil
	}

	// solid button
	b := cview.NewButton(button.Label)
	b.SetBackgroundColor(bg)
	// click
	if button.Action != nil && enabled {
		b.SetSelectedFunc(func() {

			// pressed
			if !clicked.Load() {
				b.SetBackgroundColor(bgClicked)
				s.Redraw()
				clicked.Store(true)
			}
			button.Action()

			// unpressed
			go func() {
				time.Sleep(clickDelay)
				if clicked.Load() {
					b.SetBackgroundColor(bg)
					s.Redraw()
					clicked.Store(false)
				}
			}()
		})
	} else {
		b.SetBackgroundColor(bgDisabled)
	}
	b.SetBorder(true)
	s.buttonsView.AddItem(b, 3, false)
	s.focusManager.Add(b)

	return nil
}

func (s *Stories) renderStories() string {
	mach := s.Mach()
	text := ""

	for i, story := range s.stories {
		active := ""
		if mach.Is1(story.State) {
			active = "limegreen"
		}

		ago := ""
		if !story.DeactivatedAt.IsZero() {
			ago = fmt.Sprintf("     [grey]%.0fm ago for t%d", time.Since(story.DeactivatedAt).Minutes(), story.LastActiveTicks)
		}
		text += shared.Sp(`
			%d. [%s::b]%s[-:-:-]%s
			   [darkgrey::-]%s[-]
		`, i+1, active, story.Title, ago, story.Desc) + "\n"
	}

	return text
}

// ///// ///// /////

// ///// HANDLERS (AGENT)

// ///// ///// /////

// StoriesHandlers are handlers for the agent's machine from the Stories TUI.
type StoriesHandlers struct {
	*am.ExceptionHandler
	s *Stories
}

func (h *StoriesHandlers) UICleanOutputState(e *am.Event) {
	h.s.agent.Remove1(ss.UICleanOutput, nil)
	h.s.stories = nil
	h.s.buttons = nil
	h.s.mach.EvAdd1(e, ssStories.ReplaceContent, nil)
}
