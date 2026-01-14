package tui

import (
	"slices"

	"github.com/gdamore/tcell/v2"
	"github.com/pancsta/asciigraph-tcell"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/cview"
)

// TODO re-render on resize

type Clock struct {
	Height    int
	SeriesLen int

	t      *Tui
	layout *cview.Grid
	// agent's history
	hist func() (amhist.MemoryApi, error)
	// machine time of the last render
	lastRedraw uint64
	lastData   [][]float64
}

func NewClock(tui *Tui, hist func() (amhist.MemoryApi, error)) *Clock {

	c := &Clock{
		t:         tui,
		Height:    4,
		SeriesLen: 15,
		hist:      hist,
	}

	return c
}

// ///// ///// /////

// ///// HANDLERS (AGENT)

// ///// ///// /////

func (c *Clock) DisposingState(e *am.Event) {
	// TODO
	// c.tui.mach().EvAddErrState(e, ss.ErrUI,
	// 	tty.Close(), nil)
}

func (c *Clock) AnyState(e *am.Event) {
	c.t.Redraw()
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (c *Clock) Init() error {
	if err := c.t.agent.BindHandlers(c); err != nil {
		return err
	}
	c.layout = cview.NewGrid()
	c.layout.AddItem(cview.NewBox(), 0, 0, 1, 1, 4, 0, true)

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

func (c *Clock) Redraw() {
	// data or cache
	mTimeSum := c.t.Mach().Time(nil).Sum(nil)
	if c.lastRedraw != mTimeSum {
		c.lastData = c.Data()
	}
	c.lastRedraw = mTimeSum
	x, y, width, height := c.layout.Box.GetInnerRect()

	s := c.t.app.GetScreen()
	c.t.app.Lock()
	defer c.t.app.Unlock()
	defer s.Show()

	asciigraph.PlotManyToScreen(s, x, y, c.lastData, asciigraph.Width(width-2),
		asciigraph.Height(height-1), asciigraph.Precision(0), asciigraph.HideAxisY(true),
		asciigraph.SeriesColors(
			tcell.ColorRed,
			tcell.ColorYellow,
			tcell.ColorGreen,
			tcell.ColorBlue,
		))
}

func (c *Clock) Data() [][]float64 {
	hist, err := c.hist()
	if err != nil {
		return [][]float64{make([]float64, 1)}
	}

	// TODO use schema groups (self?)
	tracked := hist.Config().TrackedStates
	lastState := slices.Index(tracked, ss.UICleanOutput)
	states := tracked[0:lastState]

	// build series (colors)
	_, _, width, _ := c.layout.Box.GetRect()
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
	maxWidth := width + 1

	// query history TODO better ctx
	ctx := c.t.MachTUI.Ctx()
	rows, err := hist.FindLatest(ctx, false, c.t.cfg.TUI.ClockRange, amhist.Query{})
	if err != nil {
		c.t.MachTUI.AddErr(err, nil)
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
