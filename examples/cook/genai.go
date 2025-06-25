package cook

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/brianvoe/gofakeit/v7"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"gopkg.in/yaml.v3"

	"github.com/pancsta/secai"
	"github.com/pancsta/secai/examples/cook/db"
	"github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/shared"
)

// ///// ///// /////

// ///// GEN AI

// ///// ///// /////

func (a *Agent) RestoreCharacterState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreCharacter)

	go func() {
		dbChar, err := a.Queries().GetCharacter(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}

		// generate a new character
		if dbChar.Result == "" {
			mach.EvAdd1(e, ss.GenCharacter, nil)
			return
		}

		// unmarshal JSON result into ResultGenCharacter
		var res schema.ResultGenCharacter
		if err := json.Unmarshal([]byte(dbChar.Result), &res); err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		// store and next
		a.character.Store(&res)
		mach.EvAdd1(e, ss.CharacterReady, nil)
	}()
}

func (a *Agent) GenCharacterState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenCharacter)
	llm := a.pGenCharacter

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// run the prompt (checks ctx)
		// TODO gen profession via LLM (age accurate)
		params := schema.ParamsGenCharacter{
			CharacterProfession: gofakeit.JobTitle(),
			CharacterYear:       rand.Intn(120) + 1900,
		}
		res, err := llm.Run(e, params, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrLLM, err, nil)
			return
		}

		// persist
		jResult, _ := json.Marshal(res)
		_, err = a.Queries().AddCharacter(ctx, string(jResult))
		if err != nil {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			// DB err is OK
		}
		a.character.Store(res)

		// next
		mach.EvAdd1(e, ss.CharacterReady, nil)
	}()
}

func (a *Agent) GenCharacterEnd(e *am.Event) {
	a.pGenCharacter.HistCleanOpenAI()
}

func (a *Agent) CharacterReadyEnter(e *am.Event) bool {
	return a.character.Load() != nil
}

func (a *Agent) CharacterReadyState(e *am.Event) {
	char := a.character.Load()
	j, _ := yaml.Marshal(char)

	// attach to prompts which depend on the character
	doc := secai.NewDocument("Character", string(j))
	doc.AddToPrompts(a.pGenJokes, a.pIngredientsPicking, a.pRecipePicking, a.pGenResources, a.pGenStepComments,
		a.pOrienting)

	msg := fmt.Sprintf("Your host will be %s from %d. Profession: %s.", char.Name, char.Year, char.Profession)
	a.Output(msg, shared.FromNarrator)
	_ = a.OutputPhrase("CharacterReady")
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

			jokes := &schema.ResultGenJokes{}
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

func (a *Agent) GenJokesEnter(e *am.Event) bool {
	return len((*a.jokes.Load()).Jokes) == 0
}

func (a *Agent) GenJokesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenJokes)
	llm := a.pGenJokes

	params := schema.ParamsGenJokes{
		Amount: a.Config.GenJokesAmount,
	}

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// run the prompt (checks ctx)
		res, err := llm.Run(e, params, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErrState(e, ss.ErrLLM, err, nil)
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
	a.pGenJokes.HistCleanOpenAI()
}

func (a *Agent) RestoreResourcesState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.RestoreResources)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// restore
		dbRes, err := a.Queries().GetResources(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			mach.EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}
		if len(dbRes) == 0 {
			mach.EvAdd1(e, ss.GenResources, nil)
		} else {

			res := &schema.ResultGenResources{
				Phrases: make(map[string][]string),
			}
			for _, r := range dbRes {
				res.Phrases[r.Key] = append(res.Phrases[r.Key], r.Value)
			}
			a.resources.Store(res)

			// next
			mach.EvAdd1(e, ss.ResourcesReady, nil)
		}
	}()
}

func (a *Agent) GenResourcesEnter(e *am.Event) bool {
	return a.resources.Load() == nil
}

func (a *Agent) GenResourcesState(e *am.Event) {
	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenResources)
	llm := a.pGenResources

	params := schema.LLMResources

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// run the prompt (checks ctx)
		res, err := llm.Run(e, params, "")
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			mach.EvAddErr(e, err, nil)
			return
		}

		// persist
		for key, phrases := range res.Phrases {
			for _, p := range phrases {
				_, err := a.Queries().AddResource(ctx, db.AddResourceParams{
					Key:   key,
					Value: p,
				})
				if err != nil {
					mach.EvAddErrState(e, ss.ErrDB, err, nil)
					break
				}
			}
		}
		a.resources.Store(res)

		// next
		mach.EvAdd1(e, ss.ResourcesReady, nil)
	}()
}

func (a *Agent) GenResourcesEnd(e *am.Event) {
	a.pGenResources.HistCleanOpenAI()
}

func (a *Agent) GenStepCommentsState(e *am.Event) {

	// collect
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenStepComments)
	llm := a.pGenStepComments
	params := schema.ParamsGenStepComments{
		Recipe: *a.recipe.Load(),
		Steps:  a.mem.StateNamesMatch(schema.MatchSteps),
	}

	// unblock
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		res := &schema.ResultGenStepComments{}
		var err error
		// mock schema
		if mock.GenStepCommentsRes != "" {
			err := json.Unmarshal([]byte(mock.GenStepCommentsRes), res)
			if err != nil {
				mach.EvAddErr(e, err, nil)
				return
			}

			// run the prompt (checks ctx)
		} else {
			res, err = llm.Run(e, params, "")
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErr(e, err, nil)
				return
			}
		}
		// TODO validate

		// store and next
		a.stepComments.Store(res)
		mach.EvAdd1(e, ss.StepCommentsReady, nil)
	}()
}

func (a *Agent) GenStepCommentsEnd(e *am.Event) {
	a.pGenStepComments.HistCleanOpenAI()
}

func (a *Agent) GenStepsState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.GenSteps)
	llm := a.pGenSteps
	params := schema.ParamsGenSteps{
		Recipe: *a.recipe.Load(),
	}

	// unblock
	go func() {
		// i := 1
		// TODO amhelp.Loop()
		// for {
		res := &schema.ResultGenSteps{}
		var err error

		// mock schema
		if mock.GenStepsRes != "" {
			err := json.Unmarshal([]byte(mock.GenStepsRes), res)
			if err != nil {
				mach.EvAddErr(e, err, nil)
				return
			}

			// live schema
		} else {
			// run the prompt (checks ctx)
			res, err = llm.Run(e, params, "")
			if ctx.Err() != nil {
				return // expired
			}
			if err != nil {
				mach.EvAddErrState(e, ss.ErrLLM, err, nil)
				return
			}
		}

		// prefix and checksum the schema
		cBefore := 0
		cAfter := 0
		for _, state := range res.Schema {
			cBefore += amhelp.CountRelations(&state)
		}
		// prefix state names
		res.Schema = amhelp.PrefixStates(res.Schema, "Step", true, nil, nil)
		for _, state := range res.Schema {
			cAfter += amhelp.CountRelations(&state)
		}

		if cBefore != cAfter {
			mach.EvAddErr(e, fmt.Errorf("%w: %d before, %d after", am.ErrSchema, cBefore, cAfter), nil)
			return
		}

		// merge steps schema into memory
		memSchema := am.SchemaMerge(a.mem.Schema(), res.Schema)
		stateNames := sortSteps(memSchema)
		err = a.mem.SetSchema(memSchema, slices.Concat(a.mem.StateNames(), stateNames))
		if err != nil {
			mach.EvAddErrState(e, ss.ErrMem, err, nil)
			return
		}

		mach.Add1(ss.StepsReady, nil)
		// 	break
		// }

		// re-render and wait for completed
		mach.Add1(ss.CheckStories, nil)
	}()
}

func (a *Agent) GenStepsEnd(e *am.Event) {
	a.pGenSteps.HistCleanOpenAI()
}

func (a *Agent) StepsReadyState(e *am.Event) {
	memSchema := a.mem.Schema()
	memResolve := a.mem.Resolver()

	// add step buttons, keeping the progress bar (1st button)
	buts := a.Stories[ss.StoryCookingStarted].Buttons[0:1]
	for _, name := range a.mem.StateNamesMatch(schema.MatchSteps) {
		s := memSchema[name]
		but := shared.StoryButton{}

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
		if name == schema.MemMealReady {
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
			a.Mach().EvAdd(e, S{ss.CheckStories, ss.StepCompleted}, PassAA(&AA{
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

		but.VisibleCook = amhelp.Cond{
			Is:  S{ss.StoryCookingStarted},
			Not: S{ss.StoryMealReady},
		}

		buts = append(buts, but)
	}

	// sort buttons by idx:\d
	sort.Sort(shared.StoryButsByIdx(buts))

	// update the story with new buttons
	a.Stories[ss.StoryCookingStarted].Buttons = buts
	a.renderStories(e)
}

func (a *Agent) StepsReadyEnd(e *am.Event) {
	mach := a.Mach()

	// reset buttons
	a.Stories[ss.StoryCookingStarted].Buttons = a.Stories[ss.StoryCookingStarted].Buttons[0:1]

	// copy ingredients TODO extract as amhelp.CopySchema(states)
	oldSchema := a.mem.Schema()
	ingredientsStates := a.mem.StateNamesMatch(schema.MatchIngredients)

	err := a.initMem()
	newSchema := a.mem.Schema()
	if err != nil {
		mach.EvAddErrState(e, ss.ErrMem, err, nil)
		return
	}
	if len(ingredientsStates) == 0 {
		return
	}

	for _, name := range ingredientsStates {
		newSchema[name] = oldSchema[name]
	}
	err = a.mem.SetSchema(newSchema, slices.Concat(a.mem.StateNames(), ingredientsStates))
	mach.EvAddErrState(e, ss.ErrMem, err, nil)

	// remove stories UI
	a.renderStories(e)
}
