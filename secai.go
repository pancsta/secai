package secai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/instructor-ai/instructor-go/pkg/instructor"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	"github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/pancsta/secai/db"
	"github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
	"github.com/sashabaranov/go-openai"
)

type S = am.S

var ss = schema.AgentStates
var sessId = uuid.New().String()

// ///// ///// /////

// ///// BASIC TYPES

// ///// ///// /////

func init() {
	if os.Getenv("SECAI_DIR") == "" {
		os.Setenv("SECAI_DIR", ".")
	}
}

// DOCUMENT

type Document struct {
	title string
	parts []string
}

func NewDocument(title string, content ...string) *Document {
	return &Document{
		title: title,
		parts: content,
	}
}

func (d *Document) Title() string {
	return d.title
}

func (d *Document) Parts() []string {
	return slices.Clone(d.parts)
}

func (d *Document) AddPart(parts ...string) *Document {
	d.parts = append(d.parts, parts...)
	return d
}

func (d *Document) Clear() *Document {
	d.parts = nil
	return d
}

func (d *Document) Clone() Document {
	return *NewDocument(d.title, d.parts...)
}

// ///// ///// /////

// ///// PROMPT

// ///// ///// /////

type PromptApi interface {
	AddTool(tool ToolApi)

	HistOpenAI() []openai.ChatCompletionMessage
	AppendHistOpenAI(msg *openai.ChatCompletionMessage)
}

type PromptSchemaless = Prompt[any, any]

type Prompt[P any, R any] struct {
	Conditions   string
	Steps        string
	Result       string
	SchemaParams P
	SchemaResult R

	// number of previous messages to include
	HistoryMsgLen int
	// number of previous machine times to include
	HistoryStateLen int

	tools      map[string]ToolApi
	histOpenAI []openai.ChatCompletionMessage
	State      string
	A          AgentApi
}

func NewPrompt[P any, R any](agent AgentApi, state, condition, steps, results string) *Prompt[P, R] {
	if condition == "" {
		condition = "This is a conversation with a helpful and friendly AI assistant."
	}

	return &Prompt[P, R]{
		Conditions:      shared.Sp(condition),
		Steps:           shared.Sp(steps),
		Result:          shared.Sp(results),
		HistoryMsgLen:   10,
		HistoryStateLen: 100,
		State:           state,
		A:               agent,

		tools: make(map[string]ToolApi),
	}
}

// TODO accept model as general opts obj
func (p *Prompt[P, R]) Run(params P, model string) (*R, error) {
	// TODO support model pre-selection
	if p.State == "" {
		return nil, fmt.Errorf("prompt state not set")
	}

	// prep the machine
	mach := p.A.Mach()
	ctx := mach.NewStateCtx(p.State)
	mach.Add1(ss.RequestingLLM, nil)
	defer mach.Remove1(ss.RequestingLLM, nil)

	// gen an LLM prompt
	llm := p.A.OpenAI()
	content, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}

	contentStr := string(content)
	p.A.Log(contentStr)

	// compose along with previous msgs
	usrMsg := openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: contentStr,
	}
	msgs := p.MsgsOpenAI()
	sysMsg := msgs[0].Content
	dbId, err := p.A.Queries().AddPrompt(ctx, db.AddPromptParams{
		SessionID:   sessId,
		Agent:       mach.Id(),
		State:       p.State,
		System:      sysMsg,
		HistoryLen:  int64(len(msgs) - 1),
		Request:     contentStr,
		CreatedAt:   time.Now(),
		MachTimeSum: int64(mach.TimeSum(nil)),
		MachTime:    fmt.Sprintf("%v", mach.Time(nil)),
	})
	if err != nil {
		return nil, err
	}

	outDir := os.Getenv("SECAI_DIR")
	if outDir != "" {
		// save sysMsg to SECAI_DIR under statename.prompt
		filename := filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".sys.prompt")
		if err := os.WriteFile(filename, []byte(sysMsg), 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		filename = filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".prompt")
		if err := os.WriteFile(filename, content, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// call the LLM and fill the result (according to the schema)
	// TODO keep as states, allow for both (and others)
	if model == "" {
		model = openai.GPT4o
		if os.Getenv("DEEPSEEK_API_KEY") != "" {
			model = "deepseek-chat"
		}
	}
	var result R
	req := openai.ChatCompletionRequest{
		Model:    model,
		Messages: slices.Concat(msgs, []openai.ChatCompletionMessage{usrMsg}),
	}
	_, err = llm.CreateChatCompletion(ctx, req, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to run LLM: %w", err)
	}

	// persist
	p.AppendHistOpenAI(&usrMsg)
	resultJ, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	err = p.A.Queries().AddPromptResponse(ctx, db.AddPromptResponseParams{
		Response: sql.NullString{String: string(resultJ), Valid: true},
		ID:       dbId,
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (p *Prompt[P, R]) AddTool(tool ToolApi) {
	p.tools[tool.Mach().Id()] = tool
}

func (p *Prompt[P, R]) Generate() string {

	// instructions
	// TODO needed? mode takes care of this?
	// result := p.Result + "\n" +
	// 	"Always respond using the proper JSON schema.\n" +
	// 	"Always use the available additional information and context to enhance the response."

	// documents
	docs := ""
	for _, t := range p.tools {
		doc := t.Document().Clone()
		c := doc.Parts()
		if len(c) == 0 {
			continue
		}
		docs += "## " + doc.Title() + "\n\n" + strings.Join(doc.Parts(), "\n") + "\n\n"
	}
	if docs != "" {
		docs = "# EXTRA INFORMATION AND CONTEXT\n\n" + docs
	}

	// template
	return shared.Sp(`
		# IDENTITY and PURPOSE
	
		%s
		# INTERNAL ASSISTANT STEPS
	
		%s
		# OUTPUT INSTRUCTIONS
	
		%s
		%s
		`, p.Conditions, p.Steps, p.Result, docs)
}

// OPENAI

func (p *Prompt[P, R]) MsgsOpenAI() []openai.ChatCompletionMessage {
	// system msg
	msgs := []openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleSystem,
		Content: p.Generate(),
	}}

	// get N latest user msgs
	hist := p.HistOpenAI()
	if len(hist) > p.HistoryMsgLen {
		hist = hist[len(hist)-p.HistoryMsgLen:]
	}

	return slices.Concat(msgs, hist)
}

func (p *Prompt[P, R]) HistOpenAI() []openai.ChatCompletionMessage {
	return p.histOpenAI
}

func (p *Prompt[P, R]) AppendHistOpenAI(msg *openai.ChatCompletionMessage) {
	if msg == nil {
		return
	}
	p.histOpenAI = append(p.histOpenAI, *msg)
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

type AgentApi interface {
	Output(txt string, from shared.From)

	Mach() *am.Machine
	SetMach(*am.Machine)

	SetOpenAI(c *instructor.InstructorOpenAI)
	OpenAI() *instructor.InstructorOpenAI

	Start() am.Result
	Stop(disposeCtx context.Context) am.Result
	Log(txt string, args ...any)

	DB() *sql.DB
	Queries() *db.Queries

	// TODO history
}

func InitAgent[G AgentApi](
	ctx context.Context, id string, states am.S, machSchema am.Schema, agent G,
) (G, error) {
	// validate states schema
	if err := amhelp.Implements(states, schema.AgentStates.Names()); err != nil {
		return agent, fmt.Errorf("AgentStates not implemented: %w", err)
	}
	// validate config
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("DEEPSEEK_API_KEY") == "" {
		return agent, fmt.Errorf("OPENAI_API_KEY or DEEPSEEK_API_KEY required")
	}

	// machine
	mach, err := am.NewCommon(ctx, id, machSchema, states, agent, nil, nil)
	if err != nil {
		return agent, err
	}
	shared.MachTelemetry(mach, nil)
	agent.SetMach(mach)

	// LLM clients
	// TODO expose as states

	// DEEPSEEK
	deepseekApi := os.Getenv("DEEPSEEK_API_KEY")
	if deepseekApi != "" {
		config := openai.DefaultConfig(deepseekApi)
		// TODO check for OPENAI_BASE_URL first
		config.BaseURL = "https://api.deepseek.com"

		agent.SetOpenAI(instructor.FromOpenAI(
			openai.NewClientWithConfig(config),
			// instructor.WithMode(instructor.ModeJSON),
			// TODO config
			instructor.WithMaxRetries(3),
		))

		// OPENAI
	} else {
		agent.SetOpenAI(instructor.FromOpenAI(
			openai.NewClient(os.Getenv("OPENAI_API_KEY")),
			// instructor.WithMode(instructor.ModeJSON),
			// TODO config
			instructor.WithMaxRetries(3),
		))
	}

	return agent, nil
}

// BASE AGENT

type Agent struct {
	*am.ExceptionHandler
	*ssam.DisposedHandlers

	// UserInput is a prompt submitted the user, owned by [schema.AgentStatesDef.Prompt].
	UserInput string

	mach                *am.Machine
	prompts             map[string]PromptApi
	history             []openai.Message
	openAI              *instructor.InstructorOpenAI
	maxRetries          int
	db                  *sql.DB
	queries             *db.Queries
	dbPending           []func(ctx context.Context) error
	requestingLLMEnter  int
	requestingLLMExit   int
	requestingToolEnter int
	requestingToolExit  int
}

var _ AgentApi = &Agent{}

// METHODS

func (a *Agent) DB() *sql.DB {
	return a.db
}

func (a *Agent) Output(txt string, from shared.From) {
	fmt.Print(txt)
}

func (a *Agent) Mach() *am.Machine {
	return a.mach
}

func (a *Agent) SetMach(m *am.Machine) {
	a.mach = m
}

func (a *Agent) OpenAI() *instructor.InstructorOpenAI {
	return a.openAI
}

func (a *Agent) SetOpenAI(c *instructor.InstructorOpenAI) {
	a.openAI = c
}

func (a *Agent) Start() am.Result {
	return a.Mach().Add1(ss.Start, nil)
}

func (a *Agent) Stop(disposeCtx context.Context) am.Result {
	res := a.Mach().Remove1(ss.Start, nil)
	if disposeCtx != nil {
		a.Mach().Add1(ss.Disposing, nil)
		<-a.Mach().When1(ss.Disposed, disposeCtx)
	}

	return res
}

func (a *Agent) Log(txt string, args ...any) {
	if os.Getenv("SECAI_LOG") == "" {
		return
	}
	a.mach.Log(txt+"\n", args...)
}

func (a *Agent) Queries() *db.Queries {
	if a.queries == nil {
		a.queries = db.New(a.db)
	}

	return a.queries
}

// HANDLERS

func (a *Agent) StartEnter(e *am.Event) bool {
	// TODO err msg
	return os.Getenv("SECAI_DIR") != ""
}

func (a *Agent) StartState(e *am.Event) {
	// pass
}

func (a *Agent) DBStartingState(e *am.Event) {
	ctx := a.mach.NewStateCtx(ss.DBStarting)

	go func() {
		conn, err := db.Open(os.Getenv("SECAI_DIR"))
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			return
		}
		a.db = conn

		// truncate
		// TODO DEBUG
		// if err := a.Queries().DropPrompts(ctx); err != nil {
		// 	a.mach.AddErr(err, nil)
		// 	return
		// }

		// create tables
		_, err = a.db.ExecContext(ctx, db.Schema())
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				a.mach.AddErr(err, nil)
			}
		}

		// exec late queries
		for _, fn := range a.dbPending {
			if ctx.Err() != nil {
				return // expired
			}
			if err := fn(ctx); err != nil {
				a.mach.AddErr(err, nil)
				return
			}
		}

		if ctx.Err() != nil {
			return // expired
		}
		a.mach.Add1(ss.DBReady, nil)
	}()

	// TODO migrations

	// start
	// tx, err := a.db.BeginTx(ctx, nil)
	// if err != nil {
	// 	return 0, err
	// }
	// defer tx.Rollback()
	// q := a.queries.WithTx(tx)

	// save
	// err = tx.Commit()
	// if err != nil {
	// 	log.Errorf("failed to commit: %v", err)
	// 	return 0, err
	// }
}

func (a *Agent) DBReadyEnd(e *am.Event) {
	err := a.db.Close()
	if err != nil {
		a.mach.AddErr(err, nil)
	}
}

func (a *Agent) DBSavingEnter(e *am.Event) bool {
	// TODO typed params
	_, ok := e.Args["fn"].(func(ctx context.Context) error)

	return ok
}

// TODO generalize these type of handlers in pkg/states
//  as counted-multi

func (a *Agent) RequestingLLMEnter(e *am.Event) bool {
	a.requestingLLMEnter++
	return true
}

func (a *Agent) RequestingLLMExit(e *am.Event) bool {
	a.requestingLLMExit++
	return a.requestingLLMEnter == a.requestingLLMExit
}

func (a *Agent) RequestingLLMEnd(e *am.Event) {
	a.mach.Remove1(ss.Requesting, nil)
}

func (a *Agent) RequestingToolEnter(e *am.Event) bool {
	a.requestingToolEnter++
	return true
}

func (a *Agent) RequestingToolExit(e *am.Event) bool {
	a.requestingToolExit++
	return a.requestingToolEnter == a.requestingToolExit
}

func (a *Agent) RequestingToolEnd(e *am.Event) {
	a.mach.Remove1(ss.Requesting, nil)
}

func (a *Agent) RequestingExit(e *am.Event) bool {
	return !a.mach.Any1(ss.RequestingLLM, ss.RequestingTool)
}

func (a *Agent) DBSavingState(e *am.Event) {
	// postpone
	fn := e.Args["fn"].(func(ctx context.Context) error)
	if a.mach.Not1(ss.DBReady) {
		a.dbPending = append(a.dbPending, fn)
		a.mach.Remove1(ss.DBSaving, nil)

		return
	}

	// save
	ctx := a.mach.NewStateCtx(ss.DBReady)
	tick := a.mach.Tick(ss.DBSaving)
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
		if err := fn(ctx); err != nil {
			a.mach.AddErr(err, nil)
		}

		// last one deactivates
		if tick == a.mach.Tick(ss.DBSaving) {
			a.mach.Remove1(ss.DBSaving, nil)
		}
	}()
}

func (a *Agent) PromptState(e *am.Event) {
	// TODO typed args, Enter
	a.UserInput = e.Args["prompt"].(string)
}

func (a *Agent) PromptEnd(e *am.Event) {
	a.UserInput = ""
}

// ///// ///// /////

// ///// TOOL

// ///// ///// /////

type ToolApi interface {
	Mach() *am.Machine
	SetMach(*am.Machine)
	Document() *Document
}

type Tool struct {
	mach *am.Machine
	Doc  *Document
}

func NewTool(
	agent AgentApi, idSuffix, title string, states am.S, stateSchema am.Schema,
) (*Tool, error) {
	// validate the state schema
	if err := amhelp.Implements(states, schema.ToolStates.Names()); err != nil {
		return nil, fmt.Errorf("%w: ToolStates not implemented: %w", am.ErrSchema, err)
	}

	// document
	t := &Tool{
		Doc: NewDocument(title),
	}

	// machine
	id := "tool-" + agent.Mach().Id() + "-" + idSuffix
	mach, err := am.NewCommon(agent.Mach().Ctx(), id, stateSchema, states, nil, agent.Mach(), &am.Opts{
		Tags: []string{"tool"},
	})
	if err != nil {
		return nil, err
	}
	t.mach = mach
	shared.MachTelemetry(mach, nil)

	// pipe Ready from the tool to agent
	err = pipes.BindReady(mach, agent.Mach(), "", "")
	if err != nil {
		return nil, err
	}

	// pipe Start from the agent to tool
	err = pipes.BindStart(agent.Mach(), mach, "", "")
	if err != nil {
		return nil, err
	}

	return t, nil
}

func (t *Tool) Mach() *am.Machine {
	return t.mach
}

func (t *Tool) SetMach(m *am.Machine) {
	t.mach = m
}

func ToolAddToPrompts(t ToolApi, prompts ...PromptApi) {
	for _, p := range prompts {
		p.AddTool(t)
	}
}

// ///// ///// /////

// ///// SCENARIO

// ///// ///// /////

// Scenario produces ScenarioSamples for checkpoint states.
type Scenario struct {
	// Name of the scenario
	Name string

	// scenario only applicable if these states are active (optional)
	RequireActive am.S
	// scenario only applicable if these states are inactive (optional)
	RequireInactive am.S

	// states possible to trigger (separately)
	Inputs am.S
	// states expected to happen
	Checkpoints am.S
	// additional context states (optional)
	Contexts am.S

	// number of future transitions to check for checkpoint activations, eg [1, 5, 10]
	// will mark a checkpoint true if it gets activated in 1 or 5 or 10 txes
	// from the input state
	// default: [1]
	ActivationDistances []int
	// number of mutations to simulate and check for checkpoint confirmations
	ScenarioSteps int // default: 1
}

type ScenarioSamples struct {
	// active checkpoints in the beginning of the scenario
	StartingCheckpoints []string
	// names of input states, with only one being called at a time
	Inputs []string
	// names of checkpoint states
	Checkpoint []string
	// names of context states
	Contexts []string
	// samples with Checkpoint states as inputs
	Samples []*CheckpointSamples
	// current tx steps of the input state TODO later
	// RelationSteps []*CheckpointSamples
}

type CheckpointSamples struct {
	// activation distance of the checkpoints
	Distance int
	// step of the scenario
	ScenarioStep int
	// input-results for the given scenario step and distance
	InRes []InRes
}

type InRes struct {
	Active   uint8
	Expected uint8
}
