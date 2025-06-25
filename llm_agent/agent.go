// Package llmagent is a base agent extended with common LLM prompts.
package llmagent

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/secai"
	"github.com/pancsta/secai/llm_agent/schema"
	"github.com/pancsta/secai/shared"
)

type S = am.S

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

type Agent struct {
	*secai.Agent

	// prompts

	pCheckingOfferRefs *schema.PromptCheckingOfferRefs
}

func New(ctx context.Context, id string, states am.S, machSchema am.Schema) *Agent {
	// init the agent along with the base
	return &Agent{
		Agent: secai.NewAgent(ctx, id, states, machSchema),
	}
}

func (a *Agent) Init(agent secai.AgentAPI) error {
	// call super
	err := a.Agent.Init(agent)
	if err != nil {
		return err
	}

	a.pCheckingOfferRefs = schema.NewPromptCheckingOfferRefs(a)
	return nil
}

// HANDLERS

func (a *Agent) CheckingOfferRefsState(e *am.Event) {
	args := shared.ParseArgs(e.Args)

	prompt := args.Prompt
	choices := a.OfferList
	if len(args.Choices) > 0 {
		choices = args.Choices
	}
	retCh := args.RetOfferRef
	llm := a.pCheckingOfferRefs

	// unblock
	go func() {
		// deferred chan return
		var ret *shared.OfferRef
		defer func() {
			retCh <- ret
		}()

		foundFn := func(i int) *shared.OfferRef {
			if i >= len(choices) {
				return nil
			}
			text := choices[i]
			return &shared.OfferRef{
				Index: i,
				Text:  shared.RemoveStyling(text),
			}
		}

		// infer locally (from 1-based to 0-based)
		i := shared.NumRef(prompt)
		if i >= 0 && i <= len(choices) {
			ret = foundFn(i - 1)
			return
		}

		if !args.CheckLLM {
			return
		}

		// infer via LLM
		params := schema.ParamsCheckingOfferRefs{
			Choices: shared.Map(choices, func(o string) string {
				return shared.RemoveStyling(o)
			}),
			Prompt: args.Prompt,
		}
		res, err := llm.Run(e, params, "")
		if err != nil {
			a.Mach().AddErr(err, nil)
			return
		}
		if res.RefIndex >= 0 && res.RefIndex < len(choices) {
			ret = foundFn(res.RefIndex)
			return
		}
	}()
}

// TODO PromptState(e) which checks for unexpected msgs (no InputPending) and compares all states
