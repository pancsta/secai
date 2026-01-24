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

	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	sa "github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/examples/cook/states"
	"github.com/pancsta/secai/shared"
)

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (a *Agent) CharacterReadyState(e *am.Event) {
	// call super
	a.AgentLLM.CharacterReadyState(e)

	a.DocCharacter.AddToPrompts(a.pGenJokes, a.pIngredientsPicking, a.pRecipePicking, a.pGenStepComments)
}

func (a *Agent) GenJokesEnter(e *am.Event) bool {
	return len((*a.jokes.Load()).Jokes) == 0
}

func (a *Agent) RestoreJokesState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreJokes)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

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
	}()
}

func (a *Agent) GenJokesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenJokes)
	llm := a.pGenJokes

	params := sa.ParamsGenJokes{
		Amount: a.Config.Cook.GenJokesAmount,
	}

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// run the prompt (checks ctx)
		res, err := llm.Exec(e, params)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrAI, err, nil)
			return
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
	}()
}

func (a *Agent) GenJokesEnd(e *am.Event) {
	a.pGenJokes.HistClean()
}

func (a *Agent) GenStepCommentsState(e *am.Event) {

	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenStepComments)
	llm := a.pGenStepComments

	// collect final steps
	steps := a.mem.StateNamesMatch(sa.MatchSteps)
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
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		res := &sa.ResultGenStepComments{}
		var err error

		// TODO config
		for i := range 5 {
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

		mach.EvAddErr(e, err, nil)
		a.ValFile(nil, "step-comments", res, "yaml")

		// store and next
		a.stepComments.Store(res)
		mach.EvAdd1(e, ss.StepCommentsReady, nil)
	}()
}

func (a *Agent) GenStepCommentsEnd(e *am.Event) {
	a.pGenStepComments.HistClean()
}

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
		var err error
		// try 5 times TODO config
		for i := range 5 {
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
				res, err = llm.Exec(e, params)
				if ctx.Err() != nil {
					return // expired
				}
				if err != nil {
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
		if err != nil {
			mach.EvAddErrState(e, ss.ErrMem, err, nil)
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

func (a *Agent) StepsReadyState(e *am.Event) {
	memSchema := a.mem.Schema()
	memResolve := a.mem.Resolver()

	// add step buttons, keeping the progress bar (1st button)
	buts := a.stories[ss.StoryCookingStarted].Actions[0:1]
	for _, name := range a.mem.StateNamesMatch(sa.MatchSteps) {
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
			a.Mach().EvAdd(e, S{ss.CheckStories, ss.StepCompleted}, Pass3(&A3{
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
	a.renderStories(e)
}

func (a *Agent) StepsReadyEnd(e *am.Event) {
	mach := a.Mach()

	// reset buttons
	a.stories[ss.StoryCookingStarted].Actions = a.stories[ss.StoryCookingStarted].Actions[0:1]

	// copy ingredients
	ingredientsStates := a.mem.StateNamesMatch(sa.MatchIngredients)
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
	a.renderStories(e)
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
	schema := amhelp.PrefixStates(schemaRAW, "Step", true, nil, nil)
	for _, state := range schema {
		cAfter += amhelp.CountRelations(&state)

		// prefer require over remove TODO fix the other state the opposite way?
		for _, name := range state.Require {
			slices.DeleteFunc(state.Remove, func(s string) bool {
				return s == name
			})
		}
	}

	if cBefore != cAfter {
		err := fmt.Errorf("%w: %d before, %d after", am.ErrSchema, cBefore, cAfter)
		return nil, nil, err
	}

	// merge steps schema into memory
	memSchema := am.SchemaMerge(a.mem.Schema(), schema)
	stepNames := sortSteps(schema)
	newNames := slices.Concat(a.mem.StateNames(), stepNames)

	a.ValFile(nil, "mem", memSchema, "yaml")

	err := validateStepSchema(memSchema, stepNames, newNames)
	if err != nil {
		a.ValFile(nil, "steps-failed", schemaRAW, "yaml")
		return nil, nil, err
	}

	return memSchema, newNames, nil
}
