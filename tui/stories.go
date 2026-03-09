package tui

import (
	"fmt"
	"sync/atomic"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/cview"

	"github.com/pancsta/secai/shared"
)

// TODO merge into TUI
type Stories struct {
	storiesList *cview.TextView
	layout      *cview.Flex
	buttonsView *cview.ScrollView
	actions     []shared.ActionInfo
	stories     []shared.StoryInfo
	// handlers for the parent agent machine
	handlers *StoriesHandlers
	header   *cview.TextView
	t        *TUI
	cfg      *shared.Config
	// currently clicked button ID
	clicked atomic.Pointer[string]
	// button ID to UI button
	buttons map[string]*cview.Button
}

// NewStories returns a new TUI dedicated to showing stories and their progress (as buttons).
func NewStories(tui *TUI, actions []shared.ActionInfo, stories []shared.StoryInfo, cfg *shared.Config) *Stories {
	s := &Stories{
		t:       tui,
		actions: actions,
		stories: stories,
		cfg:     cfg,
		buttons: make(map[string]*cview.Button),
	}
	s.clicked.Store(new(""))

	return s
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (s *Stories) UIRenderStoriesState(e *am.Event) {
	mach := s.t.agent
	args := ParseArgs(e.Args)
	if args.Actions != nil {
		s.actions = args.Actions
	}
	if args.Stories != nil {
		s.stories = args.Stories
	}

	s.storiesList.SetText(s.renderStories())
	s.hClearActions()

	for _, act := range s.actions {
		err := s.addAction(act)
		if err != nil {
			mach.EvAddErrState(e, ss.ErrUI, err, nil)
			return
		}
	}

	s.t.Redraw()
}

func (s *Stories) UICleanOutputState(e *am.Event) {
	s.stories = nil
	s.actions = nil
	s.t.agent.EvAdd1(e, ss.UIRenderStories, nil)
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (s *Stories) Init() error {
	if err := s.t.agent.BindHandlers(s); err != nil {
		return err
	}

	// header
	s.header = cview.NewTextView()
	label := s.cfg.Agent.Label
	intro := s.cfg.Agent.Intro
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
	for _, act := range s.actions {
		err := s.addAction(act)
		if err != nil {
			s.t.agent.AddErrState(ss.ErrUI, err, nil)
			break
		}
	}
	s.storiesList.SetText(s.renderStories())

	return nil
}

// hClearActions replaces the whole button view with a new one. This method CANT be called while rendering, as
// replacing flexview items will deadlock.
func (s *Stories) hClearActions() {
	for _, prim := range s.buttonsView.GetItems() {
		s.buttonsView.RemoveItem(prim)
	}
	s.buttons = make(map[string]*cview.Button)
}

func (s *Stories) actionByID(id string) *shared.ActionInfo {
	for _, act := range s.actions {
		if act.ID == id {
			return &act
		}
	}
	return nil
}

func (s *Stories) buttonByID(id string) *cview.Button {
	but, _ := s.buttons[id]
	return but
}

func (s *Stories) addAction(act shared.ActionInfo) error {
	if !act.VisibleMem || !act.VisibleAgent {
		return nil
	}

	enabled := !act.IsDisabled

	if act.ValueEnd > 0 {
		return s.addProgress(act, enabled)
	}

	// solid button
	s.addButton(act, enabled)
	// s.t.focusManager.Add(b)

	return nil
}

func (s *Stories) addButton(action shared.ActionInfo, enabled bool) {
	but := cview.NewButton(action.Label)
	but.SetBackgroundColor(themeButtonBg)
	but.SetBackgroundColorFocused(themeButtonBg)
	if *s.clicked.Load() == action.ID {
		but.SetBackgroundColor(themeButtonBgClicked)
	} else if !enabled {
		but.SetBackgroundColor(themeButtonBgDisabled)
	}
	s.buttons[action.ID] = but

	// click
	if action.Action && enabled {
		but.SetSelectedFunc(func() {

			// pressed
			s.clicked.Store(new(action.ID))
			but.SetBackgroundColor(themeButtonBgClicked)
			s.t.Redraw()
			s.t.agent.Add1(ss.StoryAction, Pass(&A{
				ID: action.ID,
			}))

			// unpressed TODO terrible
			go func() {
				time.Sleep(clickDelay)
				if *s.clicked.Load() == action.ID {
					s.clicked.Store(new(""))
				}
				s.t.MachTUI.Eval("addButton", func() {
					// fresh refs
					action := s.actionByID(action.ID)
					if action == nil {
						return
					}
					but := s.buttonByID(action.ID)
					if but == nil {
						return
					}

					but.SetBackgroundColor(themeButtonBg)
					if action.IsDisabled {
						but.SetBackgroundColor(themeButtonBgDisabled)
					}
					s.t.Redraw()
				}, nil)
			}()
		})
	}

	but.SetBorder(true)
	s.buttonsView.AddItem(but, 3, false)
}

func (s *Stories) addProgress(action shared.ActionInfo, enabled bool) error {
	// progress button
	v := action.Value

	// TODO cview hangs with 0-0 ranges
	end := max(2, action.ValueEnd)
	p := cview.NewProgressBar()

	p.SetMax(end)
	p.SetProgress(v)
	p.SetBorder(true)
	if v >= end && action.LabelEnd != "" {
		p.SetTitle(action.LabelEnd)
	} else {
		p.SetTitle(action.Label)
	}
	s.buttonsView.AddItem(p, 3, false)
	// TODO
	// s.t.focusManager.Add(p)

	// style
	p.SetBackgroundColor(themeButtonBg)
	if !enabled {
		p.SetBackgroundColor(themeButtonBgDisabled)
	}

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
