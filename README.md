# secai

**secai** is a Golang framework for _Reasoning AI Workflows_, implemented using a unique state machine which thrives in
complexity. It's a solid foundation for complex, proactive, and **long-lived AI Agents** with deep and structured
memory. Each bot ships with embedded devtools and several UIs. The execution flow is graph-based, which allows for
precise behavior modeling of agents, including interruptions, concurrency, and fault tolerance.

It's a sophisticated replacement for frameworks like LangGraph and offers deeply relational consensus of state.

* Demos
  * [v0.5 - Fully Embedded](#v05---fully-embedded)
  * [v0.4 - Local One](#v04---catch-up)
  * [v0.2 - User Demo](#v02---user-demo)
  * [v0.1 - Platform Demo](#v01---platform-demo)
* [Features](#features)
  * [Implementation](#implementation)
* [Try It](#try-it)
  * [Schema Examples](#schema-examples)
* [Documentation](#documentation)
* [Getting Started](#getting-started)
* [Scripting](#bash-scripts)

## v0.5 - Fully Embedded

[Live debugger](http://ai-gents.work.local:15834/dbg-cook) | [Live SQL](http://ai-gents.work.local:15834/data-cook) | [Read logs](http://ai-gents.work.local:15834/demo/cook.html)
 | [Browse files](http://ai-gents.work.local:15834/demo)

<div align="center" class="video">
    <a href="http://ai-gents.work.local:15834/assets/demo/imgs/demo.gif">
        <img width="420px"
            src="http://ai-gents.work.local:15834/demo/imgs/demo.gif"
            alt="AI-gent Cook" />
    </a>
</div>

<table>

  <tr>
    <td align="center">Debugger</td>
    <td align="center">REPL</td>
    <td align="center">DB</td>
    <td align="center">DB</td>
    <td align="center">Log</td>
  </tr>
  <tr>
    <td align="center">
        <img src="http://ai-gents.work.local:15834/demo/imgs/am-dbg.png"/>
    </td>
    <td align="center">
        <img src="http://ai-gents.work.local:15834/demo/imgs/repl.png"/>
    </td>
    <td align="center">
        <img src="http://ai-gents.work.local:15834/demo/imgs/db1.png"/>
    </td>
    <td align="center">
        <img src="http://ai-gents.work.local:15834/demo/imgs/db2.png"/>
    </td>
    <td align="center">
        <img src="http://ai-gents.work.local:15834/demo/imgs/log.png"/>
    </td>
  </tr>
</table>

<details>
  <summary>Click to see the diagram</summary>

<img src="http://ai-gents.work.local:15834/demo/_diagram.svg" />
</details>

## v0.4 - Local One

AI-gent Cook on _qwen3-vl-30b_

<div align="center" class="video">
    <a href="http://ai-gents.work.local:15834/assets/demo-v0.4/_demo.gif">
        <img width="420px"
            src="https://github.com/user-attachments/assets/a387b00f-c2ba-444c-9b58-381d1425edfb"
            alt="AI-gent Cook" />
    </a>
</div>

<details>
  <summary>Click to see the diagram</summary>

<img src="http://ai-gents.work.local:15834/assets/demo-v0.4/_diagram.svg" />
</details>

## v0.2 - User Demo

Screenshots and [YouTube](https://youtu.be/rbwXg64poBE) are also available.

<p align="center"><a href="https://pancsta.github.io/assets/secai/demo2/secai-demo2.mp4"><img src="https://pancsta.github.io/assets/secai/demo2/demo2.png?"></a></p>

> [!NOTE]
> User demo (7m captions-only) showcasing a cooking assistant which helps to pick a recipe from ingredients AND cook it.

<details>
  <summary>Click to see screenshots</summary>

<table>

  <tr>
    <td align="center">Intro</td>
    <td align="center">AI-gent Cook</td>
    <td align="center">Debugger 1</td>
    <td align="center">Debugger 2</td>
    <td align="center">Memory & Stories</td>
  </tr>
  <tr>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/1.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/2.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/3.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/4.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/5.png"/>
    </td>
  </tr>

  <tr>
    <td align="center">User Interfaces</td>
    <td align="center">Outro</td>
    <td align="center">&nbsp;</td>
    <td align="center">&nbsp;</td>
    <td align="center">&nbsp;</td>
  </tr>
  <tr>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/6.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo2/7.png"/>
    </td>
    <td align="center">
        &nbsp;
    </td>
    <td align="center"> 
        &nbsp;
    </td>
    <td align="center">
        &nbsp;
    </td>
  </tr>

  <tr>
    <td align="center" colspan="5">Diagram</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/cook.svg"/>
    </td>
  </tr>

</table>
</details>

## v0.1 - Platform Demo

Screenshots and [YouTube](https://youtu.be/0VJzO1S-gV0) are also available.

<p align="center"><a href="https://pancsta.github.io/assets/secai/demo1/secai-demo1.mp4"><img src="https://pancsta.github.io/assets/secai/demo1/demo1.png"></a></p>

> [!NOTE]
> Platform demo (5m captions-only), showcasing all nine ways an agent can be seen, in addition to the classic chat view.

<details>
  <summary>Click to see screenshots</summary>

<table>

  <tr>
    <td align="center">SVG graph</td>
    <td align="center">am-dbg</td>
    <td align="center">Grafana</td>
    <td align="center">Jaeger</td>
    <td align="center">REPL</td>
  </tr>
  <tr>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/1.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/2.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/3.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/4.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/5.png"/>
    </td>
  </tr>

  <tr>
    <td align="center">SQL</td>
    <td align="center">IDE</td>
    <td align="center">Bash</td>
    <td align="center">Prompts</td>
    <td align="center">&nbsp;</td>
  </tr>
  <tr>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/6.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/7.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/8.png"/>
    </td>
    <td align="center">
        <img src="https://pancsta.github.io/assets/secai/demo1/9.png"/>
    </td>
    <td align="center">
        &nbsp;
    </td>
  </tr>

  <tr>
    <td align="center" colspan="5">Diagram</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/deepresearch.svg"/>
    </td>
  </tr>

</table>
</details>

## Features

- multi-prompt agency
  - a single agent/bot is built of several AI prompts
  - each state can have a prompt bound to it, with dedicated history and documents
- atomic consensus with relations and negotiation
  - eg states excluding each other can't be active simultaneously
- dedicated DSL layer for bot schema 
  - suitable for non-coding authors
- structured prompt input/output via JSON schemas
- declarative flow definitions for non-linear flows
- cancellation support (interrupts)
- choice menu (list of offers)
- prompt history
  - embedded SQLite
  - JSONL log
  - "latest prompt" files
- proactive stories with actors
  - stories have actions and progress
- LLM-sourced story switching (orienting)
  - on prompts and timeouts
- dynamic flow graph for the memory
  - LLM creates an actionable state-machine
- TUIs and WebAssembly PWAs for user interfaces

### Goals

- precision
- correctness
- granular debugging

### Devtools

All devtools are available on the web, some also as TUIs, and some as regular files. Everything is shipped as a single
file.

- debugger
- REPL
- diagrams (D2 SVGs)
- DB browser
- log viewer
- observability (OpenTelemetry, Prometheus, Loki)

### Tools

- websearch (dockerized [searxng](https://github.com/searxng/searxng))
- HTML scrape (embedded [colly](https://github.com/gocolly/colly))

## Implementation

- Golang & WASM
- [asyncmachine-go](https://asyncmachine.dev) for graph control flow
- [instructor-go](https://github.com/instructor-ai/instructor-go) for AI APIs
- [invopop/jsonschema](https://github.com/invopop/jsonschema) for prompt schemas
- [aRPC](https://asyncmachine.dev/rpc) for network transparency
  - a single agent can span across multiple servers and browsers
- [cview](https://codeberg.org/tslocum/cview) for TUIs
- [go-app](https://go-app.dev/) for web UIs
- [ncruces](https://github.com/ncruces/go-sqlite3) for SQLite

### Architecture

- Agent (actor)
  - state-machine schema
  - prompts
  - tools
- Tool (actor)
  - state-machine schema
- Memory
  - state-machine schema
- Prompt (state)
  - params schema
  - result schema
  - history log
  - documents
- Stories (state)
  - actors (state machines)
  - actions
  - progress
- Document
  - title
  - content

## Try It

- [Download a binary release](https://github.com/pancsta/secai/releases) of AI-gent Cook (Linux, macOS, Windows)

```markdown
AI-gent Cook v0.5.0

Web:
- http://localhost:12854
- http://localhost:12854/agent

Files:
- config: config.kdl
- log:    tmp-cook/cook.jsonl

TUI:
- http://localhost:7856
- ssh localhost -p 7955 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no

REPL:
- http://localhost:13179
- ./cook repl --config config.kdl

Log:
- http://localhost:12858
- ./cook log --tail --config config.kdl
- tail -f tmp-cook/cook.jsonl -n 100 | fblog -d -x msg -x time -x level

Debugger:
- http://localhost:13178
- files: http://localhost:13171
- ssh localhost -p 13172 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no

DB:
- Base: http://localhost:13180
- Agent: http://localhost:13181
- History: http://localhost:13182

https://AI-gents.work
```
## Schema Examples

Code snippets from state and prompt schemas of `examples/cook`. Both schemas are pure and debuggable Golang code.

### State Schema

```go
// CookStatesDef contains all the states of the Cook state machine.
type CookStatesDef struct {
	*am.StatesBase

	// ...

	// prompts

	RestoreJokes string
	GenJokes     string
	JokesReady   string

	GenSteps string
	// StepsReady implies the steps have been translated into actionable memory.
	StepsReady string

	GenStepComments   string
	StepCommentsReady string

	// inherit from LLM AgentLLM
	*ssllm.AgentLLMStatesDef
}

// ...

// CookSchema represents all relations and properties of CookStates.
var CookSchema = SchemaMerge(
	// inherit from LLM AgentLLM
	ssllm.LLMAgentSchema,
	am.Schema{
        
		// gen AI

		ssC.RestoreJokes: {
			Auto:    true,
			Require: S{ssC.DBReady, ssC.CharacterReady},
			Remove:  sgC.Jokes,
		},
		ssC.GenJokes: {
			Require: S{ssC.CharacterReady, ssC.DBReady},
			Remove:  sgC.Jokes,
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.JokesReady: {Remove: sgC.Jokes},

		ssC.GenSteps: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted},
			Remove:  S{ssC.StepsReady},
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.StepsReady: {
			Require: S{ssC.RecipeReady},
			Remove:  S{ssC.GenSteps},
		},

		ssC.GenStepComments: {
			Auto:    true,
			Require: S{ssC.StoryCookingStarted, ssC.StepsReady},
			Remove:  S{ssC.StepCommentsReady},
			Tags:    S{ssbase.TagPrompt},
		},
		ssC.StepCommentsReady: {Remove: S{ssC.GenStepComments}},

		ssC.Orienting: {
			Multi: true,
			Tags:  S{ssbase.TagPrompt},
		},
		ssC.OrientingMove: {},
	})
```

### Prompt Schema

```go
// RECIPE

type PromptRecipePicking = secai.Prompt[ParamsRecipePicking, ResultRecipePicking]

func NewPromptRecipePicking(agent shared.AgentBaseAPI) *PromptRecipePicking {
	return secai.NewPrompt[ParamsRecipePicking, ResultRecipePicking](
		agent, ss.StoryRecipePicking, `
			- You're a database of cooking recipes.
		`, `
			1. Suggest recipes based on user's ingredients.
			2. If possible, find 1 extra recipe, which is well known, but 1-3 ingredients are missing.
			3. Summarize the propositions using the character's personality.
		`, `
			- Limit the amount of recipes to the requested number (excluding the extra recipe).
			- Include an image URL per each recipe
		`)
}

type Recipe struct {
	Name  string
	Desc  string
	Steps string
}

type ParamsRecipePicking struct {
	// List of available ingredients.
	Ingredients []Ingredient
	// The number of recipes needed.
	Amount int
}

type ResultRecipePicking struct {
	// List of proposed recipes
	Recipes []Recipe
	// Extra recipe with unavailable ingredients.
	ExtraRecipe Recipe
	// Message to the user, summarizing the recipes. Max 3 sentences.
	Summary string
}

// ...

var StoryRecipePicking = &shared.Story{
	StoryInfo: shared.StoryInfo{
		State: ss.StoryRecipePicking,
		Title: "Recipe Picking",
		Desc:  "The bot offers some recipes, based on the ingredients.",
	},
	Agent: shared.StoryActor{
		Trigger: amhelp.Cond{
			Is:  am.S{ss.Ready, ss.IngredientsReady},
			Not: am.S{ss.RecipeReady},
		},
	},
}
```

Read the full [state schema](examples/cook/states/ss_cook.go) and [prompt schema](examples/cook/schema/sa_cook.go).

## Documentation

- secai: [API](https://pkg.go.dev/github.com/pancsta/secai) / [Docs](https://github.com/pancsta/secai/wiki/Developer-Docs)
- [asyncmachine-go](https://asyncmachine.dev): [API](https://pkg.go.dev/github.com/pancsta/asyncmachine-go/pkg/machine) / [Docs](https://github.com/pancsta/asyncmachine-go/blob/main/docs/manual.md)
- [instructor-go](https://github.com/instructor-ai/instructor-go): [API](https://pkg.go.dev/github.com/instructor-ai/instructor-go) / [Docs](https://go.useinstructor.com/)
- [cview](https://codeberg.org/tslocum/cview): [API](https://pkg.go.dev/codeberg.org/tslocum/cview)

## Getting Started

We can use one of the examples as a starting template. It allows for further semver updates of the base framework.

1. Choose the source example
   - `export SECAI_EXAMPLE=cook`
   - `export SECAI_EXAMPLE=research` (broken since `v0.4.0`)
2. `git clone https://github.com/pancsta/secai.git`
3. install task `./secai/scripts/deps.sh`
4. copy the agent `cp -R secai/examples/$SECAI_EXAMPLE MYAGENT`
5. `cd MYAGENT && go mod init github.com/USER/MYAGENT`
6. get fresh configs
   1. `task sync-taskfile`
   2. `task sync-configs`
7. start it `task start`
8. look around `task --list-all`
9. configure the bot `$EDITOR config.kdl`

## Differences

**secai** differs from other AI agents / workflows frameworks in the way it treats AI prompts. Most frameworks call each
prompt an "agent", while **secai** treats prompts as simple DB queries with IoC (Inversion of Control). Tools usage
happens manually through typesafe params / results. This approach increases determinism, safety, and overfall control.
This multi-prompt workflow forms an actual **bot** / **agent**. This does not mean agents can't be composed into larger
groups, which happens simply on the state level (via piping / aRPC), as the underlying workflow engine (asyncmachine)
doesn't depend on AI at all.

## Scripting

`arpc` offers CLI access to remote agents, including subscription. It's perfect for quick and simple integrations, scripts, or experiments.

Example: `$ arpc -f tmp/research.addr -- when . Requesting && echo "REQUESTING"`

1. Connect to the address from `tmp/research.addr`
2. When the last connected agent (`.`) goes into state `Requesting`
3. Print "REQUESTING" and exit

## License

To help keep AI open, this project migrated to **GPL** starting from `v0.3.0`.

## Acknowledgements

- [AtomicAgents](https://github.com/BrainBlend-AI/atomic-agents)
- [SecretAgent Soma.fm](https://somafm.com/secretagent/)
