package secai

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"dario.cat/mergo"
	instr "github.com/567-labs/instructor-go/pkg/instructor"
	instrc "github.com/567-labs/instructor-go/pkg/instructor/core"
	instrg "github.com/567-labs/instructor-go/pkg/instructor/providers/google"
	instroai "github.com/567-labs/instructor-go/pkg/instructor/providers/openai"
	"github.com/google/uuid"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	amhistbb "github.com/pancsta/asyncmachine-go/pkg/history/bbolt"
	amhistg "github.com/pancsta/asyncmachine-go/pkg/history/gorm"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	"github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/pancsta/secai/db"
	"github.com/pancsta/secai/db/sqlc"
	"github.com/pancsta/secai/schema"
	"github.com/pancsta/secai/shared"
)

type S = am.S
type A = shared.A

var ss = schema.AgentBaseStates
var sessId = uuid.New().String()
var Pass = shared.Pass
var ParseArgs = shared.ParseArgs

// ///// ///// /////

// ///// BASIC TYPES

// ///// ///// /////

var (
	ErrHistNil = errors.New("history is nil")
	ErrNoAI    = errors.New("no AI provider configured")
)

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

type PromptMsg struct {
	From    instrc.Role
	Content string
}

type OpenAIClient struct {
	Cfg *shared.ConfigAIOpenAI
	C   *instr.InstructorOpenAI
}

type GeminiClient struct {
	Cfg *shared.ConfigAIGemini
	C   *instr.InstructorGoogle
}

type PromptApi interface {
	AddTool(tool ToolApi)
	AddDoc(doc *Document)

	GenSysPrompt() string
	Conversation() (*instrc.Conversation, string)
	HistClean()
}

type PromptSchemaless = Prompt[any, any]

type Prompt[P any, R any] struct {
	Conditions   string
	Steps        string
	Result       string
	SchemaParams P
	SchemaResult R
	State        string
	A            AgentAPI
	// number of previous messages to include
	HistoryMsgLen int
	Msgs          []*PromptMsg

	tools map[string]ToolApi
	docs  map[string]*Document
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

func (p *Prompt[P, R]) Exec(e *am.Event, params P) (*R, error) {
	// TODO choose AI per prompt
	if p.State == "" {
		return nil, fmt.Errorf("prompt state not set")
	}

	// prep the machine
	mach := p.A.Mach()
	ctx := mach.NewStateCtx(p.State)
	cfg := p.A.ConfigBase()
	// TODO nest under dir/prompts
	outDir := cfg.Agent.Dir
	mach.EvAdd1(e, ss.RequestingLLM, nil)
	defer mach.EvRemove1(e, ss.RequestingLLM, nil)

	// AI provider
	// TODO metric state per provider
	var (
		// TODO enum
		provider string
		model    string
		gemini   *GeminiClient
		openAI   *OpenAIClient
	)
	if ais := p.A.OpenAI(); len(ais) > 0 {
		openAI = ais[0]
		provider = "openai"
		model = openAI.Cfg.Model
	} else if ais := p.A.Gemini(); len(ais) > 0 {
		gemini = ais[0]
		provider = "gemini"
		model = gemini.Cfg.Model
	} else {
		return nil, fmt.Errorf("%w: %s", ErrNoAI, p.State)
	}

	// gen an LLM prompt
	sessID := mach.Id() + "-" + p.State
	prompt, err := json.MarshalIndent(params, "", "	")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}
	// contentLog, _ := json.Marshal(params)
	contentStr := string(prompt)
	conv, sys := p.Conversation()
	conv.AddUserMessage(contentStr)

	// detailed log
	historyLen := int64(len(conv.GetMessages()) - 1)
	if cfg.Agent.Log.Prompts {
		p.A.Logger().Info("LLM req for "+p.State, "sys_prompt", sys, "historyLen", historyLen)
	}
	// brief log
	p.A.Log(p.State, "prompt", params)
	if outDir != "" {
		// save sys msg to output dir under "statename.sys.md"
		filename := filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".sys.md")
		if err := os.WriteFile(filename, []byte(sys), 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt to output dir under "statename.prompt.json"
		filename = filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".prompt.json")
		if err := os.WriteFile(filename, prompt, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// call the LLM and fill the result (according to the schema)
	var result R
	var resultJ []byte
	var errLLM error

	if openAI != nil {
		req := openai.ChatCompletionRequest{
			Model:    openAI.Cfg.Model,
			Messages: instroai.ConversationToMessages(conv),
		}
		// TODO collect usage tokens, save in DB
		_, errLLM = openAI.C.CreateChatCompletion(ctx, req, &result)
		if errLLM == nil {
			resultJ, err = json.MarshalIndent(result, "", "	")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal result: %w", err)
			}
		}
	} else if gemini != nil {
		req := instr.GoogleRequest{
			Model:    gemini.Cfg.Model,
			Contents: instrg.ConversationToContents(conv),
		}
		_, errLLM = gemini.C.CreateChatCompletion(ctx, req, &result)
		if errLLM == nil {
			resultJ, err = json.MarshalIndent(result, "", "	")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal result: %w", err)
			}
		}
	}

	// persist in SQL
	args := &A{
		DBQuery: func(ctx context.Context) error {
			q := p.A.QueriesBase()

			dbId, err := q.AddPrompt(ctx, sqlc.AddPromptParams{
				SessionID:   sessID,
				Agent:       mach.Id(),
				State:       p.State,
				System:      sys,
				HistoryLen:  historyLen,
				Request:     contentStr,
				Provider:    provider,
				Model:       model,
				CreatedAt:   time.Now(),
				MachTimeSum: int64(mach.Time(nil).Sum(nil)),
				MachTime:    fmt.Sprintf("%v", mach.Time(nil)),
			})
			if err != nil {
				return err
			}
			p.A.Log(p.State, "query", "SELECT * FROM prompts WHERE id="+strconv.Itoa(int(dbId)))

			err = q.AddPromptResponse(ctx, sqlc.AddPromptResponseParams{
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
		data := []any{"provider", provider, "model", model}
		if openAI != nil {
			data = append(data, "url", openAI.Cfg.URL)
		}
		p.A.Err("llm_req", errLLM, data...)
		// TODO handle context cancelled
		return nil, fmt.Errorf("llm_%s: %w", provider, errLLM)
	}

	p.A.Logger().Info(p.State, "result", result)

	// persist in mem and fs
	if p.HistoryMsgLen > 0 {
		p.Msgs = append(p.Msgs, &PromptMsg{
			From:    instrc.RoleUser,
			Content: contentStr,
		}, &PromptMsg{
			From:    instrc.RoleAssistant,
			Content: string(resultJ),
		})
	}
	if outDir != "" {
		filename := filepath.Join(outDir, p.A.Mach().Id()+"-"+p.State+".resp.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	return &result, nil
}

// AddTool registers a SECAI TOOL which then exports it's documents into the system prompt. This is different from an AI tool.
func (p *Prompt[P, R]) AddTool(tool ToolApi) {
	p.tools[tool.Mach().Id()] = tool
}

// AddDoc adds a document into the system prompt.
func (p *Prompt[P, R]) AddDoc(doc *Document) {
	p.docs[doc.Title()] = doc
}

// TODO RemoveTool, RemoveDoc

// GenSysPrompt generates a system prompt.
func (p *Prompt[P, R]) GenSysPrompt() string {

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

// Conversation will create a conversation with history and system prompt, return sys prompt on the side.
func (p *Prompt[P, R]) Conversation() (*instrc.Conversation, string) {
	sys := p.GenSysPrompt()
	c := instrc.NewConversation(sys)
	limit := max(100, p.HistoryMsgLen)
	if l := len(p.Msgs); l > limit {
		p.Msgs = p.Msgs[l-limit:]
	}
	for i := len(p.Msgs) - 1; i >= 0; i-- {
		msg := p.Msgs[i]
		c.AddMessage(msg.From, msg.Content)
	}

	return c, sys
}

func (p *Prompt[P, R]) HistClean() {
	p.Msgs = nil
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

// AgentInit is an init func for extendable agents (non-top level ones).
type AgentInit interface {
	Init(
		agentImpl AgentAPI, cfg *shared.Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args any,
	) error
}

// AgentAPI is the top-level public API for all agents to implement.
type AgentAPI interface {
	Output(txt string, from shared.From) am.Result

	Mach() *am.Machine
	Hist() (amhist.MemoryApi, error)
	HistMem() *amhist.Memory
	HistSQLite() *amhistg.Memory
	HistBBolt() *amhistbb.Memory

	ConfigBase() *shared.Config
	OpenAI() []*OpenAIClient
	Gemini() []*GeminiClient

	Start() am.Result
	Stop(disposeCtx context.Context) am.Result
	Log(msg string, args ...any)
	Err(msg string, err error, args ...any)
	Logger() *slog.Logger

	QueriesBase() *sqlc.Queries
	HistoryStates() am.S
}

// BASE AGENT

type AgentBase struct {
	*am.ExceptionHandler
	*ssam.DisposedHandlers

	// UserInput is a prompt submitted the user, owned by [schema.AgentBaseStatesDef.Prompt].
	UserInput string
	// OfferList is a list of choices for the user.
	// TODO atomic?
	OfferList []string
	// Messages
	Msgs []*shared.Msg

	logger              *slog.Logger
	cfg                 *shared.Config
	mach                *am.Machine
	histMem             *amhist.Memory
	histSQLite          *amhistg.Memory
	histBBolt           *amhistbb.Memory
	prompts             map[string]PromptApi
	openAI              []*OpenAIClient
	gemini              []*GeminiClient
	openAIHistory       []openai.Message
	maxRetries          int
	dbConn              *sql.DB
	dbQueries           *sqlc.Queries
	dbPending           []func(ctx context.Context) error
	requestingLLMEnter  int
	requestingLLMExit   int
	requestingToolEnter int
	requestingToolExit  int
	states              am.S
	machSchema          am.Schema
	ctx                 context.Context
	id                  string
	// loggerMach is a bridge between slog and machine log
	loggerMach *slog.Logger
	agentImpl  AgentAPI
	args       any
}

var _ AgentAPI = &AgentBase{}
var _ AgentInit = &AgentBase{}

func NewAgent(
	ctx context.Context, states am.S, machSchema am.Schema,
) *AgentBase {

	a := &AgentBase{
		DisposedHandlers: &ssam.DisposedHandlers{},
		states:           states,
		machSchema:       machSchema,
		ctx:              ctx,
	}

	return a
}

// METHODS

// Init initializes the AgentLLM and returns an error. It does not block.
func (a *AgentBase) Init(
	agentImpl AgentAPI, cfg *shared.Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args any,
) error {

	// validate states schema
	if err := amhelp.Implements(a.states, schema.AgentBaseStates.Names()); err != nil {
		return fmt.Errorf("AgentBaseStates not implemented: %w", err)
	}
	a.agentImpl = agentImpl

	// config
	a.cfg = cfg
	if err := a.buildConfig(); err != nil {
		return err
	}

	// logger
	logFile := filepath.Join(cfg.Agent.Dir, cfg.Agent.ID+".jsonl")
	if v := cfg.Agent.Log.File; v != "" {
		logFile = v
	}
	rotator := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
	}
	a.logger = slog.New(slog.NewJSONHandler(rotator, &slog.HandlerOptions{
		// TODO more levels
		Level: slog.LevelInfo,
	}))

	// machine
	mach, err := am.NewCommon(a.ctx, cfg.Agent.ID, a.machSchema, a.states, agentImpl, nil, nil)
	if err != nil {
		return err
	}
	a.mach = mach
	mach.SetGroups(groups, states)
	shared.MachTelemetry(mach, logArgs)
	if cfg.Debug.REPL {
		opts := arpc.ReplOpts{
			ArgsPrefix: cfg.Agent.ID,
			AddrDir:    cfg.Agent.Dir,
			Args:       args,
		}
		if err := arpc.MachRepl(mach, "", &opts); err != nil {
			return err
		}
	}
	a.loggerMach = slog.New(slog.NewTextHandler(
		amhelp.SlogToMachLog{Mach: mach}, amhelp.SlogToMachLogOpts))

	// AI clients
	if err := a.initAI(); err != nil {
		return err
	}

	a.Log("initialized", "id", cfg.Agent.ID)

	return nil
}

func (a *AgentBase) buildConfig() error {
	for i, item := range a.cfg.AI.OpenAI {
		baseDefault := shared.ConfigDefaultOpenAI()
		if err := mergo.Merge(&baseDefault, item, mergo.WithOverride); err != nil {
			return err
		}
		a.cfg.AI.OpenAI[i] = baseDefault
	}
	for i, item := range a.cfg.AI.Gemini {
		baseDefault := shared.ConfigDefaultGemini()
		if err := mergo.Merge(&baseDefault, item, mergo.WithOverride); err != nil {
			return err
		}
		a.cfg.AI.Gemini[i] = baseDefault
	}

	return nil
}

func (a *AgentBase) initAI() error {
	// TODO expose as states

	// open ai
	for i := range a.cfg.AI.OpenAI {
		item := &a.cfg.AI.OpenAI[i]
		if item.Disabled {
			continue
		}

		config := openai.DefaultConfig(item.Key)
		if item.URL != "" {
			a.Log("using OpenAI", "base", item.URL)
			config.BaseURL = item.URL
		}
		a.openAI = append(a.openAI, &OpenAIClient{
			Cfg: item,
			C: instr.FromOpenAI(
				openai.NewClientWithConfig(config),
				instr.WithMode(instr.ModeJSONSchema),
				instr.WithMaxRetries(item.Retries),
			),
		})
	}

	// gemini
	for i := range a.cfg.AI.Gemini {
		item := &a.cfg.AI.Gemini[i]
		if item.Disabled {
			continue
		}

		client, err := genai.NewClient(a.ctx, &genai.ClientConfig{
			// TODO enforce schema?
			APIKey: item.Key,
		})
		if err != nil {
			return err
		}
		a.gemini = append(a.gemini, &GeminiClient{
			Cfg: item,
			C: instr.FromGoogle(client,
				instr.WithMode(instr.ModeJSONSchema),
				// TODO config
				instr.WithMaxRetries(item.Retries),
			),
		})
	}

	return nil
}

func (a *AgentBase) db() *sql.DB {
	return a.dbConn
}

// Output is a sugar for adding a [schema.AgentBaseStatesDef.Msg] mutation.
func (a *AgentBase) Output(txt string, from shared.From) am.Result {
	// TODO check last msg and avoid dups
	return a.Mach().Add1(ss.Msg, Pass(&A{
		Msg: shared.NewMsg(txt, from),
	}))
}

func (a *AgentBase) Mach() *am.Machine {
	return a.mach
}

func (a *AgentBase) SetMach(m *am.Machine) {
	a.mach = m
}

func (a *AgentBase) OpenAI() []*OpenAIClient {
	return a.openAI
}

func (a *AgentBase) Gemini() []*GeminiClient {
	return a.gemini
}

// Start is a sugar for adding a [schema.AgentBaseStatesDef.Start] mutation.
func (a *AgentBase) Start() am.Result {
	return a.Mach().Add1(ss.Start, nil)
}

func (a *AgentBase) Stop(disposeCtx context.Context) am.Result {
	res := a.Mach().Remove1(ss.Start, nil)
	if disposeCtx != nil {
		a.Mach().Add1(ss.Disposing, nil)
		<-a.Mach().When1(ss.Disposed, disposeCtx)
	}

	return res
}

// Log will push a log entry to Logger as Info() and optionally the machine log with SECAI_LOG_AM.
// Log accepts the same convention of arguments as [slog.Info].
func (a *AgentBase) Log(txt string, args ...any) {
	// log into the machine logger TODO config
	if a.cfg.Agent.Log.Machine {
		a.loggerMach.Info(txt, args...)
	}
	a.logger.Info(txt, args...)
}

func (a *AgentBase) Err(msg string, err error, args ...any) {
	args = append([]any{"err", err}, args...)
	a.logger.Error(msg, args...)
}

func (a *AgentBase) Logger() *slog.Logger {
	return a.logger
}

func (a *AgentBase) QueriesBase() *sqlc.Queries {
	if a.dbQueries == nil {
		a.dbQueries = sqlc.New(a.dbConn)
	}

	return a.dbQueries
}

func (a *AgentBase) BuildOffer() string {
	ret := ""
	for i, o := range a.OfferList {
		ret += fmt.Sprintf("%d. %s\n", i+1, o)
	}

	return ret
}

func (a *AgentBase) Hist() (amhist.MemoryApi, error) {
	// TODO custom UnmarshalText for parsing enums
	parsed := amhist.BackendEnum.Parse(a.cfg.Agent.History.Backend)
	switch *parsed {
	case amhist.BackendSqlite:
		if a.histSQLite == nil {
			return nil, ErrHistNil
		}
		return a.histSQLite, nil
	case amhist.BackendBbolt:
		if a.histBBolt == nil {
			return nil, ErrHistNil
		}
		return a.histBBolt, nil
	default:
		if a.histMem == nil {
			return nil, ErrHistNil
		}
		return a.histMem, nil
	}
}

func (a *AgentBase) ConfigBase() *shared.Config {
	return a.cfg
}

func (a *AgentBase) HistMem() *amhist.Memory {
	return a.histMem
}

func (a *AgentBase) HistSQLite() *amhistg.Memory {
	return a.histSQLite
}

func (a *AgentBase) HistBBolt() *amhistbb.Memory {
	return a.histBBolt
}

func (a *AgentBase) HistoryStates() am.S {
	panic("implement in subclass")
}

// HANDLERS

func (a *AgentBase) StartEnter(e *am.Event) bool {
	// TODO err msg
	return a.cfg.Agent.Dir != ""
}

func (a *AgentBase) StartState(e *am.Event) {
	err := os.MkdirAll(a.cfg.Agent.Dir, 0755)
	a.Mach().EvAddErr(e, err, nil)
}

func (a *AgentBase) HistoryDBStartingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.HistoryDBStarting)

	// fork
	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		c := a.cfg.Agent.History

		// tracking config
		tracked := a.agentImpl.HistoryStates()
		histConfig := amhist.BaseConfig{
			TrackedStates:    tracked,
			Changed:          tracked,
			MaxRecords:       c.Max,
			StoreTransitions: true,
		}

		// init
		var err error
		onErr := func(err error) {
			mach.AddErr(err, nil)
		}
		file := filepath.Join(a.cfg.Agent.Dir, "machine")
		backend := amhist.BackendEnum.Parse(c.Backend)
		if backend == nil {
			backend = &amhist.BackendMemory
		}
		switch *backend {

		case amhist.BackendSqlite:
			cfgSQL := amhistg.Config{BaseConfig: histConfig}
			db, _, err := amhistg.NewDb(file, a.cfg.Debug.Misc)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
			a.histSQLite, err = amhistg.NewMemory(a.ctx, db, mach, cfgSQL, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}

		case amhist.BackendBbolt:
			cfgBB := amhistbb.Config{BaseConfig: histConfig}
			db, err := amhistbb.NewDb(file)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
			a.histBBolt, err = amhistbb.NewMemory(a.ctx, db, mach, cfgBB, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}

		default:
			a.histMem, err = amhist.NewMemory(a.ctx, nil, mach, histConfig, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
		}

		// next
		a.Mach().Add1(ss.HistoryDBReady, nil)
	}()
}

func (a *AgentBase) HistoryDBReadyEnd(e *am.Event) {
	hist, _ := a.Hist()
	err := hist.Dispose()
	if err != nil {
		a.Mach().AddErr(err, nil)
	}
}

func (a *AgentBase) BaseDBStartingState(e *am.Event) {
	ctx := a.Mach().NewStateCtx(ss.BaseDBStarting)

	go func() {
		if ctx.Err() != nil {
			return // expired
		}

		// init DB
		dbFile := filepath.Join(a.cfg.Agent.Dir, "secai.sqlite")
		conn, _, err := db.Open(dbFile)
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
		// if err := a.QueriesBase().DropPrompts(ctx); err != nil {
		// 	a.Mach().AddErr(err, nil)
		// 	return
		// }

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

func (a *AgentBase) BaseDBReadyEnd(e *am.Event) {
	err := a.dbConn.Close()
	if err != nil {
		a.Mach().AddErr(err, nil)
	}
}

func (a *AgentBase) BaseDBSavingEnter(e *am.Event) bool {
	return shared.ParseArgs(e.Args).DBQuery != nil
}

func (a *AgentBase) BaseDBSavingState(e *am.Event) {
	// postpone if not BaseDBReady
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

func (a *AgentBase) RequestingLLMEnter(e *am.Event) bool {
	a.requestingLLMEnter++
	return true
}

func (a *AgentBase) RequestingLLMExit(e *am.Event) bool {
	a.requestingLLMExit++
	return a.requestingLLMEnter == a.requestingLLMExit
}

func (a *AgentBase) RequestingLLMEnd(e *am.Event) {
	a.Mach().Remove1(ss.Requesting, nil)
}

func (a *AgentBase) RequestingToolEnter(e *am.Event) bool {
	a.requestingToolEnter++
	return true
}

func (a *AgentBase) RequestingToolExit(e *am.Event) bool {
	a.requestingToolExit++
	return a.requestingToolEnter == a.requestingToolExit
}

func (a *AgentBase) RequestingToolEnd(e *am.Event) {
	a.Mach().Remove1(ss.Requesting, nil)
}

func (a *AgentBase) RequestingExit(e *am.Event) bool {
	return !a.Mach().Any1(ss.RequestingLLM, ss.RequestingTool)
}

func (a *AgentBase) PromptEnter(e *am.Event) bool {
	return shared.ParseArgs(e.Args).Prompt != ""
}

func (a *AgentBase) PromptState(e *am.Event) {
	a.UserInput = shared.ParseArgs(e.Args).Prompt
	a.Output(a.UserInput, shared.FromUser)
}

func (a *AgentBase) PromptEnd(e *am.Event) {
	a.UserInput = ""
}

func (a *AgentBase) MsgEnter(e *am.Event) bool {
	args := ParseArgs(e.Args)
	return args.Msg != nil
}

func (a *AgentBase) InterruptedState(e *am.Event) {
	args := ParseArgs(e.Args)

	// remove the current prompt only (allow for offline prompts)
	a.Mach().Remove1(ss.Prompt, nil)
	if args.IntByTimeout {
		a.Output("Interrupted by a timeout", shared.FromSystem)
	} else {
		a.Output("Interrupted by the user", shared.FromSystem)
	}
}

func (a *AgentBase) ResumeState(e *am.Event) {
	a.Output("Resumed by the user", shared.FromSystem)
}

// PROMPTS

func (a *AgentBase) CheckingOfferRefsEnter(e *am.Event) bool {
	args := shared.ParseArgs(e.Args)
	return len(a.OfferList) > 0 && len(args.Prompt) > 0 && args.RetOfferRef != nil
}

func (a *AgentBase) CheckingOfferRefsState(e *am.Event) {
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
	cfg := agent.ConfigBase()
	// TODO move to MachTelemetry
	if cfg.Debug.REPL {
		// TODO typesafe args
		opts := arpc.ReplOpts{
			AddrDir: cfg.Agent.Dir,
		}
		if err := arpc.MachRepl(mach, "", &opts); err != nil {
			return nil, err
		}
	}

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
