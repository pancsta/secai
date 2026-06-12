package cook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	sa "github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/examples/cook/states"
	"github.com/pancsta/secai/shared"
)

// TODO config
var retires = 5

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

var _ = ss.CharacterReady

func (a *Agent) CharacterReadyState(e *am.Event) {
	// call super
	a.AgentLLM.CharacterReadyState(e)

	a.DocCharacter.AddToPrompts(a.pGenJokes, a.pIngredientsPicking, a.pRecipePicking, a.pGenStepComments)
}

var _ = ss.GenJokes

func (a *Agent) GenJokesEnter(e *am.Event) bool {
	return len((*a.jokes.Load()).Jokes) == 0
}

var _ = ss.RestoreJokes

func (a *Agent) RestoreJokesState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreJokes)

	mach.Fork(ctx, e, func() {
		// restore
		dbRes, err := a.Queries().GetJokes(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}
		if len(dbRes) == 0 {
			mach.EvAdd1(e, ss.GenJokes, nil)
		} else {

			jokes := &sa.ResultGenJokes{}
			for _, j := range dbRes {
				jokes.Jokes = append(jokes.Jokes, j.Text)
				jokes.IDs = append(jokes.IDs, j.ID)
			}
			a.jokes.Store(jokes)

			// next
			mach.EvAdd1(e, ss.JokesReady, nil)
		}
	})
}

var _ = ss.GenJokes

func (a *Agent) GenJokesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenJokes)
	llm := a.pGenJokes

	params := sa.ParamsGenJokes{
		Amount: a.Config.Cook.GenJokesAmount,
	}

	// unblock
	mach.Fork(ctx, e, func() {
		for range retires {
			// run the prompt (checks ctx)
			res, err := llm.Exec(e, params)
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrAI, err, nil)
				continue
			}

			// persist
			for _, joke := range res.Jokes {
				j, _ := json.Marshal(joke)
				_, err := a.Queries().AddJoke(ctx, string(j))
				if err != nil {
					mach.EvAddErrState(e, ss.ErrDB, err, nil)
					break
				}
			}
			a.jokes.Store(res)

			// next
			mach.EvAdd1(e, ss.JokesReady, nil)
			return
		}

		a.LogErr("GenJokes_too_many_errs", nil)
	})
}

func (a *Agent) GenJokesEnd(e *am.Event) {
	a.pGenJokes.HistClean()
}

var _ = ss.GenStepComments

func (a *Agent) GenStepCommentsState(e *am.Event) {

	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenStepComments)
	llm := a.pGenStepComments

	// collect final steps
	steps := a.mem.StateNames().FilterMatch(sa.MatchSteps)
	mem := a.mem.Schema()
	steps = slices.DeleteFunc(steps, func(s string) bool {
		state := mem[s]
		return slices.Contains(state.Tags, "final")
	})

	// req
	params := sa.ParamsGenStepComments{
		Recipe: *a.recipe.Load(),
		Steps:  steps,
	}

	// unblock
	mach.Fork(ctx, e, func() {
		res := &sa.ResultGenStepComments{}
		var err error
		for i := range retires {
			if i > 0 {
				a.Log("GenStepCommentsState", "try", i)
			}

			// mock schema
			if mock.GenStepCommentsRes != "" {
				err = json.Unmarshal([]byte(mock.GenStepCommentsRes), res)
				if err != nil {
					mach.EvAddErr(e, err, nil)
					continue
				}

				// run the prompt (checks ctx)
			} else {
				res, err = llm.Exec(e, params)
				if ctx.Err() != nil {
					return // expired
				}
				if err != nil {
					mach.EvAddErrState(e, ss.ErrAI, err, nil)
					continue
				}
			}

			// validate
			if len(res.Comments) < len(steps)/2 {
				err = fmt.Errorf("not enough comments: %d < %d/2", len(res.Comments), len(steps))
				continue
			}

			// clean up
			for i := range res.Comments {
				for _, s := range steps {
					res.Comments[i] = strings.TrimPrefix(res.Comments[i], s+": ")
				}
			}
		}

		if err != nil {
			a.LogErr("GenStepComments_too_many_errs", nil)
			mach.EvAddErr(e, err, nil)
			return
		}

		// store and next
		a.ValFile(nil, "step-comments", res, "yaml")
		a.stepComments.Store(res)
		mach.EvAdd1(e, ss.StepCommentsReady, nil)
	})
}

func (a *Agent) GenStepCommentsEnd(e *am.Event) {
	a.pGenStepComments.HistClean()
}

var _ = ss.GenSteps

func (a *Agent) GenStepsEnter(e *am.Event) bool {
	recipe := a.recipe.Load()
	return recipe != nil
}

func (a *Agent) GenStepsState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenSteps)
	llm := a.pGenSteps
	params := sa.ParamsGenSteps{
		Recipe: *a.recipe.Load(),
	}
	a.Log("GenSteps_initial_schema", "memSchema", a.mem.Schema())

	// unblock
	mach.Fork(ctx, e, func() {
		var stateErr error
		for i := range retires {
			res := &sa.ResultGenSteps{}
			if i > 0 {
				a.Log("GenSteps", "try", i)
			}

			// mock schema
			if mock.GenStepsRes != "" {
				err := json.Unmarshal([]byte(mock.GenStepsRes), res)
				if err != nil {
					mach.EvAddErr(e, err, nil)
					continue
				}

				// live schema
			} else {
				// run the prompt (checks ctx)
				var err error
				res, err = llm.Exec(e, params)
				if ctx.Err() != nil {
					return // expired
				}
				if err != nil {
					stateErr = err
					mach.EvAddErrState(e, ss.ErrAI, err, nil)
					continue
				}
			}

			memSchema, newNames, err := a.processStepSchema(ctx, res)

			// try to set if OK
			if err == nil {
				err = a.mem.SetSchema(memSchema, newNames)
			}

			// handle both errs
			if err != nil {
				stateErr = err
				a.LogErr("GenSteps_bad_schema", err,
					"schema", memSchema,
					"states", newNames,
				)

				// try again
				continue
			}

			// next
			mach.EvAdd1(e, ss.StepsReady, nil)
			mach.EvAdd1(e, ss.CheckStories, nil)
			break
		}

		// check err
		if stateErr != nil {
			mach.EvAddErrState(e, ss.ErrMem, stateErr, nil)
			// TODO ErrStepsState
			a.LogErr("GenSteps_too_many_errs", nil)
			// TODO phrase resource +config +another recipe choice
			a.Output("Unable to generate cooking steps after 5 tries :(", shared.FromAssistant)
			return
		}
	})
}

func (a *Agent) GenStepsEnd(e *am.Event) {
	a.pGenSteps.HistClean()
}

var _ = ss.StepsReady

func (a *Agent) StepsReadyState(e *am.Event) {
	memSchema := a.mem.Schema()
	memResolve := a.mem.Resolver()

	// add step buttons, keeping the progress bar (1st button)
	buts := a.stories[ss.StoryCookingStarted].Actions[0:1]
	for _, name := range a.mem.StateNames().FilterMatch(sa.MatchSteps) {
		s := memSchema[name]
		but := shared.Action{
			ID: amhelp.RandId(8),
		}

		// step number
		num := amhelp.TagValue(s.Tags, "idx")

		// check Require and take it's number +1
		if num == "" && s.Require != nil && len(s.Require) > 0 {
			req := memSchema[s.Require[0]]
			num = amhelp.TagValue(req.Tags, "idx")
			but.PosInferred = true
		}
		iNum, _ := strconv.Atoi(num)
		but.Pos = iNum
		if num != "" {
			num = "(" + num + ") "
		}

		// button
		if name == states.MemMealReady {
			continue
		}
		label, _ := strings.CutPrefix(name, "Step")
		label = num + shared.RevertPascalCase(label)
		but.Label = label
		but.Desc = "Press this button when the step \"" + label + "\" is done"

		// click action
		but.Action = func() {
			res := a.mem.EvAdd1(e, name, nil)
			if res == am.Canceled {
				return
			}
			a.Mach().EvAdd(e, S{ss.CheckStories, ss.StepCompleted}, am.Pass(&AStepCompleted{
				ID: name,
			}))
		}

		// disable clicked
		but.IsDisabled = func() bool {
			// disable when a state which removes this one is active AND FINAL
			fromNames, _ := memResolve.InboundRelationsOf(name)
			for _, fromName := range fromNames {
				fromState := memSchema[fromName]
				if slices.Contains(fromState.Remove, name) && a.mem.Is1(fromName) &&
					amhelp.TagValue(fromState.Tags, "final") != "" {

					return true
				}
			}

			// disable when active
			return a.mem.Is1(name)
		}

		but.VisibleAgent = amhelp.Cond{
			Is:  S{ss.StoryCookingStarted},
			Not: S{ss.StoryMealReady},
		}

		buts = append(buts, but)
	}

	// sort buttons by idx:\d
	sort.Sort(shared.StoryActionsByIdx(buts))

	// update the story with new buttons
	a.stories[ss.StoryCookingStarted].Actions = buts
	a.hRenderStories(e)
}

func (a *Agent) StepsReadyEnd(e *am.Event) {
	mach := a.Mach()

	// reset buttons
	a.stories[ss.StoryCookingStarted].Actions = a.stories[ss.StoryCookingStarted].Actions[0:1]

	// copy ingredients
	ingredientsStates := a.mem.StateNames().FilterMatch(sa.MatchIngredients)
	oldSchema := a.mem.Schema()
	// start with an empty schema
	err := a.initMem()
	if err != nil {
		mach.EvAddErrState(e, ss.ErrMem, err, nil)
		return
	}
	err = amhelp.CopySchema(oldSchema, a.mem, ingredientsStates)
	// dont stop on err
	mach.EvAddErrState(e, ss.ErrMem, err, nil)

	// remove stories UI
	a.hRenderStories(e)
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (a *Agent) processStepSchema(ctx context.Context, res *sa.ResultGenSteps) (am.Schema, am.S, error) {
	// TODO prevent clicking MealReady
	//

	schemaRAW := res.Schema
	a.ValFile(nil, "steps", schemaRAW, "yaml")

	// prefix and checksum the schema TODO why count?
	cBefore := 0
	cAfter := 0
	for _, state := range schemaRAW {
		cBefore += amhelp.CountRelations(&state)
	}
	// prefix state names
	schema := schemaRAW.Prefix("Step", true, nil, nil)

	// fix relations
	idxLast := 0
	for name, state := range schema {
		if name == ss.StoryMealReady {
			continue
		}
		cAfter += amhelp.CountRelations(&state)

		// prefer require over remove
		schema[name] = state.SetRels(am.State{
			Require: state.Require.Sub(schema[name].Remove),
		})

		// find last step idx
		num := amhelp.TagValueInt(state.Tags, "idx")
		if num > idxLast {
			idxLast = num
		}
	}

	// all same-indexes remove each other
	for i := range idxLast + 1 {
		states := schema.FilterByTag("idx:" + strconv.Itoa(i)).Names()
		for _, s := range states {
			schema[s] = schema[s].SetRels(am.State{
				Remove: states,
			})

			// next index can only require a #final from lower indexes
			if i == 0 {
				continue
			}
			for ii, s2 := range schema[s].Require {
				target := schema[s2]
				targetIdx := amhelp.TagValue(target.Tags, "idx")
				targetSiblings := schema.FilterByTag("idx:" + targetIdx)
				if !target.HasTag("final") && len(targetSiblings) > 0 {
					finalNames := targetSiblings.FilterByTag("final").Names()
					if len(finalNames) > 0 {
						schema[s].Require[ii] = finalNames[0]
					}
				}
			}
		}
	}

	if cBefore != cAfter {
		err := fmt.Errorf("%w: %d before, %d after", am.ErrSchema, cBefore, cAfter)
		return nil, nil, err
	}

	// merge steps schema into memory
	memSchema := a.mem.Schema().Merge(schema)
	stepNames := sortSteps(schema)
	newNames := a.mem.StateNames().Add(stepNames)

	a.ValFile(nil, "mem", memSchema, "yaml")

	err := a.validateStepSchema(memSchema, stepNames, newNames)
	if err != nil {
		a.ValFile(nil, "steps-failed", schemaRAW, "yaml")
		return nil, nil, err
	}

	return memSchema, newNames, nil
}

func (a *Agent) cookingSteps() (am.S, error) {
	if a.mem == nil {
		return nil, fmt.Errorf("no memory mach")
	}
	return sortSteps(a.mem.Schema()), nil
}

func (a *Agent) validateStepSchema(schema am.Schema, stepStates, allStates am.S) error {
	// TODO check min steps amount

	// check if going 1,2,3..n will end on MealReady
	mach := am.New(context.Background(), schema, &am.Opts{
		Id:     a.Mach().Id() + "-steptest-" + stepStates.Hash(),
		Parent: a.Mach(),
	})
	mach.SetGroupsString(map[string]S{
		"Steps":       stepStates,
		"Ingredients": allStates.FilterMatch(sa.MatchIngredients),
	}, []string{"Steps"})
	if a.Config.Debug.DBGAddr != "" {
		addr, _, _, _ := a.Config.Debug.DbgAddrs()
		if addr != "" {
			amhelp.MachDebug(mach, addr, a.Config.Agent.Log.MachLevel, false, amhelp.SemConfigEnv(true))
		}
	}
	var err error
	for _, step := range stepStates {
		if step == states.MemMealReady {
			continue
		}
		if mach.Add1(step, nil) == am.Canceled {
			err = fmt.Errorf("%s canceled", step)
			break
		}
	}

	// flush dbg
	if a.Config.Debug.DBGAddr != "" {
		time.Sleep(time.Second)
	}

	if !mach.Is1(states.MemMealReady) {
		return fmt.Errorf("step schema is invalid: %w", err)
	}

	mach.Dispose()

	return nil
}
