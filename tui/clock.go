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

// TODO re-render on resize

type Clock struct {
	mach      *am.Machine
	logger    *slog.Logger
	app       *tview.Application
	msgs      []*shared.Msg
	uiMach    *am.Machine
	hist      *amhist.History
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

		mach:   mach,
		logger: logger,
		app:    tview.NewApplication(),
	}

	return c
}

func (c *Clock) DisposingState(e *am.Event) {
	// TODO
	// c.Mach().EvAddErrState(e, ss.ErrUI,
	// 	tty.Close(), nil)
}

// regexp for " \d+"
var lineNumRe = regexp.MustCompile(`^(\s+)(\d+)`)

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
		// lines[i] = ansi.String(result)

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

	id := "tui-clock-" + c.mach.Id() + "-" + name
	uiMach, err := am.NewCommon(c.mach.NewStateCtx(ss.UIMode), id, states.UIStoriesSchema, ssui.Names(), nil, c.mach, nil)
	if err != nil {
		return err
	}
	c.screen = screen
	shared.MachTelemetry(uiMach, nil)
	c.uiMach = uiMach
	mach := c.Mach()

	// TODO track whitelistChanged
	c.hist = amhist.Track(mach, c.States, c.HistSize)
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
	return c.mach
}

func (c *Clock) UIMach() *am.Machine {
	return c.uiMach
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

	// plot recent clocks
	for iser, ser := range series {
		// cut to max width
		if ser.To-ser.From > maxWidth {
			ser.To = ser.From + maxWidth
		}

		amount := ser.To - ser.From

		plots[iser] = make([]float64, min(maxWidth, maxLen))
		ticks := make(am.Time, amount)
		// TODO Entries created for non tracked states?
		for _, e := range *c.hist.Entries.Load() {
			ticks = ticks.Add(e.MTimeDiff[ser.From:ser.To])
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
	c.UIMach().Add(S{ssui.Start, ssui.Ready}, nil)
	go c.mach.Add1(ss.UIReady, nil)
	err := c.app.Run()
	if err != nil && err.Error() != "EOF" {
		c.mach.AddErrState(ss.ErrUI, err, nil)
	}

	return err
}

func (c *Clock) Stop() error {
	_ = c.dispose()
	c.app.Stop()

	return nil
}
