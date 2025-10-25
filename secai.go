package secai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/instructor-ai/instructor-go/pkg/instructor"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	"github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/sashabaranov/go-openai"

	"github.com/pancsta/secai/db"
	"github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
)

type S = am.S
type A = shared.A

var ss = schema.AgentStates
var sessId = uuid.New().String()
var Pass = shared.Pass
var ParseArgs = shared.ParseArgs

// ///// ///// /////

// ///// BASIC TYPES

// ///// ///// /////

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

func (d *Document) AddToPrompts(prompts ...PromptApi) {
	for _, p := range prompts {
		p.AddDoc(d)
	}
}

// ///// ///// /////

// ///// PROMPT

// ///// ///// /////

type PromptApi interface {
	AddTool(tool ToolApi)
	AddDoc(doc *Document)

	HistOpenAI() []openai.ChatCompletionMessage
	AppendHistOpenAI(msg *openai.ChatCompletionMessage)
	HistCleanOpenAI()
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

	tools      map[string]ToolApi
	docs       map[string]*Document
	histOpenAI []openai.ChatCompletionMessage
	State      string
	A          AgentAPI
}

func NewPrompt[P any, R any](agent AgentAPI, state, condition, steps, results string) *Prompt[P, R] {
	if condition == "" {
		condition = "This is a conversation with a helpful and friendly AI assistant."
	}

	return &Prompt[P, R]{
		Conditions:    shared.Sp(condition),
		Steps:         shared.Sp(steps),
		Result:        shared.Sp(results),
		HistoryMsgLen: 10,
		State:         state,
		A:             agent,

		tools: make(map[string]ToolApi),
		docs:  make(map[string]*Document),
	}
}

// TODO accept model as general opts obj
// TODO rename to Exec?
func (p *Prompt[P, R]) Run(e *am.Event, params P, model string) (*R, error) {
	// TODO retries
	// TODO support model pre-selection
	if p.State == "" {
		return nil, fmt.Errorf("prompt state not set")
	}

	// prep the machine
	mach := p.A.Mach()
	ctx := mach.NewStateCtx(p.State)
	// TODO config
	outDir := os.Getenv("SECAI_DIR")
	mach.EvAdd1(e, ss.RequestingLLM, nil)
	defer mach.EvRemove1(e, ss.RequestingLLM, nil)

	// gen an LLM prompt
	llm := p.A.OpenAI()
	prompt, err := json.MarshalIndent(params, "", "	")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}
	contentLog, _ := json.Marshal(params)
	contentStr := string(prompt)

	// compose along with previous msgs
	usrMsg := openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: contentStr,
	}
	// TODO define used hist per prompt
	msgs := p.MsgsOpenAI()
	sysMsg := msgs[0].Content

	// log in various ways
	if os.Getenv("SECAI_LOG_PROMPTS") != "" {
		p.A.Logger().Info(p.State, "sys_prompt", sysMsg)
	}
	p.A.Log(p.State, "prompt", string(contentLog))
	if outDir != "" {
		// save sysMsg to SECAI_DIR under "statename.sys.md"
		filename := filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".sys.md")
		if err := os.WriteFile(filename, []byte(sysMsg), 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt to SECAI_DIR under "statename.prompt.json"
		filename = filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".prompt.json")
		if err := os.WriteFile(filename, prompt, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// TODO separate state per LLM provider

	// call the LLM and fill the result (according to the schema)
	if model == "" {
		model = openai.GPT4o
		// TODO config
		if os.Getenv("DEEPSEEK_API_KEY") != "" {
			model = "deepseek-chat"
		}
		if os.Getenv("GEMINI_API_KEY") != "" {
			model = "gemini-2.5-flash"
		}
	}
	var result R
	var resultJ []byte
	req := openai.ChatCompletionRequest{
		Model:    model,
		Messages: slices.Concat(msgs, []openai.ChatCompletionMessage{usrMsg}),
	}
	// TODO collect usage tokens, save in DB
	_, errLLM := llm.CreateChatCompletion(ctx, req, &result)
	if errLLM == nil {
		resultJ, err = json.MarshalIndent(result, "", "	")
	}

	// persist in SQL
	args := &A{
		DBQuery: func(ctx context.Context) error {
			q := p.A.BaseQueries()

			dbId, err := q.AddPrompt(ctx, db.AddPromptParams{
				SessionID:   sessId,
				Agent:       mach.Id(),
				State:       p.State,
				System:      sysMsg,
				HistoryLen:  int64(len(msgs) - 1),
				Request:     contentStr,
				CreatedAt:   time.Now(),
				MachTimeSum: int64(mach.Time(nil).Sum(nil)),
				MachTime:    fmt.Sprintf("%v", mach.Time(nil)),
			})
			if err != nil {
				return err
			}
			p.A.Log(p.State, "query", "SELECT * FROM prompts WHERE id="+strconv.Itoa(int(dbId)))

			err = q.AddPromptResponse(ctx, db.AddPromptResponseParams{
				Response: sql.NullString{String: string(resultJ), Valid: true},
				ID:       dbId,
			})
			if err != nil {
				return err
			}

			return nil
		},
	}
	mach.EvAdd1(e, ss.BaseDBSaving, Pass(args))

	// handle the LLM err
	if errLLM != nil {
		return nil, fmt.Errorf("failed to run LLM: %w", errLLM)
	}

	p.A.Logger().Info(p.State, "result", result)

	// persist in mem and fs
	p.AppendHistOpenAI(&usrMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	if outDir != "" {
		filename := filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".resp.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	return &result, nil
}

func (p *Prompt[P, R]) AddTool(tool ToolApi) {
	p.tools[tool.Mach().Id()] = tool
}

func (p *Prompt[P, R]) AddDoc(doc *Document) {
	p.docs[doc.Title()] = doc
}

// TODO RemoveTool, RemoveDoc

func (p *Prompt[P, R]) Generate() string {

	// documents
	docs := ""
	for _, t := range p.tools {
		doc := t.Document()
		c := doc.Parts()
		if len(c) == 0 {
			continue
		}
		docs += "## " + doc.Title() + "\n\n" + strings.Join(doc.Parts(), "\n") + "\n\n"
	}
	for _, d := range p.docs {
		c := d.Parts()
		if len(c) == 0 {
			continue
		}
		docs += "## " + d.Title() + "\n\n" + strings.Join(d.Parts(), "\n") + "\n\n"
	}
	if docs != "" {
		docs = "# EXTRA INFORMATION AND CONTEXT\n\n" + docs
	}

	// other sections

	cond := ""
	if p.Conditions != "" {
		cond = "# IDENTITY and PURPOSE\n\n" + p.Conditions + "\n"
	}

	steps := ""
	if p.Steps != "" {
		steps = "# INTERNAL ASSISTANT STEPS\n\n" + p.Steps + "\n"
	}

	result := ""
	if p.Result != "" {
		result = "# OUTPUT INSTRUCTIONS\n\n" + p.Result + "\n"
	}

	// template
	return strings.Trim(shared.Sp(`
		%s
		%s
		%s
		%s
		`, cond, steps, result, docs), "\n ")
}

// OPENAI

func (p *Prompt[P, R]) MsgsOpenAI() []openai.ChatCompletionMessage {
	// system msg
	msgs := []openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleSystem,
		Content: p.Generate(),
	}}

	// get N latest user msgs
	var hist []openai.ChatCompletionMessage
	if p.HistoryMsgLen > 0 {
		hist = p.HistOpenAI()
		if len(hist) > p.HistoryMsgLen {
			hist = hist[len(hist)-p.HistoryMsgLen:]
		}
	}

	return slices.Concat(msgs, hist)
}

func (p *Prompt[P, R]) HistOpenAI() []openai.ChatCompletionMessage {
	return p.histOpenAI
}

func (p *Prompt[P, R]) HistCleanOpenAI() {
	p.histOpenAI = nil
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

// AgentAPI is the top-level public API for all agents to overwrite.
type AgentAPI interface {
	Output(txt string, from shared.From) am.Result

	Mach() *am.Machine
	SetMach(*am.Machine)

	SetOpenAI(c *instructor.InstructorOpenAI)
	OpenAI() *instructor.InstructorOpenAI

	Start() am.Result
	Stop(disposeCtx context.Context) am.Result
	Log(txt string, args ...any)
	Logger() *slog.Logger

	BaseQueries() *db.Queries

	// TODO history
}

// BASE AGENT

type Agent struct {
	*am.ExceptionHandler
	*ssam.DisposedHandlers

	// UserInput is a prompt submitted the user, owned by [schema.AgentStatesDef.Prompt].
	UserInput string
	// OfferList is a list of choices for the user.
	// TODO atomic?
	OfferList []string
	logger    *slog.Logger

	mach                *am.Machine
	prompts             map[string]PromptApi
	history             []openai.Message
	openAI              *instructor.InstructorOpenAI
	maxRetries          int
	dbConn              *sql.DB
	dbQueries           *db.Queries
	dbPending           []func(ctx context.Context) error
	requestingLLMEnter  int
	requestingLLMExit   int
	requestingToolEnter int
	requestingToolExit  int

	// Messages
	Msgs []*shared.Msg

	// init
	states     am.S
	machSchema am.Schema
	ctx        context.Context
	id         string
	// loggerMach is a bridger between slog and machine log
	loggerMach *slog.Logger
}

var _ AgentAPI = &Agent{}

// TODO config
func NewAgent(
	ctx context.Context, id string, states am.S, machSchema am.Schema,
) *Agent {

	a := &Agent{
		DisposedHandlers: &ssam.DisposedHandlers{},
		states:           states,
		machSchema:       machSchema,
		ctx:              ctx,
		id:               id,
		logger:           slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	return a
}

// METHODS

// Init initializes the Agent and returns an error. It does not block.
func (a *Agent) Init(agent AgentAPI, groups any, states am.States) error {
	// validate states schema
	if err := amhelp.Implements(a.states, schema.AgentStates.Names()); err != nil {
		return fmt.Errorf("AgentStates not implemented: %w", err)
	}
	// validate config TODO config
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("DEEPSEEK_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		return fmt.Errorf("OPENAI_API_KEY or DEEPSEEK_API_KEY or GEMINI_API_KEY required")
	}

	// machine
	mach, err := am.NewCommon(a.ctx, a.id, a.machSchema, a.states, agent, nil, nil)
	if err != nil {
		return err
	}
	a.mach = mach
	mach.SetGroups(groups, states)
	shared.MachTelemetry(mach, nil)

	// LLM clients
	// TODO expose as states

	// DEEPSEEK
	deepseekApi := os.Getenv("DEEPSEEK_API_KEY")
	geminiApi := os.Getenv("GEMINI_API_KEY")
	if deepseekApi != "" {
		config := openai.DefaultConfig(deepseekApi)
		// TODO check for OPENAI_BASE_URL first
		config.BaseURL = "https://api.deepseek.com"

		a.SetOpenAI(instructor.FromOpenAI(
			openai.NewClientWithConfig(config),
			// instructor.WithMode(instructor.ModeJSON),
			// TODO config
			instructor.WithMaxRetries(3),
		))

		// GEMINI TODO doesnt return JSONSchema
	} else if geminiApi != "" {
		config := openai.DefaultConfig(geminiApi)
		// TODO check for OPENAI_BASE_URL first
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"

		a.SetOpenAI(instructor.FromOpenAI(
			openai.NewClientWithConfig(config),
			instructor.WithMode(instructor.ModeJSONSchema),
			// TODO config
			instructor.WithMaxRetries(3),
		))

		// OPENAI
	} else {
		a.SetOpenAI(instructor.FromOpenAI(
			openai.NewClient(os.Getenv("OPENAI_API_KEY")),
			// instructor.WithMode(instructor.ModeJSON),
			// TODO config
			instructor.WithMaxRetries(3),
		))
	}

	// machine logger
	a.loggerMach = slog.New(slog.NewTextHandler(
		amhelp.SlogToMachLog{Mach: mach}, amhelp.SlogToMachLogOpts))

	return nil
}

func (a *Agent) db() *sql.DB {
	return a.dbConn
}

// Output is a sugar for adding a [schema.AgentStatesDef.Msg] mutation.
func (a *Agent) Output(txt string, from shared.From) am.Result {
	// TODO check last msg and avoid dups
	return a.Mach().Add1(ss.Msg, Pass(&A{
		Msg: shared.NewMsg(txt, from),
	}))
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

// Start is a sugar for adding a [schema.AgentStatesDef.Start] mutation.
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

// Log will push a log entry to Logger as Info() and optionally the machine log with SECAI_AM_LOG.
// Log accepts the same convention of arguments as [slog.Info].
func (a *Agent) Log(txt string, args ...any) {
	// log into the machine logger TODO config
	if os.Getenv("SECAI_AM_LOG") != "" {
		a.loggerMach.Info(txt, args...)
	}
	a.logger.Info(txt, args...)
}

func (a *Agent) Logger() *slog.Logger {
	return a.logger
}

func (a *Agent) BaseQueries() *db.Queries {
	if a.dbQueries == nil {
		a.dbQueries = db.New(a.dbConn)
	}

	return a.dbQueries
}

func (a *Agent) BuildOffer() string {
	ret := ""
	for i, o := range a.OfferList {
		ret += fmt.Sprintf("%d. %s\n", i+1, o)
	}

	return ret
}

// HANDLERS

func (a *Agent) StartEnter(e *am.Event) bool {
	// TODO err msg
	// TODO config
	return os.Getenv("SECAI_DIR") != ""
}

func (a *Agent) StartState(e *am.Event) {
	dir := os.Getenv("SECAI_DIR")
	err := os.MkdirAll(dir, 0755)
	a.Mach().EvAddErr(e, err, nil)
}

func (a *Agent) BaseDBStartingState(e *am.Event) {
	ctx := a.Mach().NewStateCtx(ss.BaseDBStarting)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}
		// TODO config
		conn, err := db.Open(os.Getenv("SECAI_DIR"))
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			a.Mach().EvAddErrState(e, ss.ErrDB, err, nil)
			return
		}
		a.dbConn = conn

		// truncate
		// TODO DEBUG
		// if err := a.BaseQueries().DropPrompts(ctx); err != nil {
		// 	a.Mach().AddErr(err, nil)
		// 	return
		// }

		// create tables
		_, err = a.dbConn.ExecContext(ctx, db.Schema())
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				a.Mach().EvAddErrState(e, ss.ErrDB, err, nil)
				return
			}
		}

		// exec late queries
		for _, fn := range a.dbPending {
			if ctx.Err() != nil {
				return // expired
			}
			if err := fn(ctx); err != nil {
				a.Mach().EvAddErrState(e, ss.ErrDB, err, nil)
				return
			}
		}

		if ctx.Err() != nil {
			return // expired
		}
		a.Mach().Add1(ss.BaseDBReady, nil)
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

func (a *Agent) BaseDBReadyEnd(e *am.Event) {
	err := a.dbConn.Close()
	if err != nil {
		a.Mach().AddErr(err, nil)
	}
}

func (a *Agent) BaseDBSavingEnter(e *am.Event) bool {
	return shared.ParseArgs(e.Args).DBQuery != nil
}

func (a *Agent) BaseDBSavingState(e *am.Event) {
	// postpone if not DBReady
	fn := shared.ParseArgs(e.Args).DBQuery
	if a.Mach().Not1(ss.BaseDBReady) {
		a.dbPending = append(a.dbPending, fn)
		a.Mach().Remove1(ss.BaseDBSaving, nil)

		return
	}

	// save
	ctx := a.Mach().NewStateCtx(ss.BaseDBReady)
	tick := a.Mach().Tick(ss.BaseDBSaving)
	go func() {
		if ctx.Err() != nil {
			return // expired
		}
		if err := fn(ctx); err != nil {
			a.Mach().AddErr(err, nil)
		}

		// the last one deactivates
		if tick == a.Mach().Tick(ss.BaseDBSaving) {
			a.Mach().Remove1(ss.BaseDBSaving, nil)
		}
	}()
}

// TODO generalize these type of handlers in pkg/x/helpers as counted-multi

func (a *Agent) RequestingLLMEnter(e *am.Event) bool {
	a.requestingLLMEnter++
	return true
}

func (a *Agent) RequestingLLMExit(e *am.Event) bool {
	a.requestingLLMExit++
	return a.requestingLLMEnter == a.requestingLLMExit
}

func (a *Agent) RequestingLLMEnd(e *am.Event) {
	a.Mach().Remove1(ss.Requesting, nil)
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
	a.Mach().Remove1(ss.Requesting, nil)
}

func (a *Agent) RequestingExit(e *am.Event) bool {
	return !a.Mach().Any1(ss.RequestingLLM, ss.RequestingTool)
}

func (a *Agent) PromptEnter(e *am.Event) bool {
	return shared.ParseArgs(e.Args).Prompt != ""
}

func (a *Agent) PromptState(e *am.Event) {
	a.UserInput = shared.ParseArgs(e.Args).Prompt
	a.Output(a.UserInput, shared.FromUser)
}

func (a *Agent) PromptEnd(e *am.Event) {
	a.UserInput = ""
}

func (a *Agent) MsgEnter(e *am.Event) bool {
	args := ParseArgs(e.Args)
	return args.Msg != nil
}

func (a *Agent) InterruptedState(e *am.Event) {
	args := ParseArgs(e.Args)

	// remove the current prompt only (allow for offline prompts)
	a.Mach().Remove1(ss.Prompt, nil)
	if args.IntByTimeout {
		a.Output("Interrupted by a timeout", shared.FromSystem)
	} else {
		a.Output("Interrupted by the user", shared.FromSystem)
	}
}

func (a *Agent) ResumeState(e *am.Event) {
	a.Output("Resumed by the user", shared.FromSystem)
}

// PROMPTS

func (a *Agent) CheckingOfferRefsEnter(e *am.Event) bool {
	args := shared.ParseArgs(e.Args)
	return len(a.OfferList) > 0 && len(args.Prompt) > 0 && args.RetOfferRef != nil
}

func (a *Agent) CheckingOfferRefsState(e *am.Event) {
	args := shared.ParseArgs(e.Args)
	ret := args.RetOfferRef

	i := shared.NumRef(args.Prompt)
	if i >= 0 && i <= len(a.OfferList) {
		// expand number to value
		text := a.OfferList[i-1]

		ret <- &shared.OfferRef{Index: i - 1, Text: text}
		return
	}

	// TODO support LLM checks for longer msgs
	ret <- nil
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
	agent AgentAPI, idSuffix, title string, states am.S, stateSchema am.Schema,
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
	id := "tool-" + idSuffix + "-" + agent.Mach().Id()
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
