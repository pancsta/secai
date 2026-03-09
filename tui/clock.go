package tui

import (
	"slices"
	"sync/atomic"

	"github.com/pancsta/asciigraph-tcell"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/cview"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/tcell-v2"
)

// TODO merge into TUI
// TODO bind mem to the clock
type Clock struct {
	t      *TUI
	layout *cview.Grid
	diff   atomic.Pointer[[][]int] // [ series ][ tick]
}

func NewClock(tui *TUI, diff [][]int) *Clock {
	ret := &Clock{
		t: tui,
	}
	ret.diff.Store(&diff)

	return ret
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (c *Clock) UIRenderClockEnter(e *am.Event) bool {
	return ParseArgs(e.Args).ClockDiff != nil
}

func (c *Clock) UIRenderClockState(e *am.Event) {
	diff := ParseArgs(e.Args).ClockDiff
	c.diff.Store(&diff)
}

func (c *Clock) Redraw() {
	diffPtr := c.diff.Load()
	if diffPtr == nil {
		return
	}
	diff := *diffPtr
	// x, y, _, height := c.layout.Box.GetInnerRect()
	x, y, width, height := c.layout.Box.GetInnerRect()

	s := c.t.app.GetScreen()
	c.t.app.Lock()
	defer c.t.app.Unlock()
	defer s.Show()

	// no center
	// data := diff

	// center
	data := make([][]float64, len(diff))
	longest := 0
	for i := range data {
		if len(diff[i]) > longest {
			longest = len(diff[i])
		}
	}
	for i := range data {
		// TODO len out of side
		l := width/2 + 1 - longest/2
		l = max(l, 0)
		data[i] = slices.Concat(make([]float64, l), intsToFloats(diff[i]), []float64{0})
		// DEBUG
		// data[i][l] = 10
		// data[i][len(data[i])-2] = 10

		// trim
		if len(data[i]) > width {
			data[i] = data[i][:width]
		}
	}

	asciigraph.PlotManyToScreen(s, x, y, data,
		asciigraph.Height(height-1), asciigraph.Precision(0), asciigraph.HideAxisY(true), asciigraph.HideAxisX(true),
		asciigraph.SeriesColors(
			tcell.ColorRed,
			tcell.ColorBlue,
			tcell.ColorYellow,
			tcell.ColorGreen,
		))
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
	c.layout.SetBackgroundTransparent(false)
	c.layout.SetBackgroundColor(themeBgColor)

	return nil
}

// ///// ///// /////

// ///// SERVICE

// ///// ///// /////

type ClockService struct {
	Agent *am.Machine
	Cfg   *shared.Config
	// agent's history
	Hist      func() (amhist.MemoryApi, error)
	SeriesLen int
	Height    int

	// machine time of the last render
	lastRedraw uint64
	cacheData  [][]int
}

func (c *ClockService) Data() [][]int {
	// data or cache
	mTimeSum := c.Agent.Time(nil).Sum(nil)
	if c.lastRedraw != mTimeSum {
		c.cacheData = c.Data()
	}
	c.lastRedraw = mTimeSum

	return c.cacheData
}

func (c *ClockService) UIUpdateClockEnter(e *am.Event) bool {
	_, err := c.Hist()
	return err == nil
}

func (c *ClockService) UIUpdateClockState(e *am.Event) {
	ctx := c.Agent.NewStateCtx(ss.UIUpdateClock)
	hist, _ := c.Hist()

	// TODO use schema groups (self?)
	tracked := hist.Config().TrackedStates
	lastState := slices.Index(tracked, ss.REPL)
	states := tracked[0:lastState]

	// build series (colors)
	// _, _, width, _ := c.layout.Box.GetRect()
	seriesLen := c.SeriesLen
	i := 0
	var series = []struct{ From, To int }{}
	for len(series) < len(states)/seriesLen {
		series = append(series, struct{ From, To int }{
			From: seriesLen * i,
			To:   min(len(states), seriesLen*(i+1)),
		})
		i++
	}

	if len(states) < 1 {
		c.Agent.EvRemove1(e, ss.UIUpdateClock, nil)
		return
	}

	plots := make([][]int, len(series))
	maxLen := lastState

	// query history
	c.Agent.Fork(ctx, e, func() {
		defer c.Agent.EvRemove1(e, ss.UIUpdateClock, nil)

		limit := min(100, c.Cfg.TUI.ClockRange)
		rows, err := hist.FindLatest(ctx, false, limit, amhist.Query{})
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			// TODO UIErr
			c.Agent.AddErr(err, nil)
			return
		}

		// plot recent clocks
		for iSer, ser := range series {
			// cut to max width
			// if ser.To-ser.From > maxWidth {
			// 	ser.To = ser.From + maxWidth
			// }

			// init data
			amount := ser.To - ser.From
			plots[iSer] = make([]int, min(seriesLen, maxLen))
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

			// TODO math.Round
			div := c.Height / int(max(1, maxC))
			for i, t := range ticks {
				plots[iSer][i] = int(t) / max(1, div)
			}
		}

		c.Agent.EvAdd1(e, ss.UIRenderClock, PassRPC(&A{
			ClockDiff: plots,
		}))
	})
}

func intsToFloats(ints []int) []float64 {
	// 1. Pre-allocate the float64 slice with the exact length needed
	floats := make([]float64, len(ints))

	// 2. Loop through and cast each integer to a float64
	for i, v := range ints {
		floats[i] = float64(v)
	}

	return floats
}
