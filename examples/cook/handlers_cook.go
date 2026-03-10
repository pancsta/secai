package cook

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/sblinch/kdl-go"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/cook/db"
	sa "github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/examples/cook/states"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui"
	"github.com/pancsta/secai/web"
)

func (a *Agent) ExceptionState(e *am.Event) {
	// call base handler
	a.ExceptionHandler.ExceptionState(e)
	args := am.ParseArgs(e.Args)

	// show the error
	if a.ConfigBase().Debug.Verbose {
		a.Output(fmt.Sprintf("ERROR: %s", args.Err), shared.FromSystem)
	}

	// exit on too many errors
	// TODO reset counter sometimes
	if a.Mach().Tick(ss.Exception) > 1000 {
		a.Mach().EvAdd1(e, ss.Disposing, nil)

	}

	// TODO remove empty errors (eg Scraping) and add ErrLoop for breaking errs
}

func (a *Agent) StartState(e *am.Event) {
	// parent handler
	a.AgentLLM.StartState(e)

	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Start)

	// start the UI
	// TODO make UI optional
	mach.EvAdd1(e, ss.UIMode, nil)
	mach.EvAdd1(e, ss.Mock, nil)

	a.handlersWeb = &web.Handlers{A: a}
	mach.AddErr(mach.BindHandlers(a.handlersWeb), nil)

	// start Heartbeat
	go func() {
		tick := time.NewTicker(a.Config.Cook.HeartbeatFreq)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return // expired
			case <-tick.C:
				mach.Add1(ss.Heartbeat, nil)
			}
		}
	}()
}

func (a *Agent) StartEnd(e *am.Event) {
	mach := a.Mach()

	mach.EvAddErr(e,
		mach.DetachHandlers(a.handlersWeb), nil)
	a.handlersWeb = nil
}

func (a *Agent) MockEnter(e *am.Event) bool {
	return a.Config.Debug.Mock && mock.Active
}

func (a *Agent) MockState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Mock)

	// first msg when cooking steps are out, right after the narrator's recipe
	if in := mock.StoryCookingStartedInput; in != "" {
		go func() {
			<-mach.When1(ss.InputPending, ctx)
			if !amhelp.Wait(ctx, time.Second) {
				return
			}
			mach.EvAdd1(e, ss.Prompt, Pass3(&A3{
				Prompt: in,
			}))
		}()
	}

	whenInput3 := mach.WhenTicks(ss.InputPending, 3, ctx)
	// first msg when cooking steps are out, right after the narrator's recipe
	if in := mock.StoryCookingStartedInput3; in != "" {
		go func() {
			<-whenInput3
			if !amhelp.Wait(ctx, time.Second) {
				return
			}
			mach.EvAdd1(e, ss.Prompt, Pass3(&A3{
				Prompt: in,
			}))
		}()
	}
}

func (a *Agent) LoopEnter(e *am.Event) bool {
	return !a.loop.Ended()
}

func (a *Agent) LoopState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Loop)

	// global session timeout TODO handle better
	timeout := a.Config.Cook.SessionTimeout
	if amhelp.IsDebug() {
		timeout *= 10
		// timeout = time.Second * 3
	}

	// unblock
	go func() {
		for a.loop.Ok(nil) {

			// step ctx (for this select only)
			stepCtx, cancel := context.WithCancel(ctx)
			select {

			case <-ctx.Done():
				cancel()
				// expired
				a.loop.Break()

			case <-mach.When1(ss.Interrupted, stepCtx):
				// interrupted, wait until resumed and loop again
				<-mach.When1(ss.Resume, stepCtx)
				cancel()

			// timeout - trigger an interruption
			case <-time.After(timeout):
				cancel()
				mach.EvAdd1(e, ss.Interrupted, Pass3(&A3{
					IntByTimeout: true,
				}))
				a.loop.Break()
			}
		}

		// end
		mach.EvRemove1(e, ss.Loop, nil)
	}()
}

func (a *Agent) AnyState(e *am.Event) {
	mach := a.Mach()
	tx := e.Transition()
	mtime := mach.Time(nil).Sum(nil)

	// refresh stories on state changes but avoid recursion and DUPs
	// TODO bind each story directly via wait methods (maybe a group?)
	skipCalled := S{ss.CheckStories, ss.StoryChanged, ss.Healthcheck, ss.Loop, ss.Requesting,
		ss.RequestingAI, ss.RequestedAI, ss.Heartbeat, ss.UIRenderStories, ss.UICleanOutput, ss.UIUpdateClock}
	called := tx.Mutation.CalledIndex(mach.StateNames())
	if a.lastStoryCheck != mtime && called.Not(skipCalled) && !mach.WillBe1(ss.CheckStories) {
		mach.EvAdd1(e, ss.CheckStories, nil)
	}
	a.lastStoryCheck = mtime

	// redraw clock
	hist, err := a.Hist()
	if err != nil {
		return
	}

	// redraw on tracked changed
	added, removed := e.Transition().TimeIndexDiff()
	names := slices.Concat(added.ActiveStates(nil), removed.ActiveStates(nil))
	for _, s := range names {
		if hist.IsTracked1(s) {
			a.Mach().EvAdd1(e, ss.UIUpdateClock, nil)
			break
		}
	}
}

func (a *Agent) HeartbeatState(e *am.Event) {
	mach := a.Mach()
	mach.Remove1(ss.Heartbeat, nil)

	hist, err := a.Hist()
	if err == nil {
		mach.AddErr(
			hist.Sync(), nil)
	}

	// TODO check orienting
}

func (a *Agent) CheckStoriesState(e *am.Event) {
	// TODO debounce using schedule
	// TODO move to base agent?
	// TODO bind to When methods, dont re-run each time on AnyState
	mach := a.Mach()

	var stateList S
	var activateList []bool
	for _, s := range a.stories {
		// TODO env const
		isDebug := os.Getenv("SECAI_DEBUG_STORY") == s.State
		if isDebug {
			a.Log("checking", "story", s.State)
		}

		// validate
		if !mach.Has1(s.State) {
			mach.AddErr(fmt.Errorf("%w: %s", am.ErrStateMissing, s.State), nil)
			continue
		}

		s.Tick = mach.Tick(s.State)
		// this story should be active
		activate := false
		// this story has automatic triggers
		hasTriggers := false

		// dont activate without passing triggers

		// check the agent machine
		if !s.Agent.Trigger.IsEmpty() {
			activate = s.Agent.Trigger.Check(s.Agent.Mach)
			if isDebug {
				a.Log("cook", "check", activate)
			}
			hasTriggers = true
		}

		// check the memory machine
		if !s.Memory.Trigger.IsEmpty() {
			check := s.Memory.Trigger.Check(s.Memory.Mach)
			activate = (activate || !hasTriggers) && check
			if isDebug {
				a.Log("memory", "check", check)
			}
			hasTriggers = true
		}

		// manual story
		if !hasTriggers {
			if isDebug {
				a.Log("no triggers", "story", s.State)
			}
			continue
		}

		// add to the list after a passed "impossibility check"
		if mach.Is1(s.State) && !activate && !amhelp.CantRemove1(mach, s.State, nil) ||
			mach.Not1(s.State) && activate && !amhelp.CantAdd1(mach, s.State, nil) {

			if isDebug {
				a.Log("story changed", "story", s.State)
			}
			stateList = append(stateList, s.State)
			activateList = append(activateList, activate)
		}
	}

	// apply the changes if any
	if len(stateList) > 0 {
		mach.EvAdd1(e, ss.StoryChanged, Pass3(&A3{
			StatesList:   stateList,
			ActivateList: activateList,
		}))
	}

	// re-render all buttons
	a.renderStories(e)
}

func (a *Agent) StoryChangedState(e *am.Event) {
	mach := a.Mach()
	args := ParseArgs(e.Args)
	states := args.StatesList
	activates := args.ActivateList

	for i, name := range states {
		activate := activates[i]
		s := a.stories[name]

		if s == nil {
			a.Log("story not found", "state", name)
			continue
		}

		// deactivate
		if mach.Is1(s.State) && !activate {
			res := mach.EvRemove1(e, s.State, nil)

			// TODO handle Queued?
			if res != am.Canceled {
				s.Agent.TimeDeactivated = mach.Time(nil)
				s.Memory.TimeDeactivated = a.mem.Time(nil)
				s.DeactivatedAt = time.Now()
				s.LastActiveTicks = s.Agent.TimeDeactivated.Sum(nil) -
					s.Agent.TimeActivated.Sum(nil)
			}

			// activate
		} else if mach.Not1(s.State) && activate {
			res := mach.EvAdd1(e, s.State, nil)

			// TODO handle Queued?
			if res != am.Canceled {
				s.Agent.TimeActivated = mach.Time(nil)
				s.Memory.TimeActivated = a.mem.Time(nil)
			} else {
				a.Log("failed to activate", "state", name)
			}
		}
	}
}

func (a *Agent) InterruptedState(e *am.Event) {
	// call super
	a.AgentLLM.InterruptedState(e)

	mach := a.Mach()
	switch mach.Switch(states.CookGroups.Interruptable) {
	case ss.StoryWakingUp:
		a.Output("not waking up", shared.FromAssistant)
	}
}

func (a *Agent) ReadyEnter(e *am.Event) bool {
	// wait for all the tools to be ready
	// return a.tSearxng.Mach().Is1(ss.Ready)
	return true
}

// ReadyState is a test mocking handler.
func (a *Agent) ReadyState(e *am.Event) {
	if mock.GenStepsRes == "" || mock.Recipe == "" || !a.Config.Debug.Mock || !mock.Active {
		return
	}
	mach := a.Mach()

	// store the recipe and to activate cooking
	recipe := sa.Recipe{}
	if err := json.Unmarshal([]byte(mock.Recipe), &recipe); err != nil {
		mach.EvAddErr(e, err, nil)
		return
	}
	a.recipe.Store(&recipe)
	mach.Add(S{ss.IngredientsReady, ss.RecipeReady}, nil)
}

// TODO enter

func (a *Agent) SSHConnState(e *am.Event) {
	mach := a.Mach()
	args := ParseArgs(e.Args)
	sess := args.SSHSess
	// user := sess.User() TODO desktop / mobile for various UIs
	done := args.Done
	ctx := mach.NewStateCtx(ss.UIMode)

	screen, err := tui.NewSessionScreen(sess)
	if err != nil {
		err = fmt.Errorf("unable to create screen: %w", err)
		mach.EvAddErrState(e, ss.ErrUI, err, nil)
		return
	}

	// new UI will add UIReady
	mach.Remove1(ss.UIReady, nil)
	uiMain := tui.NewTui(mach, a.Logger(), a.ConfigBase(),
		// TODO remote addr of local fwder, not the browser
		sess.RemoteAddr().String())

	// screen init is required for cview, but not for tview TODO still?
	if err := screen.Init(); err != nil {
		_, _ = fmt.Fprintln(sess.Stderr(), "unable to init screen:", err)
		return
	}

	// init the UI TODO merge into 1
	stories := tui.NewStories(uiMain, a.Actions(), a.Stories(), a.ConfigBase())
	chat := tui.NewChat(uiMain, slices.Clone(a.msgs))
	clock := tui.NewClock(uiMain, a.Store().ClockDiff)

	err = uiMain.Init(screen, a.nextUIName(), stories, clock, chat)
	if err != nil {
		a.Mach().EvAddErrState(e, ss.ErrUI, err, nil)
		return
	}

	// register
	a.tuis = append(a.tuis, uiMain)

	uiMain.Redraw()

	// start the UI
	go func() {
		defer close(done)
		if ctx.Err() != nil {
			return // expired
		}

		// TODO catch CTRL+C, CTRL+Q
		//   https://github.com/gliderlabs/ssh/issues/226
		err = uiMain.Start(sess.Close)
		// TODO log err if not EOF?

		mach.EvAdd1(e, ss.SSHDisconn, Pass(&A{
			TUI: uiMain,
		}))
	}()
}

func (a *Agent) SSHDisconnState(e *am.Event) {
	addr := ParseArgs(e.Args).Addr
	ui := ParseArgs(e.Args).TUI

	before := len(a.tuis)
	a.tuis = slices.DeleteFunc(a.tuis, func(t *tui.TUI) bool {
		// dispose all on web disconn TODO add conn IDs via users, match IDs
		if ui == nil {
			amhelp.DisposeEv(t.MachTUI, e)
			return true
		}

		if t == ui {
			return true
		}
		if t.ClientAddr == addr {
			return true
		}
		return false
	})

	after := len(a.tuis)
	if before == after {
		// TODO err state
		a.Mach().EvAddErr(e, fmt.Errorf("undisposed TUI"), nil)
	}
}

func (a *Agent) UIModeEnter(e *am.Event) bool {
	return a.Config.TUI.PortSSH != -1
}

func (a *Agent) UIModeState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.UIMode)

	// new session handler passing to UINewSess state
	var handlerFn ssh.Handler = func(sess ssh.Session) {
		srcAddr := sess.RemoteAddr().String()
		done := make(chan struct{})
		mach.EvAdd1(e, ss.SSHConn, Pass3(&A3{
			SSHSess: sess,
			ID:      sess.User(),
			Addr:    srcAddr,
			Done:    done,
		}))

		// TODO WhenArgs for typed args
		// amhelp.WaitForAll(ctx, time.Hour*9999, mach.WhenArgs(ss.SSHDisconn, am.A{}))

		// keep this session alive
		select {
		case <-ctx.Done():
		case <-done:
		}
	}

	// start SSH
	go func() {
		// save srv ref
		optSrv := func(s *ssh.Server) error {
			mach.EvAdd1(e, ss.SSHReady, Pass3(&A3{
				SSHServer: s,
			}))
			return nil
		}

		addr := a.Config.TUI.Host + ":" + strconv.Itoa(a.Config.TUI.PortSSH)
		a.Log("SSH UI listening", "addr", addr)
		err := ssh.ListenAndServe(addr, handlerFn, optSrv)
		if err != nil {
			mach.EvAddErrState(e, ss.ErrUI, err, nil)
		}
	}()
}

func (a *Agent) UIModeEnd(e *am.Event) {
	// TUIs
	for _, ui := range a.tuis {
		_ = ui.Stop()
	}
	a.tuis = nil

	// SSHs
	if a.srvUI != nil {
		_ = a.srvUI.Close()
	}
}

// TODO enter

func (a *Agent) SSHReadyState(e *am.Event) {
	s := ParseArgs(e.Args).SSHServer
	a.srvUI = s
}

func (a *Agent) UIMsgState(e *am.Event) {
	msg := ParseArgs(e.Args).Msg
	a.msgs = append(a.msgs, msg)
}

func (a *Agent) PromptEnter(e *am.Event) bool {
	// call super
	if !a.AgentLLM.PromptEnter(e) {
		return false
	}

	p := ParseArgs(e.Args).Prompt
	// long enough or a reference
	return len(p) >= a.Config.Cook.MinPromptLen || shared.NumRef(p) != -1
}

func (a *Agent) PromptState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.Prompt)

	limit := a.Config.AI.ReqLimit
	if int(mach.Time(S{ss.RequestingAI}).Sum(nil)) > limit {
		_ = a.OutputPhrase("ReqLimitReached", limit)
		a.reqLimitOk.Store(false)
		a.UserInput = ""
		return
	}

	if mach.Is1(ss.Interrupted) {
		_ = a.OutputPhrase("ResumeNeeded")
		return
	}

	// call super
	a.AgentLLM.PromptState(e)

	// start orienting if the input wasnt expected
	wasPending := !slices.Contains(e.Transition().StatesBefore(), ss.InputPending)
	if mach.Not1(ss.InputPending) && wasPending {
		if mach.EvAdd1(e, ss.Orienting, e.Args) == am.Canceled {
			return
		}

		// handle result
		go func() {
			<-mach.When1(ss.Orienting, ctx)
			<-mach.WhenNot1(ss.Orienting, ctx)
			if ctx.Err() != nil {
				return // expired
			}
			move := a.MoveOrienting.Load()
			if move == nil {
				return
			}
			a.MoveOrienting.Store(nil)

			// exec the move
			mach.EvAdd1(e, ss.OrientingMove, Pass2(&A2{
				Move: move,
			}))
		}()
	}
}

// InputPendingState is a test mocking handler.
func (a *Agent) InputPendingState(e *am.Event) {
	mach := a.Mach()
	if !a.Config.Debug.Mock || !mock.Active {
		return
	}

	errs := mach.Tick(ss.ErrAI)

	switch mach.Tick(ss.InputPending) {
	case 1 + errs:
		if p := mock.FlowPromptIngredients; p != "" {
			mach.EvAdd1(e, ss.Prompt, Pass3(&A3{Prompt: p}))
		}
	case 3 + errs:
		if p := mock.FlowPromptRecipe; p != "" {
			mach.EvAdd1(e, ss.Prompt, Pass3(&A3{Prompt: p}))
		}
	case 5 + errs:
		if p := mock.FlowPromptCooking; p != "" {
			mach.EvAdd1(e, ss.Prompt, Pass3(&A3{Prompt: p}))
		}
	}
}

func (a *Agent) DisposedState(e *am.Event) {
	// the end
	a.Logger().Info("disposed, bye")
	os.Exit(0)
}

// TODO bind
func (a *Agent) UIReadyEnter(e *am.Event) bool {
	for _, ui := range a.tuis {
		if ui.MachTUI.Not1(ss.Ready) {
			return false
		}
	}

	return true
}

// TODO enter?
// 		// stop on a DB err
// 		if mach.WillBe1(ss.ErrDB) {
// 			return
// 		}

func (a *Agent) DBStartingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.DBStarting)

	mach.Fork(ctx, e, func() {
		dbFile := filepath.Join(a.Config.Agent.Dir, "cook.sqlite")
		conn, _, err := db.Open(dbFile)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			secai.AddErrDB(e, mach, err)
			return
		}
		a.dbConn = conn

		if ctx.Err() != nil {
			return // expired
		}
		mach.Add1(ss.DBReady, nil)
	})
}

func (a *Agent) StepCompletedState(e *am.Event) {
	step := ParseArgs(e.Args).ID
	if rand.Intn(a.Config.Cook.StepCommentFreq) != 0 && a.mem.Has1(step) {
		return
	}

	// skip when some story is about to change (like StoryCookingStarted)
	if a.Mach().WillBe1(ss.StoryChanged) {
		return
	}

	schema := a.mem.Schema()
	comments := a.stepComments.Load()
	if comments == nil {
		a.Log("step comments missing")
		return
	}

	// max index
	idxMax := 0
	for name, state := range schema {
		// TODO enum
		if !strings.HasPrefix(name, "Step") {
			continue
		}
		// TODO enum
		idxMax = max(idxMax, amhelp.TagValueInt(state.Tags, "idx:"))
	}

	// match the index of the step to comments
	state := schema[step]
	idx := -1
	for _, t := range state.Tags {
		if strings.HasPrefix(t, "idx:") {
			var err error
			idx, err = strconv.Atoi(t[4:])
			if err != nil {
				a.Log("invalid idx tag", "tag", t)
				return
			}
			break
		}
	}
	if idx != idxMax && idx >= len(comments.Comments) {
		a.Log("no step comment", "step", step)
		return
	}

	a.Output(comments.Comments[idx], shared.FromAssistant)
	// TODO add to the CookingStarted prompt history
}

func (a *Agent) StepCommentsReadyEnd(e *am.Event) {
	a.stepComments.Store(nil)
}

func (a *Agent) JokesReadyEnd(e *am.Event) {
	a.jokes.Store(&sa.ResultGenJokes{})
}

func (a *Agent) IngredientsReadyEnd(e *am.Event) {
	a.ingredients.Store(&[]sa.Ingredient{})
	err := a.initMem()
	a.Mach().EvAddErrState(e, ss.ErrMem, err, nil)
}

func (a *Agent) ConfigUpdateState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.ConfigUpdate)
	mock := mach.Is1(ss.Mock)

	// call super
	a.AgentLLM.ConfigUpdateState(e)

	// re-check stories
	mach.EvRemove(e, states.CookGroups.Stories, nil)
	mach.EvAdd1(e, ss.CheckStories, nil)
	if mock {
		mach.EvAdd1(e, ss.Mock, nil)
	}

	// save config TODO migrate to https://github.com/calico32/kdl-go?
	cfg := a.Config
	mach.Fork(ctx, e, func() {
		data, err := kdl.Marshal(cfg)
		if err != nil {
			mach.AddErr(err, nil)
			return
		}
		mach.AddErr(os.WriteFile(cfg.File, data, 0644), nil)
	})
}

func (a *Agent) StoryActionEnter(e *am.Event) bool {
	return ParseArgs(e.Args).ID != ""
}

func (a *Agent) StoryActionState(e *am.Event) {
	id := ParseArgs(e.Args).ID
	var action *shared.Action
	for _, key := range a.storiesOrder {
		s := a.stories[key]

		for i := range s.Actions {
			act := &s.Actions[i]
			if act.ID == id {
				action = act
				break
			}
		}
	}

	// TODO move to Enter
	if action == nil {
		a.Mach().EvAddErr(e, fmt.Errorf("action not found: %s", id), nil)
		return
	}

	// execute TODO fork?
	action.Action()
}

func (a *Agent) UIRenderClockState(e *am.Event) {
	a.Store().ClockDiff = ParseArgs(e.Args).ClockDiff
}

// ///// ///// /////

// ///// STORIES

// ///// ///// /////

func (a *Agent) StoryWakingUpState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryWakingUp)
	a.preWakeupSum = mach.Time(states.CookGroups.BootGen).Sum(nil)

	// loop guards
	a.loop = amhelp.NewStateLoop(mach, ss.Loop, nil)

	// unblock and check if DB is fine
	go func() {
		<-mach.When1(ss.DBReady, ctx)

		a.Output("...", shared.FromAssistant)
		genStates := states.CookGroups.BootGen
		chans := make([]<-chan struct{}, len(genStates))
		for i, s := range genStates {
			chans[i] = mach.WhenTime1(s, 1, ctx)
		}
		err := amhelp.WaitForAny(ctx, time.Minute, chans...)
		if err != nil {
			// expiration and timeout errors only
			return
		}

		// one of Gen activated, inform the user
		a.Output("...", shared.FromAssistant)
	}()
}

func (a *Agent) StoryWakingUpEnd(e *am.Event) {
	// announce only if waking up took some time (any related Gen* was triggered)
	postWakeupSum := a.Mach().Time(states.CookGroups.BootGen).Sum(nil)
	if postWakeupSum > a.preWakeupSum {
		_ = a.OutputPhrase("WokenUp")
	}
}

func (a *Agent) StoryIngredientsPickingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryIngredientsPicking)
	llm := a.pIngredientsPicking

	params := sa.ParamsIngredientsPicking{
		MinIngredients: a.Config.Cook.MinIngredients,
	}

	// clean up
	if !a.storyIngredientsPickingCleanup(e) {
		return
	}

	_ = a.OutputPhrase("IngredientsPicking", a.Config.Cook.MinIngredients)
	a.loopIngredients = amhelp.NewStateLoop(mach, ss.StoryIngredientsPicking, func() bool {
		return a.reqLimitOk.Load()
	})

	// unblock
	go func() {
		defer a.Mach().PanicToErr(nil)

		for a.loopIngredients.Ok(nil) {

			// wait for the prompt
			mach.EvAdd1(e, ss.InputPending, nil)
			<-mach.When1(ss.Prompt, ctx)
			if ctx.Err() != nil {
				return // expired
			}
			params.Prompt = a.UserInput

			// run the prompt (checks ctx)
			res, err := llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				return
			}

			// remember
			a.ingredients.Store(&res.Ingredients)

			// if enough, add ingredients to memory and finish
			if len(res.Ingredients) >= a.Config.Cook.MinIngredients {
				schema := a.mem.Schema()
				names := a.mem.StateNames()
				newNames := make([]string, len(res.Ingredients))
				for i, ing := range res.Ingredients {
					if ing.Amount <= 0 {
						mach.EvAddErr(e, fmt.Errorf("amount missing for %s", ing.Name), nil)
						continue
					}
					if len(ing.Name) < 3 {
						continue
					}

					name := "Ingredient" + shared.PascalCase(ing.Name)
					schema[name] = am.State{}
					names = append(names, name)
					newNames[i] = name
				}

				// save mem and the list
				err = a.mem.SetSchema(schema, names)
				if err != nil {
					mach.AddErrState(ss.ErrMem, err, nil)
					return
				}
				// mark the ingredients as active
				a.mem.Add(newNames, nil)
				// TODO DB save?

				// next
				mach.Add1(ss.IngredientsReady, nil)
				break
			}

			msg := fmt.Sprintf("I need at least %d ingredients to continue.", a.Config.Cook.MinIngredients)
			if res.RedoMsg == "" {
				mach.EvAddErr(e, fmt.Errorf("not enough ingredients, but redo msg empty"), nil)
			} else {
				msg = res.RedoMsg
			}
			a.Output(msg, shared.FromAssistant)

			// feed back the current list, update the UI, and go again
			params.Ingredients = res.Ingredients
		}
	}()
}

func (a *Agent) storyIngredientsPickingCleanup(e *am.Event) bool {
	mach := a.Mach()

	mach.EvRemove1(e, ss.IngredientsReady, nil)

	return a.storyRecipePickingCleanup(e)
}

func (a *Agent) StoryIngredientsPickingEnd(e *am.Event) {
	a.pIngredientsPicking.HistClean()
}

func (a *Agent) StoryRecipePickingEnter(e *am.Event) bool {
	ing := a.ingredients.Load()
	return ing != nil && len(*ing) > 0
}

func (a *Agent) StoryRecipePickingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryRecipePicking)
	llm := a.pRecipePicking
	params := sa.ParamsRecipePicking{
		Amount:      a.Config.Cook.GenRecipes,
		Ingredients: *a.ingredients.Load(),
	}

	// clean up
	if !a.storyRecipePickingCleanup(e) {
		return
	}

	// merged phrase
	_ = a.Output(a.Phrase("IngredientsPickingEnd")+" "+a.Phrase("RecipePicking"), shared.FromAssistant)
	a.loopRecipe = amhelp.NewStateLoop(mach, ss.StoryRecipePicking, func() bool {
		return a.reqLimitOk.Load()
	})

	// unblock
	go func() {
		i := 1
		for a.loopRecipe.Ok(nil) {

			// run the prompt (checks ctx)
			res, err := llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				return
			}

			// TODO scrape for correct ImageURL using searxng
			imgUrl := "https://example.com/image.jpg"

			// build the offer list and the msg
			lenRecipes := len(res.Recipes)
			a.OfferList = make([]string, lenRecipes+1)
			tmpl := func(r *sa.Recipe) string {
				return fmt.Sprintf("%s\n   %s", r.Name, imgUrl)
			}
			for i, rec := range res.Recipes {
				a.OfferList[i] = tmpl(&rec)
			}
			a.OfferList[lenRecipes] = tmpl(&res.ExtraRecipe)
			a.Output(res.Summary+"\n\n"+a.BuildOffer(), shared.FromAssistant)

			// ask the user
			mach.EvAdd1(e, ss.InputPending, nil)
			<-mach.When1(ss.Prompt, ctx)
			if ctx.Err() != nil {
				return // expired
			}

			// dereference the prompt TODO extract
			retOffer := make(chan *shared.OfferRef)
			mach.EvAdd1(e, ss.CheckingMenuRefs, Pass3(&A3{
				Prompt:      a.UserInput,
				RetOfferRef: retOffer,
				CheckLLM:    true,
			}))
			var offerIdx int
			select {
			case ret := <-retOffer:
				if ret == nil {
					// TODO next round of recipes
					mach.Log("no offer match, round %d", i+2)
					continue
				}
				offerIdx = ret.Index
			case <-ctx.Done():
				return
			}

			// pick the recipe
			var recipe sa.Recipe
			if offerIdx == len(res.Recipes) {
				recipe = res.ExtraRecipe
			} else {
				recipe = res.Recipes[offerIdx]
			}

			a.recipe.Store(&recipe)
			mach.EvAdd1(e, ss.RecipeReady, nil)
			break
		}
	}()
}

func (a *Agent) storyRecipePickingCleanup(e *am.Event) bool {
	mach := a.Mach()

	// remove recipe
	a.recipe.Store(nil)
	mach.EvRemove1(e, ss.RecipeReady, nil)

	return a.storyCookingStartedCleanup(e)
}

func (a *Agent) StoryRecipePickingEnd(e *am.Event) {
	a.pRecipePicking.HistClean()
}

func (a *Agent) StoryCookingStartedEnter(e *am.Event) bool {
	return a.recipe.Load() != nil
}

func (a *Agent) StoryCookingStartedState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryCookingStarted)
	llm := a.pCookingStarted
	params := sa.ParamsCookingStarted{
		Recipe: *a.recipe.Load(),
	}

	// clean up
	if !a.storyCookingStartedCleanup(e) {
		return
	}

	// chat
	_ = a.OutputPhrase("CookingStarted", a.recipe.Load().Name)
	a.Output(params.Recipe.Name+": "+params.Recipe.Steps, shared.FromNarrator)
	a.loopCooking = amhelp.NewStateLoop(mach, ss.StoryCookingStarted, func() bool {
		return a.reqLimitOk.Load()
	})

	// unblock
	go func() {

		// wait for related prompts
		err := amhelp.WaitForAll(ctx, time.Minute,
			mach.When1(ss.StepsReady, ctx),
			mach.When1(ss.StepCommentsReady, ctx),
		)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}
		params.ExtractedSteps = a.mem.StateNamesMatch(sa.MatchSteps)

		for a.loopCooking.Ok(nil) {
			res := &sa.ResultCookingStarted{}
			var err error

			// wait for a prompt TODO use amhelp?
			mach.EvAdd1(e, ss.InputPending, nil)
			<-mach.When1(ss.InputPending, ctx)
			<-mach.When1(ss.Prompt, ctx)
			a.runOrienting(ctx, e)

			// run the prompt (checks ctx)
			res, err = llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				return
			}

			// wait for orienting to finish
			<-mach.WhenNot1(ss.Orienting, ctx)
			if ctx.Err() != nil {
				return // expired
			}
			move := a.MoveOrienting.Load()
			if move != nil {
				a.MoveOrienting.Store(nil)
				// TODO local prompt
				// a.Output(move.Answer, shared.FromAssistant)
				mach.Add1(ss.OrientingMove, Pass2(&A2{
					Move: move,
				}))
			} else if res.Answer != "" {
				a.Output(res.Answer, shared.FromAssistant)
			}
		}
	}()
}

func (a *Agent) storyCookingStartedCleanup(e *am.Event) bool {
	mach := a.Mach()

	// remove step states
	mach.EvRemove(e, S{ss.StepsReady, ss.StepCompleted}, nil)

	return true
}

func (a *Agent) StoryCookingStartedEnd(e *am.Event) {
	a.pCookingStarted.HistClean()
}

func (a *Agent) StoryJokeEnter(e *am.Event) bool {
	return a.hasJokes()
}

func (a *Agent) StoryJokeState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryJoke)
	// deactivate via ChangeStories, not directly TODO use mach.Schedule
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
		time.Sleep(3 * time.Second)
		a.StoryDeactivate(e, ss.StoryJoke)
	}()

	// untick no-joke-msg
	a.jokeRefusedMsg = false

	// get the 1st joke and forget it
	jokes := a.jokes.Load()
	idx := rand.Intn(len(jokes.Jokes))
	a.Output(jokes.Jokes[idx], shared.FromAssistant)
	jokes.Jokes = slices.Delete(jokes.Jokes, idx, idx+1)
	err := a.Queries().RemoveJoke(ctx, jokes.IDs[idx])
	if err != nil {
		mach.EvAddErrState(e, ss.ErrDB, err, nil)
		return
	}

	// get more if we ran out
	if len(jokes.Jokes) < 1 {
		mach.EvAdd1(e, ss.GenJokes, nil)
	}
}

func (a *Agent) StoryMealReadyState(e *am.Event) {
	_ = a.OutputPhrase(ss.StoryMealReady)
}

func (a *Agent) StoryMemoryWipeState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryMemoryWipe)
	var err error

	// unblock
	mach.Fork(ctx, e, func() {
		defer a.StoryDeactivate(e, ss.StoryMemoryWipe)

		// unset DB
		a.AgentLLM.MemoryWipe(ctx, e)
		err = a.Queries().DeleteAllIngredients(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)
		err = a.Queries().DeleteAllJokes(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)

		// unset mem and boot again
		mach.EvRemove(e, SAdd(
			states.CookGroups.BootGenReady,
			S{ss.StepsReady, ss.StepCommentsReady, ss.RecipeReady, ss.IngredientsReady},
			// TODO tmp fix for no stories unsetting
			am.SRem(states.CookGroups.Stories, S{ss.StoryMemoryWipe}),
		), nil)
		mach.EvAdd1(e, ss.UICleanOutput, nil)
	})
}

func (a *Agent) StoryStartAgainState(e *am.Event) {
	defer a.StoryDeactivate(e, ss.StoryStartAgain)

	// clear and restart
	a.Mach().EvAdd1(e, ss.UICleanOutput, nil)
	a.Mach().EvRemove(e, S{ss.StepsReady, ss.StepCommentsReady, ss.RecipeReady, ss.IngredientsReady}, nil)
	a.StoryActivate(e, ss.StoryIngredientsPicking)
}
