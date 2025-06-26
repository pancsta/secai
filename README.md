# AI-gent Workflows

**AI-gent Workflows** (aka **secai**) is a platform for AI Agents with a local reasoning layer. It's implemented on top of a
**unified state graph** and makes a solid foundation for complex, proactive, and **long-lived AI Agents** with deep and
structured memory. It offers a dedicated set of devtools and is written in the Go programming language. By having
graph-based flow, **secai** allows for precise behavior modeling of agents, including interruptions and fault tolerance.

## User Demo

[Screenshots](#screenshots-user-demo) and [YouTube](https://youtu.be/rbwXg64poBE) are also available.

<p align="center"><a href="https://pancsta.github.io/assets/secai/demo2/secai-demo2.mp4"><img src="https://pancsta.github.io/assets/secai/demo2/demo2.png?"></a></p>

> [!NOTE]
> This user demo is a 7min captions-only presentation, showcasing a cooking assistant which helps to pick a recipe from ingredients AND cook it.

## Platform Demo

[Screenshots](#screenshots-platform-demo) and [YouTube](https://youtu.be/0VJzO1S-gV0) are also available.

<p align="center"><a href="https://pancsta.github.io/assets/secai/demo1/secai-demo1.mp4"><img src="https://pancsta.github.io/assets/secai/demo1/demo1.png"></a></p>

> [!NOTE]
> This tech demo is a 5min captions-only screencast, showcasing all 9 ways an agent can be seen, in addition to the classic chat view.

## Features

- prompt atomicity on the state level
  - each state can have a prompt bound to it, with dedicated history and documents
- atomic consensus with relations and negotiation
  - states excluding each other can't be active simultaneously
- separate schema DSL layer
  - suitable for non-coding authors
- declarative flow definitions
  - for non-linear flows
- cancellation support (interrupts)
- offer list / menu
- prompt history
  - in SQL (embedded SQLite)
  - in JSONL (stdout)
- proactive stories with actors
- LLM triggers (orienting)
  - on prompts and timeouts
- dynamic flow graph for the memory
  - LLM creates an actionable state machine
- UI components
  - layouts (zellij)
  - chat (tview)
  - stories (cview)
  - clock (bubbletea)
- platforms
  - SSH (all platforms)
  - Desktop PWA (all platforms)
  - Mobile PWA (basic)

### Devtools

The following devtools are for the agent, the agent's dynamic memory, and tools (all of which are the same type of state machine).

- REPL & CLI
- TUI debugger (dashboards)
- automatic diagrams (SVG, D2, mermaid)
- automatic observability (Prometheus, Grafana, Jaeger)

### Tools

- websearch ([searxng](https://github.com/searxng/searxng))
- HTML scrape ([colly](https://github.com/gocolly/colly))
- browser scrape ([chromedp](https://github.com/chromedp/chromedp)) (WIP)

### Planned

- lambda prompts (unbound)
  - based on langchaingo
- MCP (both relay and tool)
- history DSL with a vector format (WIP)
- agent contracts
- i18n
- Gemini via direct SDK
- ML triggers
  - based on local neural networks
- mobile and WASM builds
- support local LLMs (eg iOS)
- desktop apps
- dynamic tools
  - LLM creates tools on the fly
- prompts as RSS

## Implementation

- pure Golang
- typesafe state-machine and prompt schemas
- [asyncmachine-go](https://asyncmachine.dev) for graphs and control flow
- [instructor-go](https://github.com/instructor-ai/instructor-go) for the LLM layer
  - OpenAI, DeepSeek, Anthropic, Cohere (soon Gemini)
- network transparency (aRPC, debugger, REPL)
- structured concurrency (multigraph-based)
- [tview](https://github.com/rivo/tview/), [cview](https://code.rocket9labs.com/tslocum/cview), and [asciigraph](https://github.com/guptarohit/asciigraph) for UIs

### Components

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
  - actors
  - state machines
- Document
  - title
  - content

## Comparison

| Feature       | AI-gent Workflows                                                                                              | [AtomicAgents](https://github.com/BrainBlend-AI/atomic-agents) |
|---------------|----------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| Model         | unified state graph                                                                                            | BaseAgent class                                                |
| Debugger      | multi-client with time travel                                                                                  | X                                                              |
| Diagrams      | customizable level of details                                                                                  | X                                                              |
| Observability | logging & Grafana & Otel                                                                                       | X                                                              |
| REPL & CLI    | network-based                                                                                                  | X                                                              |
| History       | state-based and prompt-based                                                                                   | prompt-based                                                   |
| Pkg manager   | Golang                                                                                                         | in-house                                                       |
| Control Flow  | declarative & fault tolerant                                                                                   | imperative                                                     |
| CLI           | [bubbletea](https://github.com/charmbracelet/bubbletea), [lipgloss](https://github.com/charmbracelet/lipgloss) | [rich](https://github.com/Textualize/rich)                     |
| TUI           | [tview](https://github.com/rivo/tview/), [cview](https://code.rocket9labs.com/tslocum/cview)                   | [textual](https://github.com/Textualize/textual)               |

### Go vs Python

- just works, batteries included, no magic
- 1 package manager **vs** 4
- single binary **vs** interpreted multi-file source
- coherent static typing **vs** maybe
- easy & stable **vs** easy
- no ecosystem fragmentation
- million times faster **/s**
- [relevant xkcd](https://xkcd.com/1987/)

## Try It

Unlike Python apps, you can start it with a single command:

- [Download a binary release](https://github.com/pancsta/secai/releases/latest) (Linux, MacOS, Windows)
- Set either of the API keys:
  - `export OPENAI_API_KEY=myapikey`
  - `export DEEPSEEK_API_KEY=myapikey`
- Run `./cook` or `./research` to start the server
  - then copy-paste-run the *TUI Desktop* line
  - you'll see files being created in `./tmp`

```markdown
cook v0.2

TUI Chat:
$ ssh chat@localhost -p 7854 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no

TUI Stories:
$ ssh stories@localhost -p 7854 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no

TUI Clock:
$ ssh clock@localhost -p 7854 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no

TUI Desktop:
$ bash <(curl -L https://zellij.dev/launch) --layout $(./cook desktop-layout) attach secai-cook --create

https://ai-gents.work

{"time":"2025-06-25T11:59:28.421964349+02:00","level":"INFO","msg":"SSH UI listening","addr":"localhost:7854"}
{"time":"2025-06-25T11:59:29.779618008+02:00","level":"INFO","msg":"output phrase","key":"IngredientsPicking"}
```

## Example

Code snippets from [`/examples/research`](/examples/research/schema/sa_research.go) (ported from [AtomicAgents](https://github.com/BrainBlend-AI/atomic-agents/tree/main/atomic-examples/deep-research/deep_research/agents)).
Both the state and prompt schemas are pure and debuggable Golang code.

### State Schema

```go
// ResearchStatesDef contains all the states of the Research state machine.
type ResearchStatesDef struct {
    *am.StatesBase

    CheckingInfo string
    NeedMoreInfo string

    SearchingLLM string
    SearchingWeb string
    Scraping     string

    Answering string
    Answered  string

    *ss.AgentStatesDef
}

// ResearchGroupsDef contains all the state groups Research state machine.
type ResearchGroupsDef struct {
    Info    S
    Search  S
    Answers S
}

// ResearchSchema represents all relations and properties of ResearchStates.
var ResearchSchema = SchemaMerge(
    // inherit from Agent
    ss.AgentSchema,

    am.Schema{

        // Choice "agent"
        ssR.CheckingInfo: {
            Require: S{ssR.Start, ssR.Prompt},
            Remove:  sgR.Info,
        },
        ssR.NeedMoreInfo: {
            Require: S{ssR.Start},
            Add:     S{ssR.SearchingLLM},
            Remove:  sgR.Info,
        },

        // Query "agent"
        ssR.SearchingLLM: {
            Require: S{ssR.NeedMoreInfo, ssR.Prompt},
            Remove:  sgR.Search,
        },
        ssR.SearchingWeb: {
            Require: S{ssR.NeedMoreInfo, ssR.Prompt},
            Remove:  sgR.Search,
        },
        ssR.Scraping: {
            Require: S{ssR.NeedMoreInfo, ssR.Prompt},
            Remove:  sgR.Search,
        },

        // Q&A "agent"
        ssR.Answering: {
            Require: S{ssR.Start, ssR.Prompt},
            Remove:  SAdd(sgR.Info, sgR.Answers),
        },
        ssR.Answered: {
            Require: S{ssR.Start},
            Remove:  SAdd(sgR.Info, sgR.Answers, S{ssR.Prompt}),
        },
    })

var sgR = am.NewStateGroups(ResearchGroupsDef{
    Info:    S{ssR.CheckingInfo, ssR.NeedMoreInfo},
    Search:  S{ssR.SearchingLLM, ssR.SearchingWeb, ssR.Scraping},
    Answers: S{ssR.Answering, ssR.Answered},
})
```

### Prompt Schema

```go
func NewCheckingInfoPrompt(agent secai.AgentApi) *secai.Prompt[ParamsCheckingInfo, ResultCheckingInfo] {
    return secai.NewPrompt[ParamsCheckingInfo, ResultCheckingInfo](
        agent, ssR.CheckingInfo, `
            - You are a decision-making agent that determines whether a new web search is needed to answer the user's question.
            - Your primary role is to analyze whether the existing context contains sufficient, up-to-date information to
            answer the question.
            - You must output a clear TRUE/FALSE decision - TRUE if a new search is needed, FALSE if existing context is
            sufficient.
        `, `
            1. Analyze the user's question to determine whether or not an answer warrants a new search
            2. Review the available web search results 
            3. Determine if existing information is sufficient and relevant
            4. Make a binary decision: TRUE for new search, FALSE for using existing context
        `, `
            Your reasoning must clearly state WHY you need or don't need new information
            If the web search context is empty or irrelevant, always decide TRUE for new search
            If the question is time-sensitive, check the current date to ensure context is recent
            For ambiguous cases, prefer to gather fresh information
            Your decision must match your reasoning - don't contradict yourself
        `)
}

// CheckingInfo (Choice "agent")

type ParamsCheckingInfo struct {
    UserMessage  string
    DecisionType string
}

type ResultCheckingInfo struct {
    Reasoning string `jsonschema:"description=Detailed explanation of the decision-making process"`
    Decision  bool   `jsonschema:"description=The final decision based on the analysis"`
}
```

Read the [schema file in full](/examples/deepresearch/schema/sa_research.go).

## Screenshots

### Screenshots User Demo

- [Slide Deck](https://speakerdeck.com/pancsta/ai-gent-workflows/)

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
    <td align="center" colspan="5">State Schema</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/cook.svg"/>
    </td>
  </tr>

</table>

### Screenshots Platform Demo

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
    <td align="center" colspan="5">State Schema</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/deepresearch.svg"/>
    </td>
  </tr>

</table>

### Screenshots Dashboards

<table>

  <tr>
    <td align="center" colspan="5">Dashboard 1</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/dashboard-2.png"/>
    </td>
  </tr>

  <tr>
    <td align="center" colspan="5">Dashboard 2</td>
  </tr>
  <tr>
    <td align="center" colspan="5">
        <img src="https://pancsta.github.io/assets/secai/dashboard-1.png"/>
    </td>
  </tr>

</table>

## Documentation

- secai: [API](https://pkg.go.dev/github.com/pancsta/secai) / [Docs](https://github.com/pancsta/secai/wiki/Developer-Docs)
- [asyncmachine-go](https://asyncmachine.dev): [API](https://pkg.go.dev/github.com/pancsta/asyncmachine-go/pkg/machine) / [Docs](https://github.com/pancsta/asyncmachine-go/blob/main/docs/manual.md)
- [instructor-go](https://github.com/instructor-ai/instructor-go): [API](https://pkg.go.dev/github.com/instructor-ai/instructor-go) / [Docs](https://go.useinstructor.com/)
- [tview](https://github.com/rivo/tview/): [API](https://pkg.go.dev/github.com/rivo/tview) / [Docs](https://github.com/rivo/tview/wiki)
- [cview](https://codeberg.org/tslocum/cview): [API](https://pkg.go.dev/codeberg.org/tslocum/cview)

## Getting Started

We can use one of the examples as a starting template. It allows for further semver updates of the base framework.

1. Choose the source example
   - `export SECAI_EXAMPLE=cook`
   - `export SECAI_EXAMPLE=research`
2. `git clone https://github.com/pancsta/secai.git`
3. install task `./secai/scripts/deps.sh`
4. copy the agent `cp -R secai/examples/$SECAI_EXAMPLE MYAGENT`
5. `cd MYAGENT && go mod init github.com/USER/MYAGENT`
6. get fresh configs
   1. `task sync-taskfile`
   2. `task sync-configs`
7. start it `task start`
8. look around `task --list-all`
9. configure `cp template.env .env`

## User Interfaces

Several TUIs with dedicated UI states are included in [`/tui`](/tui):

### Chat TUI

- **senders & msgs** scrollable view with links
- multiline **prompt** with blocking and progress
- send / stop **button**

### Stories TUI

- **list of stories** with activity status, non-actionable
- **dynamic buttons** and progress bars, actionable

### Clockmoji TUI

- recent **clock changes** plotted by [asciigraph](http://github.com/guptarohit/asciigraph)

## Bash Scripts

`arpc` offers CLI access to remote agents, including subscription. It's perfect for quick and simple integrations, scripts, or experiments.

Example: `arpc -f tmp/research.addr -- when . Requesting && echo "REQUESTING"`

1. Connect to the address from `tmp/research.addr`
2. When the last connected agent (`.`) goes into state `Requesting`
3. Print "REQUESTING" and exit

## Acknowledgements

- [AtomicAgents](https://github.com/BrainBlend-AI/atomic-agents)
- [SecretAgent Soma.fm](https://somafm.com/secretagent/)
