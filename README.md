# secret-aigents

**SecAI** is an AtomicAgents-like framework for **autonomous LLM daemons**. It's implemented on top of a
**unified state graph** and makes a solid foundation for complex, proactive, and long-lived **AI Agents** with deep and
structured memory. It offers a dedicated set of devtools and is written in the Go programming language. By having
graph-based flow, **SecAI** allows for precise behavior modeling of agents, including interruptions and fault-tolerancy.

## Demo

[Screenshots](#screenshots) and [YouTube](https://youtu.be/0VJzO1S-gV0) are also available.

<p align="center"><a href="https://pancsta.github.io/assets/secai/demo1/secai-demo1.mp4"><img src="https://pancsta.github.io/assets/secai/demo1/demo1.png"></a></p>

> [!NOTE]
> This tech demo is a 5min captions-only screencast, showcasing all 9 ways an agent can be seen, in addition to the classic chat view.

## Features

- prompt atomicity on the state level
- atomic consensus with relations and negotiation
- declarative flow definitions
- REPL & CLI
- TUI debugger (+zellij dashboards)
- automatic diagrams (SVG, D2, mermaid)
- automatic observability (Prometheus, Grafana, Jaeger)
- cancellation support (interrupts)
- prompt history in SQL (embedded SQLite)
- chat TUI (tview)

### Tools

- websearch ([searxng](https://github.com/searxng/searxng))
- HTML scrape ([colly](https://github.com/gocolly/colly))
- browser scrape ([chromedp](https://github.com/chromedp/chromedp)) (WIP)

### Planned

- typesafe arguments struct
- history DSL with a vector format (WIP)
- pro-active scenarios (WIP)
- MCP
- dynamic tools
- dynamic state graph
- i18n

## Implementation

- pure Golang
- typesafe state & prompt schemas
- [asyncmachine-go](https://asyncmachine.dev) for graphs and control flow
- [instructor-go](https://github.com/instructor-ai/instructor-go) for the LLM layer
- network transparency (aRPC, debugger, REPL)
- structured concurrency (multigraph-based)
- [tview](https://github.com/rivo/tview/) for chat TUI

### Components

- Agent (actor)
  - state-machine schema
- Tool (actor)
  - state-machine schema
- Prompt (stateless)
  - params schema
  - result schema
- Document
  - title, content

## Comparison

| Feature       | SecAI                                                                                                          | AtomicAgents                                     |
|---------------|----------------------------------------------------------------------------------------------------------------|--------------------------------------------------|
| Model         | unified state graph                                                                                            | BaseAgent class                                  |
| Debugger      | multi-client with time travel                                                                                  | X                                                |
| Diagrams      | customizable level of details                                                                                  | X                                                |
| Observability | logging & Grafana & Otel                                                                                       | X                                                |
| REPL & CLI    | network-based                                                                                                  | X                                                |
| History       | state-based and prompt-based                                                                                   | prompt-based                                     |
| Pkg manager   | Golang                                                                                                         | in-house                                         |
| Control Flow  | declarative & fault tolerant                                                                                   | imperative                                       |
| CLI           | [bubbletea](https://github.com/charmbracelet/bubbletea), [lipgloss](https://github.com/charmbracelet/lipgloss) | [rich](https://github.com/Textualize/rich)       |
| TUI           | [tview](https://github.com/rivo/tview/), [cview](https://code.rocket9labs.com/tslocum/cview)                   | [textual](https://github.com/Textualize/textual) |

### Go vs Python

- just works, batteries included, no magic
- 1 package manager **vs** 4
- single binary **vs** interpreted multi-file source
- coherent static typing **vs** maybe
- easy & stable **vs** easy
- no ecosystem fragmentation
- million times faster /s

## Try It

Unlike Python apps, you can start it with a single command:

1. [Install Go](https://go.dev/doc/install)
2. Set either of the API keys:
   - `export OPENAI_API_KEY=myapikey`
   - `export DEEPSEEK_API_KEY=myapikey`
3. Run `go run github.com/pancsta/secai/examples/deepresearch/cmd@latest`

## Example

Code snippets from [`/examples/deepresearch`](/examples/deepresearch/schema/sa_research.go). Both the state and prompt schemas are pure and debuggable Golang code.

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

- [asyncmachine-go](https://asyncmachine.dev) / [api](https://pkg.go.dev/github.com/pancsta/asyncmachine-go/pkg/machine) / [docs](https://github.com/pancsta/asyncmachine-go/blob/main/docs/manual.md)
- [instructor-go](https://github.com/instructor-ai/instructor-go) / [api](https://pkg.go.dev/github.com/instructor-ai/instructor-go) / [docs](https://go.useinstructor.com/)
- [tview](https://github.com/rivo/tview/) / [api](https://pkg.go.dev/github.com/rivo/tview) / [docs](https://github.com/rivo/tview/wiki)

## Getting Started

We can use `/examples/deepresearch` as a starting template. It allows for further updates of the base framework.

1. `git clone https://github.com/pansta/secai`
2. install task `./secai/scripts/deps.sh`
3. copy the agent `cp -R secai/examples/deepresearch MYAGENT`
4. copy agent's config `cp secai/template.env MYAGENT/.env`
5. copy project configs `cp -R secai/config MYAGENT`
6. `cd MYAGENT`
7. `go mod init github.com/USER/MYAGENT`
8. `go mod tidy`
9. `task install-deps`
10. `task start`
11. `task --list`

## Chat TUI

A simple chat TUI with UI states is included in [`/tui`](/tui), consisting of:

- **senders & msgs** scrollable view with links
- multiline **prompt** with blocking and progress
- send / stop **button**

## Bash Scripts

`arpc` offers a CLI access to remote agents, including subscription. It's perfect for quick and simple integrations, scripts, or experiments.

Example: `arpc -f tmp/deepresearch.addr -- when . Requesting && echo "REQUESTING"`

1. Connect to the address from `tmp/deepresearch.addr`
2. When the last connected agent (`.`) goes into state `Requesting`
3. Print "REQUESTING" and exit

## Acknowledgements

- [AtomicAgents](https://github.com/BrainBlend-AI/atomic-agents)
- [SecretAgent Soma.fm](https://somafm.com/secretagent/)
