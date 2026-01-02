package tui

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/cview"
	"github.com/pancsta/secai/shared"
)

const clickDelay = time.Second

type Stories struct {
	storiesList    *cview.TextView
	layout         *cview.Flex
	buttonsView    *cview.ScrollView
	replacePending bool
	buttons        []shared.StoryAction
	stories        []shared.StoryInfo
	// handlers for the parent agent machine
	handlers *StoriesHandlers
	header   *cview.TextView
	t        *Tui
}

// NewStories returns a new TUI dedicated to showing stories and their progress (as buttons).
func NewStories(
	tui *Tui, buttons []shared.StoryAction, stories []shared.StoryInfo,
) *Stories {
	return &Stories{
		t:       tui,
		buttons: buttons,
		stories: stories,
	}
}

// ///// ///// /////

// ///// HANDLERS (STORIES)

// ///// ///// /////

func (s *Stories) ReqReplaceStoriesState(e *am.Event) {
	args := ParseArgs(e.Args)
	s.replacePending = true
	s.buttons = args.Actions
	s.stories = args.Stories
	s.t.MachTUI.Add1(ssT.ReplaceStories, nil)
}

func (s *Stories) ReplaceStoriesState(e *am.Event) {
	mach := s.t.MachTUI
	buts := s.buttons
	s.replacePending = false
	defer mach.Remove1(ssT.ReplaceStories, nil)

	s.storiesList.SetText(s.renderStories())
	// restore selection TODO
	// s.t.app.QueueUpdateDraw(func() {
	//
	// 	// TODO restore selection to the same button, not index
	// 	selIdx := s.t.focusManager.GetFocusIndex()
	// 	s.t.focusManager.Reset()
	// 	s.t.focusManager.Add(s.storiesList)
	s.ClearActions()
	//
	for _, but := range buts {
		err := s.AddAction(but)
		if err != nil {
			mach.EvAddErrState(e, ss.ErrUI, err, nil)
			return
		}
	}
	//
	// 	s.t.focusManager.FocusAt(min(selIdx, s.t.focusManager.Len()-1))
	//
	// deactivate after drawing TODO needs a separate app
	// s.t.app.SetAfterDrawFunc(func(_ tcell.Screen) {
	// 	mach.Remove1(ssT.ReplaceContent, nil)
	// 	s.t.app.SetAfterDrawFunc(nil)
	// })
	// })
}

func (s *Stories) ReplaceStoriesEnd(e *am.Event) {
	if s.replacePending {
		s.t.MachTUI.Add1(ssT.ReplaceStories, nil)
	}
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (s *Stories) Init() error {
	if err := s.t.MachTUI.BindHandlers(s); err != nil {
		return err
	}

	// TODO dispose on DisposingState?
	s.handlers = &StoriesHandlers{s: s}
	if err := s.t.agent.BindHandlers(s.handlers); err != nil {
		return err
	}

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
		s.t.app.Draw()
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

	// data
	for _, but := range s.buttons {
		err := s.AddAction(but)
		if err != nil {
			// TODO pipe local exception
			s.t.agent.AddErrState(ss.ErrUI, err, nil)
			break
		}
	}
	s.storiesList.SetText(s.renderStories())

	return nil
}

// ClearActions replaces the whole button view with a new one. This method CANT be called while rendering, as
// replacing flexview items will deadlock.
func (s *Stories) ClearActions() {
	for _, prim := range s.buttonsView.GetItems() {
		s.buttonsView.RemoveItem(prim)
	}
}

func (s *Stories) AddAction(button shared.StoryAction) error {
	var clicked atomic.Bool
	enabled := true
	if button.IsDisabled != nil {
		enabled = !button.IsDisabled()
	}

	// progress button
	if button.ValueEnd != nil {
		v := button.Value()

		// TODO cview hangs with 0-0 ranges
		end := max(2, button.ValueEnd())
		p := cview.NewProgressBar()

		p.SetMax(end)
		p.SetProgress(v)
		p.SetBorder(true)
		if v >= end && button.LabelEnd != "" {
			p.SetTitle(button.LabelEnd)
		} else {
			p.SetTitle(button.Label)
		}
		s.buttonsView.AddItem(p, 3, false)
		// TODO
		// s.t.focusManager.Add(p)
		p.SetBackgroundColor(themeButtonBg)

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
					p.SetBackgroundColor(themeButtonBgClicked)
					s.t.Redraw()
					clicked.Store(true)
				}
				button.Action()

				// unpressed
				go func() {
					time.Sleep(clickDelay)
					if clicked.Load() {
						p.SetBackgroundColor(themeButtonBg)
						s.t.Redraw()
						clicked.Store(false)
					}
				}()

				return 0, nil

			})

			// TODO enter / space key
		} else {
			p.SetBackgroundColor(themeButtonBgDisabled)
		}

		return nil
	}

	// solid button
	b := cview.NewButton(button.Label)
	b.SetBackgroundColor(themeButtonBg)
	// click
	if button.Action != nil && enabled {
		b.SetSelectedFunc(func() {

			// pressed
			if !clicked.Load() {
				b.SetBackgroundColor(themeButtonBgClicked)
				s.t.Redraw()
				clicked.Store(true)
			}
			button.Action()

			// unpressed
			go func() {
				time.Sleep(clickDelay)
				if clicked.Load() {
					b.SetBackgroundColor(themeButtonBg)
					s.t.Redraw()
					clicked.Store(false)
				}
			}()
		})
	} else {
		b.SetBackgroundColor(themeButtonBgDisabled)
	}
	b.SetBorder(true)
	s.buttonsView.AddItem(b, 3, false)
	// s.t.focusManager.Add(b)

	return nil
}

func (s *Stories) renderStories() string {
	mach := s.t.agent
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
	s *Stories
}

func (h *StoriesHandlers) UICleanOutputState(e *am.Event) {
	tui := h.s.t

	tui.agent.Remove1(ss.UICleanOutput, nil)
	h.s.stories = nil
	h.s.buttons = nil
	tui.MachTUI.EvAdd1(e, ssT.ReplaceStories, nil)
}
