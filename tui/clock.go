package tui

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/guptarohit/asciigraph"
	"github.com/leaanthony/go-ansi-parser"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui/states"
	"github.com/rivo/tview"
)

var ssClock = states.UIClockStates

// TODO re-render on resize

// regexp for " \d+"
var lineNumRe = regexp.MustCompile(`^(\s+)(\d+)`)

type Clock struct {
	agent     *am.Machine
	logger    *slog.Logger
	app       *tview.Application
	msgs      []*shared.Msg
	mach      *am.Machine
	hist      *amhist.Memory
	dispose   func() error
	redrawing atomic.Bool

	Height    int
	States    am.S
	HistSize  int
	chart     string
	screen    tcell.Screen
	tty       tcell.Tty
	SeriesLen int
}

var _ shared.UI = &Clock{}

func NewClock(mach *am.Machine, logger *slog.Logger, clockStates am.S) *Clock {

	c := &Clock{
		Height:    4,
		States:    clockStates,
		HistSize:  10,
		SeriesLen: 15,

		agent:  mach,
		logger: logger,
		app:    tview.NewApplication(),
	}

	return c
}

// ///// ///// /////

// ///// HANDLERS (AGENT)

// ///// ///// /////

func (c *Clock) DisposingState(e *am.Event) {
	// TODO
	// c.Mach().EvAddErrState(e, ss.ErrUI,
	// 	tty.Close(), nil)
}

func (c *Clock) AnyState(e *am.Event) {
	c.Redraw()
}

func (c *Clock) Redraw() {
	if c.redrawing.Swap(true) {
		return
	}
	defer c.redrawing.Store(false)

	mach := c.Mach()
	c.chart = c.Chart(c.Data())

	// remove line numbers
	lines := strings.Split(c.chart, "\n")
	size, err := c.tty.WindowSize()
	if err != nil {
		mach.Log("%w: %w", ss.ErrUI, err)
		return
	}
	for i, line := range lines {

		// remove prefix
		lines[i] = lineNumRe.ReplaceAllString(line, "")

		// TODO trim grid lines
		// parsed, err := ansi.Parse(lines[i])
		// if err != nil {
		// 	mach.Log("%w: %w", ss.ErrUI, err)
		// 	lines = []string{""}
		// 	break
		// }
		//
		// var result []*ansi.StyledText
		// for i, element := range parsed {
		//
		// 	if i > 0 {
		// 		result = append(result, element)
		// 		continue
		// 	}
		//
		// 	var newLabel []rune
		// 	graphemes := uniseg.NewGraphemes(element.Label)
		// 	toSkip := 4
		// 	for graphemes.Next() {
		// 		if toSkip > 0 {
		// 			toSkip--
		// 			continue
		// 		}
		// 		newLabel = append(newLabel, graphemes.Runes()...)
		// 		element.Label = string(newLabel)
		// 		result = append(result, element)
		// 		break
		// 	}
		//
		// }
		// lines[i] = ansi.MutString(result)

		// limit to width
		lines[i], err = ansi.Truncate(lines[i], size.Width)
		if err != nil {
			mach.Log("%w: %w", ss.ErrUI, err)
			lines = []string{""}
			break
		}
	}
	c.chart = strings.Join(lines, "\n")

	// clear the screen
	_, _ = fmt.Fprint(c.tty, "\033[2J\033[H")
	_, err = c.tty.Write([]byte(c.chart))
	if err != nil && !errors.Is(err, io.EOF) {
		mach.Log("%w: %w", ss.ErrUI, err)
		return
	}
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (c *Clock) Init(sub shared.UI, screen tcell.Screen, name string) error {
	ctx := c.agent.NewStateCtx(ss.UIMode)

	// init the UI machine
	id := "tui-clock-" + c.agent.Id() + "-" + name
	uiMach, err := am.NewCommon(ctx, id, states.UIClockSchema, ssClock.Names(), nil, c.agent, nil)
	if err != nil {
		return err
	}
	uiMach.SetGroups(states.UIClockGroups, states.UIClockStates)
	shared.MachTelemetry(uiMach, nil)
	c.screen = screen
	c.mach = uiMach

	// start tracking the agent
	c.hist, err = amhist.NewMemory(ctx, nil, c.agent, amhist.BaseConfig{
		TrackedStates: c.States,
		Changed:       c.States,
		MaxRecords:    c.HistSize,
	}, func(err error) {
		uiMach.AddErr(err, nil)
	})
	if err != nil {
		return err
	}
	c.InitComponents()
	if screen != nil {
		screen.EnableMouse(tcell.MouseMotionEvents)
		// TODO enable paste?
		c.app.SetScreen(screen)
	}

	tty, ok := c.screen.Tty()
	if !ok {
		// TODO pipe
		return errors.New("tty not ok")
	}
	c.tty = tty
	c.tty.NotifyResize(c.Redraw)

	return sub.BindHandlers()
}

func (c *Clock) Logger() *slog.Logger {
	return c.logger
}

func (c *Clock) Mach() *am.Machine {
	return c.agent
}

func (c *Clock) UIMach() *am.Machine {
	return c.mach
}

// BindHandlers binds transition handlers to the state machine. Overwrite it to bind methods from a subclass.
func (c *Clock) BindHandlers() error {
	return c.Mach().BindHandlers(c)
}

func (c *Clock) InitComponents() {
}

func (c *Clock) Chart(data [][]float64) string {
	return asciigraph.PlotMany(data, asciigraph.Height(c.Height), asciigraph.Precision(0),
		asciigraph.SeriesColors(
			asciigraph.Red,
			asciigraph.Yellow,
			asciigraph.Green,
			asciigraph.Blue,
		))
}

func (c *Clock) Data() [][]float64 {

	lastState := slices.Index(c.States, ss.UICleanOutput)
	states := c.States[0:lastState]

	// build series (colors)
	size, _ := c.tty.WindowSize()
	seriesLen := c.SeriesLen
	i := 1
	var series = []struct{ From, To int }{}
	for len(series) < len(states)/seriesLen {
		series = append(series, struct{ From, To int }{
			From: seriesLen * i,
			To:   min(len(states), seriesLen*(i+1)),
		})
		i++
	}

	if len(states) < 1 {
		return [][]float64{make([]float64, 0)}
	}

	plots := make([][]float64, len(series))
	maxLen := lastState
	maxWidth := size.Width + 1

	// query history
	rows, err := c.hist.FindLatest(c.UIMach().Ctx(), false, 0, amhist.Query{})
	if err != nil {
		c.UIMach().AddErr(err, nil)
		// TODO proper defaults, panic
		return [][]float64{make([]float64, len(series))}
	}

	// plot recent clocks
	for iser, ser := range series {
		// cut to max width
		if ser.To-ser.From > maxWidth {
			ser.To = ser.From + maxWidth
		}

		// init data
		amount := ser.To - ser.From
		plots[iser] = make([]float64, min(maxWidth, maxLen))
		ticks := make(am.Time, amount)

		for _, r := range rows {
			diff := r.Time.MTimeTrackedDiff
			if len(diff) < ser.To {
				continue
			}
			ticks = ticks.Add(diff[ser.From:ser.To])
		}

		var maxC uint64
		for _, c := range ticks {
			if c > maxC {
				maxC = c
			}
		}
		div := float64(c.Height) / float64(maxC)

		for i, t := range ticks {
			plots[iser][i] = float64(t) / div
		}
	}

	return plots
}

// Start starts the UI and optionally returns the error and mutates with UIErr.
func (c *Clock) Start(dispose func() error) error {
	c.dispose = dispose
	// start the U
	c.UIMach().Add(S{ssStories.Start, ssStories.Ready}, nil)
	go c.agent.Add1(ss.UIReady, nil)
	err := c.app.Run()
	if err != nil && err.Error() != "EOF" {
		c.agent.AddErrState(ss.ErrUI, err, nil)
	}

	return err
}

func (c *Clock) Stop() error {
	_ = c.dispose()
	c.app.Stop()

	return nil
}
