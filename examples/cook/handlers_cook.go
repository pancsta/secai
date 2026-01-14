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

	"github.com/gliderlabs/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai/examples/cook/db"
	sa "github.com/pancsta/secai/examples/cook/schema"
	sabase "github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/tui"
)

func (a *Agent) ExceptionState(e *am.Event) {
	// call base handler
	a.ExceptionHandler.ExceptionState(e)
	args := am.ParseArgs(e.Args)

	// show the error
	a.Output(fmt.Sprintf("ERROR: %s", args.Err), shared.FromSystem)
	a.Logger().Error("error", "err", args.Err)

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
	mach.Add1(ss.UIMode, nil)
	mach.EvAdd1(e, ss.Mock, nil)

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

func (a *Agent) MockEnter(e *am.Event) bool {
	return a.Config.Debug.Mock && mock.Active
}

func (a *Agent) MockState(e *am.Event) {
	mach := a.Mach()

	// first msg when cooking steps are out, right after the narrator's recipe
	if in := mock.StoryCookingStartedInput; in != "" {
		go func() {
			<-mach.When1(ss.InputPending, nil)
			time.Sleep(time.Second)
			mach.EvAdd1(e, ss.Prompt, PassAA(&AA{
				Prompt: in,
			}))
		}()
	}

	// first msg when cooking steps are out, right after the narrator's recipe
	if in := mock.StoryCookingStartedInput3; in != "" {
		go func() {
			<-mach.WhenTime1(ss.InputPending, 3, nil)
			time.Sleep(time.Second)
			mach.EvAdd1(e, ss.Prompt, PassAA(&AA{
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
				mach.EvAdd1(e, ss.Interrupted, PassAA(&AA{
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
	// TODO bind each story directly via wait methods
	skipCalled := S{ss.CheckStories, ss.StoryChanged, ss.Healthcheck, ss.Loop, ss.Requesting,
		ss.RequestingLLM, ss.Heartbeat}
	called := tx.Mutation.CalledIndex(mach.StateNames())
	if a.lastStoryCheck != mtime && called.Not(skipCalled) && !mach.WillBe1(ss.CheckStories) {
		mach.EvAdd1(e, ss.CheckStories, nil)
	}
	a.lastStoryCheck = mtime
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
	for _, s := range a.Stories {
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
		if !s.Cook.Trigger.IsEmpty() {
			activate = s.Cook.Trigger.Check(s.Cook.Mach)
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
		mach.EvAdd1(e, ss.StoryChanged, PassAA(&AA{
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
		s := a.Stories[name]

		if s == nil {
			a.Log("story not found", "state", name)
			continue
		}

		// deactivate
		if mach.Is1(s.State) && !activate {
			res := mach.EvRemove1(e, s.State, nil)

			// TODO handle Queued?
			if res != am.Canceled {
				s.Cook.TimeDeactivated = mach.Time(nil)
				s.Memory.TimeDeactivated = a.mem.Time(nil)
				s.DeactivatedAt = time.Now()
				s.LastActiveTicks = s.Cook.TimeDeactivated.Sum(nil) -
					s.Cook.TimeActivated.Sum(nil)
			}

			// activate
		} else if mach.Not1(s.State) && activate {
			res := mach.EvAdd1(e, s.State, nil)

			// TODO handle Queued?
			if res != am.Canceled {
				s.Cook.TimeActivated = mach.Time(nil)
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
	switch mach.Switch(sa.CookGroups.Interruptable) {
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

func (a *Agent) UISessConnState(e *am.Event) {
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
	uiMain := tui.NewTui(mach, a.Logger(), &a.Config.Config)

	// screen init is required for cview, but not for tview TODO still?
	if err := screen.Init(); err != nil {
		_, _ = fmt.Fprintln(sess.Stderr(), "unable to init screen:", err)
		return
	}

	// init the UI
	stories := tui.NewStories(uiMain, a.storiesButtons(), a.storiesInfo(), a.ConfigBase())
	chat := tui.NewChat(uiMain, slices.Clone(a.Msgs))
	clock := tui.NewClock(uiMain, a.Hist)

	err = uiMain.Init(screen, a.nextUIName(), stories, clock, chat)
	if err != nil {
		a.Mach().EvAddErrState(e, ss.ErrUI, err, nil)
		return
	}

	// register
	a.UIs = append(a.UIs, uiMain)

	// start the UI
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		err = uiMain.Start(sess.Close)
		// TODO log err if not EOF?

		close(done)
		mach.EvAdd1(e, ss.UISessDisconn, nil)
	}()
}

func (a *Agent) UISessDisconnState(e *am.Event) {
	ui := ParseArgs(e.Args).TUI

	a.UIs = shared.SlicesWithout(a.UIs, ui)
}

func (a *Agent) UIModeState(e *am.Event) {
	mach := e.Machine()
	ctx := mach.NewStateCtx(ss.UIMode)

	// new session handler passing to UINewSess state
	var handlerFn ssh.Handler = func(sess ssh.Session) {
		srcAddr := sess.RemoteAddr().String()
		done := make(chan struct{})
		mach.EvAdd1(e, ss.UISessConn, PassAA(&AA{
			SSHSess: sess,
			ID:      sess.User(),
			Addr:    srcAddr,
			Done:    done,
		}))

		// TODO WhenArgs for typed args
		// amhelp.WaitForAll(ctx, time.Hour*9999, mach.WhenArgs(ss.UISessDisconn, am.A{}))

		// keep this session alive
		select {
		case <-ctx.Done():
		case <-done:
		}
	}

	// start the server
	go func() {
		// save srv ref
		optSrv := func(s *ssh.Server) error {
			mach.EvAdd1(e, ss.UISrvListening, PassAA(&AA{
				SSHServer: s,
			}))
			return nil
		}

		addr := a.Config.TUI.Host + ":" + strconv.Itoa(a.Config.TUI.Port)
		a.Log("SSH UI listening", "addr", addr)
		err := ssh.ListenAndServe(addr, handlerFn, optSrv)
		if err != nil {
			mach.EvAddErrState(e, ss.ErrUI, err, nil)
		}
	}()
}

func (a *Agent) UIModeEnd(e *am.Event) {
	// TUIs
	for _, ui := range a.UIs {
		_ = ui.Stop()
	}
	a.UIs = nil

	// SSHs
	if a.srvUI != nil {
		_ = a.srvUI.Close()
	}
}

// TODO enter

func (a *Agent) UISrvListeningState(e *am.Event) {
	s := ParseArgs(e.Args).SSHServer
	a.srvUI = s
}

func (a *Agent) MsgState(e *am.Event) {
	msg := ParseArgs(e.Args).Msg
	a.Msgs = append(a.Msgs, msg)
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
	if int(mach.Time(S{ss.RequestingLLM}).Sum(nil)) > limit {
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
			move := a.moveOrienting.Load()
			if move == nil {
				return
			}
			a.moveOrienting.Store(nil)

			// exec the move
			mach.Add1(ss.OrientingMove, Pass(&A{
				Move: move,
			}))
		}()
	}
}

// InputPendingState is a test mocking handler.
func (a *Agent) InputPendingState(e *am.Event) {
	if !a.Config.Debug.Mock || !mock.Active {
		return
	}

	switch a.Mach().Tick(ss.InputPending) {
	case 1:
		if p := mock.FlowPromptIngredients; p != "" {
			a.Mach().EvAdd1(e, ss.Prompt, PassAA(&AA{Prompt: p}))
		}
	case 3:
		if p := mock.FlowPromptRecipe; p != "" {
			a.Mach().EvAdd1(e, ss.Prompt, PassAA(&AA{Prompt: p}))
		}
	case 5:
		if p := mock.FlowPromptCooking; p != "" {
			a.Mach().EvAdd1(e, ss.Prompt, PassAA(&AA{Prompt: p}))
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
	for _, ui := range a.UIs {
		if ui.MachTUI.Not1(ss.Ready) {
			return false
		}
	}

	return true
}

func (a *Agent) DBStartingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.DBStarting)

	// TODO flaky - timeout & redo
	go func() {
		dbFile := filepath.Join(a.Config.Agent.Dir, "cook.sqlite")
		conn, _, err := db.Open(dbFile)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}
		a.dbConn = conn

		if ctx.Err() != nil {
			return // expired
		}
		mach.Add1(ss.DBReady, nil)
	}()

	// stop on a DB err
	if mach.WillBe1(ss.ErrDB) {
		return
	}
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

func (a *Agent) OrientingState(e *am.Event) {
	mach := a.Mach()
	// use multi-state context here on purpose
	ctx := mach.NewStateCtx(ss.Orienting)
	tick := mach.Tick(ss.Orienting)
	llm := a.pOrienting
	cookSchema := a.Mach().Schema()
	prompt := ParseArgs(e.Args).Prompt

	// possible moves: all cooking steps, most stories and some states

	// moves from stories
	movesStories := map[string]string{}
	for _, name := range mach.StateNames() {
		state := cookSchema[name]

		isStory := strings.HasPrefix(name, "Story") && name != ss.StoryChanged
		isTrigger := amhelp.TagValue(state.Tags, sabase.TagTrigger) != ""
		isManual := amhelp.TagValue(state.Tags, sabase.TagManual) != ""
		// TODO reflect godoc?
		desc := ""
		if s := a.Stories[name]; s != nil && isStory {
			desc = a.Stories[name].Desc
		}

		if isTrigger || (isStory && !isManual) {
			impossible := amhelp.CantAdd1(mach, name, nil)
			if !impossible {
				movesStories[name] = desc
			}
		}
	}

	// collect and filter cooking moves
	movesCooking := a.mem.StateNamesMatch(sa.MatchSteps)
	movesCooking = slices.DeleteFunc(movesCooking, func(state string) bool {
		return amhelp.CantAdd1(a.mem, state, nil)
	})

	// build params
	params := sa.ParamsOrienting{
		Prompt:       prompt,
		MovesCooking: movesCooking,
		MovesStories: movesStories,
	}

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
		// check tail
		defer func() {
			if tick != mach.Tick(ss.Orienting) {
				return
			}
			mach.EvRemove1(e, ss.Orienting, nil)
		}()

		// run the prompt (checks ctx)
		resp, err := llm.Exec(e, params)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrLLM, err, nil)
			return
		}

		if resp.Certainty < 0.8 {
			return
		}
		if tick != mach.Tick(ss.Orienting) {
			return
		}

		// store
		a.moveOrienting.Store(resp)
	}()
}

func (a *Agent) OrientingMoveEnter(e *am.Event) bool {
	args := ParseArgs(e.Args)
	return args.Move != nil
}

func (a *Agent) OrientingMoveState(e *am.Event) {
	mach := a.Mach()
	defer mach.Remove1(ss.OrientingMove, nil)
	args := ParseArgs(e.Args)
	move := args.Move
	resCh := args.Result

	// dispatch the mutation
	m := move.Move
	var res am.Result
	if a.mem.Has1(m) {
		res = a.mem.Add1(m, nil)
		if res == am.Canceled {
			a.Log("2", "move", m)
		}

	} else if _, ok := a.Stories[m]; ok {
		res = a.StoryActivate(e, m)
		if res == am.Canceled {
			a.Log("story canceled", "move", m)
		}

	} else if mach.Has1(m) {
		res = mach.Add1(m, nil)
		if res == am.Canceled {
			a.Log("move canceled", "move", m)
		}
	}

	// optionally return the result
	if args.Result == nil || cap(args.Result) < 1 {
		return
	}

	// channel back (buf)
	select {
	case resCh <- res:
	default:
		mach.Log("OrientingMove chan closed")
	}
}

func (a *Agent) StepCommentsReadyEnd(e *am.Event) {
	a.stepComments.Store(nil)
}

func (a *Agent) CharacterReadyEnd(e *am.Event) {
	a.character.Store(nil)
}

func (a *Agent) ResourcesReadyEnd(e *am.Event) {
	a.resources.Store(nil)
}

func (a *Agent) JokesReadyEnd(e *am.Event) {
	a.jokes.Store(&sa.ResultGenJokes{})
}

func (a *Agent) IngredientsReadyEnd(e *am.Event) {
	a.ingredients.Store(&[]sa.Ingredient{})
	err := a.initMem()
	a.Mach().EvAddErrState(e, ss.ErrMem, err, nil)
}

// ///// ///// /////

// ///// STORIES

// ///// ///// /////

func (a *Agent) StoryWakingUpState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.StoryWakingUp)
	a.preWakeupSum = mach.Time(sa.CookGroups.BootGen).Sum(nil)

	// loop guards
	a.loop = amhelp.NewStateLoop(mach, ss.Loop, nil)

	// unblock and check if DB is fine
	go func() {
		<-mach.When1(ss.DBReady, ctx)

		a.Output("...", shared.FromAssistant)
		genStates := sa.CookGroups.BootGen
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
	postWakeupSum := a.Mach().Time(sa.CookGroups.BootGen).Sum(nil)
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
				mach.EvAddErrState(e, ss.ErrLLM, err, nil)
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
				mach.EvAddErrState(e, ss.ErrLLM, err, nil)
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
			mach.EvAdd1(e, ss.CheckingOfferRefs, PassAA(&AA{
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
				mach.EvAddErrState(e, ss.ErrLLM, err, nil)
				return
			}

			// wait for orienting to finish
			<-mach.WhenNot1(ss.Orienting, ctx)
			if ctx.Err() != nil {
				return // expired
			}
			move := a.moveOrienting.Load()
			if move != nil {
				a.moveOrienting.Store(nil)
				// TODO local prompt
				// a.Output(move.Answer, shared.FromAssistant)
				mach.Add1(ss.OrientingMove, Pass(&A{
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
	go func() {
		defer a.StoryDeactivate(e, ss.StoryMemoryWipe)

		// unset DB
		err = a.Queries().DeleteAllCharacter(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)
		err = a.Queries().DeleteAllIngredients(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)
		err = a.Queries().DeleteAllJokes(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)
		err = a.Queries().DeleteAllResources(ctx)
		mach.EvAddErrState(e, ss.ErrDB, err, nil)

		// unset mem and boot again
		mach.EvRemove(e, SAdd(
			sa.CookGroups.BootGenReady,
			S{ss.StepsReady, ss.StepCommentsReady, ss.RecipeReady, ss.IngredientsReady},
			// TODO tmp fix for no stories unsetting
			am.SRem(sa.CookGroups.Stories, S{ss.StoryMemoryWipe}),
		), nil)
		mach.EvAdd1(e, ss.UICleanOutput, nil)
	}()
}

func (a *Agent) StoryStartAgainState(e *am.Event) {
	defer a.StoryDeactivate(e, ss.StoryStartAgain)

	// clear and restart
	a.Mach().EvAdd1(e, ss.UICleanOutput, nil)
	a.Mach().EvRemove(e, S{ss.StepsReady, ss.StepCommentsReady, ss.RecipeReady, ss.IngredientsReady}, nil)
	a.StoryActivate(e, ss.StoryIngredientsPicking)
}
