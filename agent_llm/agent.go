// Package agent_llm is a base agent extended with common LLM prompts.
package agent_llm

import (
	"context"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	"github.com/pancsta/secai"
	sa "github.com/pancsta/secai/agent_llm/schema"
	"github.com/pancsta/secai/shared"
)

type S = am.S

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

// AgentLLM is [secai.AgentBase] extended with common LLM prompts.
type AgentLLM struct {
	*secai.AgentBase

	// prompts

	pCheckingOfferRefs *sa.PromptCheckingOfferRefs
}

func New(ctx context.Context, states am.S, schema am.Schema) *AgentLLM {
	// init the agent along with the base
	return &AgentLLM{
		AgentBase: secai.NewAgent(ctx, states, schema),
	}
}

func (a *AgentLLM) Init(agentImpl secai.AgentAPI, cfg *shared.Config, groups any, states am.States) error {
	// call super
	err := a.AgentBase.Init(agentImpl, cfg, groups, states)
	if err != nil {
		return err
	}

	a.pCheckingOfferRefs = sa.NewPromptCheckingOfferRefs(a)
	return nil
}

// HANDLERS

func (a *AgentLLM) CheckingOfferRefsState(e *am.Event) {
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
		params := sa.ParamsCheckingOfferRefs{
			Choices: shared.Map(choices, func(o string) string {
				return shared.RemoveStyling(o)
			}),
			Prompt: args.Prompt,
		}
		res, err := llm.Exec(e, params)
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
