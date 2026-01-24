package secai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"dario.cat/mergo"
	instr "github.com/567-labs/instructor-go/pkg/instructor"
	instrc "github.com/567-labs/instructor-go/pkg/instructor/core"
	instrg "github.com/567-labs/instructor-go/pkg/instructor/providers/google"
	instroai "github.com/567-labs/instructor-go/pkg/instructor/providers/openai"

	"github.com/gookit/goutil/dump"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	amhistg "github.com/pancsta/asyncmachine-go/pkg/history/gorm"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	ampipe "github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
	"github.com/pancsta/asyncmachine-go/tools/debugger"
	"github.com/pancsta/asyncmachine-go/tools/debugger/server"
	ssdbg "github.com/pancsta/asyncmachine-go/tools/debugger/states"
	typesdbg "github.com/pancsta/asyncmachine-go/tools/debugger/types"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/genai"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v3"

	"github.com/pancsta/secai/db"
	"github.com/pancsta/secai/db/sqlc"
	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/states"
)

type S = am.S
type A = shared.A

var ss = states.AgentBaseStates
var Pass = shared.Pass
var PassRpc = shared.PassRPC
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
	A            shared.AgentBaseAPI
	// number of previous messages to include
	HistoryMsgLen int
	Msgs          []*PromptMsg

	tools map[string]ToolApi
	docs  map[string]*Document
}

func NewPrompt[P any, R any](agent shared.AgentBaseAPI, state, condition, steps, results string) *Prompt[P, R] {
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
	outDir := cfg.Agent.Dir
	err := os.MkdirAll(filepath.Join(outDir, "prompts"), 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// metrics
	mach.EvAdd1(e, ss.RequestingAI, nil)
	defer mach.EvAdd1(e, ss.RequestedAI, nil)

	// AI provider
	// TODO metric state per provider
	var (
		// TODO enum
		provider string
		model    string
		gemini   *shared.GeminiClient
		openAI   *shared.OpenAIClient
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
		filename := filepath.Join(outDir, "prompts", p.State+".sys.md")
		if err := os.WriteFile(filename, []byte(sys), 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt to output dir under "statename.prompt.json"
		filename = filepath.Join(outDir, "prompts", p.State+".prompt.json")
		if err := os.WriteFile(filename, prompt, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// call the LLM and fill the result (according to the schema)
	var result R
	var resultJ []byte
	var errAI error

	if openAI != nil {
		req := openai.ChatCompletionRequest{
			Model:    openAI.Cfg.Model,
			Messages: instroai.ConversationToMessages(conv),
		}
		// TODO collect usage tokens, save in DB
		_, errAI = openAI.C.CreateChatCompletion(ctx, req, &result)
		if errAI == nil {
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
		_, errAI = gemini.C.CreateChatCompletion(ctx, req, &result)
		if errAI == nil {
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
	if errAI != nil {
		data := []any{"provider", provider, "model", model}
		if openAI != nil {
			data = append(data, "url", openAI.Cfg.URL)
		}
		p.A.LogErr("ai_req", errAI, data...)
		// TODO handle context cancelled
		return nil, fmt.Errorf("ai_%s_%s: %w", provider, model, errAI)
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
		filename := filepath.Join(outDir, "prompts", p.State+".resp.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// confirm config OK TODO handle better
	p.A.Mach().EvAdd1(e, ss.ConfigValid, nil)

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

type AgentBase struct {
	*am.ExceptionHandler
	*ssam.DisposedHandlers

	// UserInput is a prompt submitted the user, owned by [schema.AgentBaseStatesDef.Prompt].
	UserInput string
	// OfferList is a list of choices for the user.
	// TODO atomic?
	OfferList []string
	DbConn    *sql.DB

	agentImpl     shared.AgentAPI
	logger        *slog.Logger
	cfg           *shared.Config
	mach          *am.Machine
	histMem       *amhist.Memory
	histSQLite    *amhistg.Memory
	openAI        []*shared.OpenAIClient
	gemini        []*shared.GeminiClient
	openAIHistory []openai.Message
	maxRetries    int
	dbQueries     *sqlc.Queries
	dbPending     []func(ctx context.Context) error
	states        am.S
	machSchema    am.Schema
	ctx           context.Context
	id            string
	// loggerMach is a bridge between slog and machine log
	loggerMach *slog.Logger
	store      *shared.AgentStore
	dbg        *debugger.Debugger
	dbHist     *sql.DB
	dumper     *dump.Dumper
}

var _ shared.AgentBaseAPI = &AgentBase{}
var _ shared.AgentInit = &AgentBase{}

func NewAgent(ctx context.Context, states am.S, machSchema am.Schema) *AgentBase {
	a := &AgentBase{
		DisposedHandlers: &ssam.DisposedHandlers{},
		states:           states,
		machSchema:       machSchema,
		ctx:              ctx,
		store: &shared.AgentStore{
			M: make(map[string]any),
		},
	}

	return a
}

// METHODS

func (a *AgentBase) Init(
	agentImpl shared.AgentAPI, cfg *shared.Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args any,
) error {

	// validate states schema
	if err := amhelp.Implements(a.states, ss.Names()); err != nil {
		return fmt.Errorf("AgentBaseStates not implemented: %w", err)
	}
	a.agentImpl = agentImpl

	// config
	a.cfg = cfg
	if err := a.buildConfig(); err != nil {
		return err
	}

	// logger
	logFile := shared.ConfigLogPath(cfg.Agent)
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

	// redir legacy logger (eg gotty)
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(io.Discard)
	if cfg.Debug.Verbose {
		log.SetOutput(&SlogWriter{
			Logger: a.logger,
			Level:  slog.LevelInfo,
		})
	}

	// embedded am-dbg TODO ctx?
	err := a.startAmDbg()

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
			AddrDir:  cfg.Agent.Dir,
			Args:     args,
			ParseRpc: shared.ParseRpc,
		}
		if err := arpc.MachRepl(mach, "", &opts); err != nil {
			return err
		}
	}
	a.loggerMach = slog.New(slog.NewTextHandler(
		amhelp.SlogToMachLog{Mach: mach}, amhelp.SlogToMachLogOpts))

	// pprof
	if cfg.Debug.ProfilerAddr != "" {
		go func() {
			http.ListenAndServe(cfg.Debug.ProfilerAddr, nil)
		}()
	}

	// AI clients
	if err := a.initAI(); err != nil {
		return err
	}

	a.Log("initialized", "id", cfg.Agent.ID)

	return nil
}

func (a *AgentBase) AgentImpl() shared.AgentAPI {
	return a.agentImpl
}

// Output is a sugar for adding a [schema.AgentBaseStatesDef.Msg] mutation.
func (a *AgentBase) Output(txt string, from shared.From) am.Result {
	// TODO check last msg and avoid dups
	return a.Mach().Add1(ss.UIMsg, PassRpc(&A{
		Msg: shared.NewMsg(txt, from),
	}))
}

func (a *AgentBase) Mach() *am.Machine {
	return a.mach
}

func (a *AgentBase) SetMach(m *am.Machine) {
	a.mach = m
}

func (a *AgentBase) OpenAI() []*shared.OpenAIClient {
	return a.openAI
}

func (a *AgentBase) Gemini() []*shared.GeminiClient {
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

// Log is an slog logger. It can optionally pipe log entries into the machine log.
func (a *AgentBase) Log(txt string, args ...any) {
	// log into the machine logger TODO config
	if a.cfg.Agent.Log.MachFwd {
		a.loggerMach.Info(txt, args...)
	}
	a.logger.Info(txt, args...)
}

func (a *AgentBase) LogErr(msg string, err error, args ...any) {
	args = append([]any{"err", err}, args...)
	if a.cfg.Agent.Log.MachFwd {
		a.loggerMach.Error(msg, args...)
	}
	a.logger.Error(msg, args...)
}

func (a *AgentBase) Logger() *slog.Logger {
	return a.logger
}

func (a *AgentBase) QueriesBase() *sqlc.Queries {
	if a.dbQueries == nil {
		a.dbQueries = sqlc.New(a.DbConn)
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

// func (a *AgentBase) HistBBolt() *amhistbb.Memory {
// 	return a.histBBolt
// }

func (a *AgentBase) Store() *shared.AgentStore {
	return a.store
}

func (a *AgentBase) DBBase() *sql.DB {
	return a.DbConn
}

func (a *AgentBase) DBG() *debugger.Debugger {
	return a.dbg
}

func (a *AgentBase) DBHistory() *sql.DB {
	return a.dbHist
}

func (a *AgentBase) StoryActivate(e *am.Event, story string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, Pass(&A{
		StatesList:   S{story},
		ActivateList: []bool{true},
	}))
}

func (a *AgentBase) StoryDeactivate(e *am.Event, story string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, Pass(&A{
		StatesList:   S{story},
		ActivateList: []bool{false},
	}))
}

// TODO enc enum
func (a *AgentBase) ValFile(ctx context.Context, name string, val any, enc string) {
	if !a.cfg.Debug.ValFiles {
		return
	}
	if a.dumper == nil {
		a.dumper = dump.NewWithOptions(dump.WithoutColor())
	}
	if ctx == nil {
		ctx = a.ctx
	}

	var (
		data   []byte
		err    error
		suffix string
	)
	switch enc {
	case "yaml":
		suffix = ".yaml"
		data, err = yaml.Marshal(val)
	case "dump":
		suffix = ".txt"
		var buf bytes.Buffer
		a.dumper.Fprint(&buf, val)
		data = buf.Bytes()
	default:
		suffix = ".json"
		data, err = json.MarshalIndent(val, "", "  ")
	}
	if err != nil {
		a.LogErr("dbg_file", err)
		return
	}

	dir := filepath.Join(a.cfg.Agent.Dir, "vals")
	_ = os.MkdirAll(dir, 0755)
	file := filepath.Join(dir, name+suffix)

	a.Mach().Go(ctx, func() {
		err = os.WriteFile(file, data, 0644)
		if err != nil {
			a.LogErr("dbg_file", err)
		}
	})
}

//

// HANDLERS

//

func (a *AgentBase) StartEnter(e *am.Event) bool {
	// TODO err msg
	return a.cfg.Agent.Dir != ""
}

func (a *AgentBase) StartState(e *am.Event) {
	err := os.MkdirAll(a.cfg.Agent.Dir, 0755)
	a.Mach().EvAddErr(e, err, nil)

	// debug states
	if a.dbg != nil {
		a.mach.EvAdd1(e, ss.Debugger, nil)
	}
	if a.cfg.Debug.REPL {
		a.mach.EvAdd1(e, ss.REPL, nil)
	}
}

func (a *AgentBase) ExceptionState(e *am.Event) {
	a.LogErr("exception", am.ParseArgs(e.Args).Err)
}

func (a *AgentBase) HistoryDBStartingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.HistoryDBStarting)

	mach.Fork(ctx, e, func() {
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
			AddErrDB(e, mach, err)
		}
		file := filepath.Join(a.cfg.Agent.Dir, "machine")
		backend := amhist.BackendEnum.Parse(c.Backend)
		if backend == nil {
			backend = &amhist.BackendMemory
		}
		switch *backend {

		case amhist.BackendSqlite:
			cfgSQL := amhistg.Config{BaseConfig: histConfig}
			db, _, err := amhistg.NewDb(file, a.cfg.Debug.Verbose)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
			if a.dbHist, err = db.DB(); err != nil {
				mach.AddErr(err, nil)
				return
			}
			a.histSQLite, err = amhistg.NewMemory(a.ctx, db, mach, cfgSQL, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}

		case amhist.BackendBbolt:
			// TODO fix WASM
			// cfgBB := amhistbb.Config{BaseConfig: histConfig}
			// db, err := amhistbb.NewDb(file)
			// if err != nil {
			// 	mach.AddErr(err, nil)
			// 	return
			// }
			// a.histBBolt, err = amhistbb.NewMemory(a.ctx, db, mach, cfgBB, onErr)
			// if err != nil {
			// 	mach.AddErr(err, nil)
			// 	return
			// }

		default:
			a.histMem, err = amhist.NewMemory(a.ctx, nil, mach, histConfig, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
		}

		// next
		a.Mach().Add1(ss.HistoryDBReady, nil)
	})
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
			AddErrDB(e, a.mach, err)
			return
		}
		a.DbConn = conn

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
				AddErrDB(e, a.mach, err)
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
	err := a.DbConn.Close()
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
	a.Mach().Go(ctx, func() {
		if err := fn(ctx); err != nil {
			AddErrDB(e, a.mach, err)
		}

		// the last one deactivates
		if tick == a.Mach().Tick(ss.BaseDBSaving) {
			a.Mach().Remove1(ss.BaseDBSaving, nil)
		}
	})
}

func (a *AgentBase) RequestingAIState(e *am.Event) {
	a.Mach().EvAdd1(e, ss.Requesting, nil)
}

func (a *AgentBase) RequestedAIState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.Requesting, nil)
}

func (a *AgentBase) RequestingToolState(e *am.Event) {
	a.Mach().EvAdd1(e, ss.Requesting, nil)
}

func (a *AgentBase) RequestedToolState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.Requesting, nil)
}

func (a *AgentBase) RequestingExit(e *am.Event) bool {
	m := a.Mach()
	return m.Tick(ss.RequestingAI) == m.Tick(ss.RequestedAI) &&
		m.Not1(ss.RequestingTool) && m.Not1(ss.RequestedTool)
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

func (a *AgentBase) UIMsgEnter(e *am.Event) bool {
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

func (a *AgentBase) ConfigUpdateEnter(e *am.Event) bool {
	return ParseArgs(e.Args).ConfigAI != nil
}

func (a *AgentBase) ConfigUpdateState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.ConfigUpdate, nil)
	cfg := ParseArgs(e.Args).ConfigAI
	// TODO support >1 backend
	if cfg.OpenAI != nil {
		a.cfg.AI.OpenAI = slices.Concat(a.cfg.AI.OpenAI, cfg.OpenAI)
	}
	if cfg.Gemini != nil {
		a.cfg.AI.Gemini = slices.Concat(a.cfg.AI.Gemini, cfg.Gemini)
	}
	AddErrAI(e, a.mach, a.initAI())

	// restart related states
	a.mach.EvRemove(e, am.S{ss.ErrAI, ss.Exception, ss.InputPending, ss.Mock}, nil)
	a.mach.EvAdd1(e, ss.UICleanOutput, nil)
}

func (a *AgentBase) CheckingMenuRefsEnter(e *am.Event) bool {
	args := shared.ParseArgs(e.Args)
	return len(a.OfferList) > 0 && len(args.Prompt) > 0 && args.RetOfferRef != nil
}

func (a *AgentBase) CheckingMenuRefsState(e *am.Event) {
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

//

// PRIVATE

//

func (a *AgentBase) initAI() error {
	// TODO expose as states

	// open ai
	for i := range a.cfg.AI.OpenAI {
		item := &a.cfg.AI.OpenAI[i]
		if item.Disabled || item.Key == "" {
			continue
		}

		if item.Model == "" {
			item.Model = shared.ConfigDefaultOpenAI().Model
		}

		config := openai.DefaultConfig(item.Key)
		if item.URL != "" {
			a.Log("using OpenAI", "base", item.URL)
			config.BaseURL = item.URL
		}
		a.openAI = append(a.openAI, &shared.OpenAIClient{
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
		if item.Disabled || item.Key == "" {
			continue
		}

		client, err := genai.NewClient(a.ctx, &genai.ClientConfig{
			// TODO enforce schema?
			APIKey: item.Key,
		})
		if err != nil {
			return err
		}
		a.gemini = append(a.gemini, &shared.GeminiClient{
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
	return a.DbConn
}

func (a *AgentBase) buildConfig() error {
	// set env
	os.Setenv(am.EnvAmLog, a.cfg.Agent.Log.MachLevel.Level())
	if a.cfg.Debug.DBGAddr != "" {
		os.Setenv(dbg.EnvAmDbgAddr, a.cfg.Debug.DBGAddr)
		os.Setenv(amhelp.EnvAmLogFull, "1")
	}

	if a.cfg.Agent.Log.MachPrint {
		os.Setenv(amhelp.EnvAmLogPrint, "1")
	}

	// slice defaults
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

	// RPC debug
	if a.cfg.Debug.Verbose {
		if a.cfg.Debug.DBGAddr != "" {
			os.Setenv(arpc.EnvAmRpcDbg, "1")
		}
		os.Setenv(arpc.EnvAmRpcLogClient, "1")
		os.Setenv(arpc.EnvAmRpcLogServer, "1")
		os.Setenv(amhelp.EnvAmHealthcheck, "1")
	}

	return nil
}

func (a *AgentBase) startAmDbg() error {
	// TODO ctx for blocking
	cfg := a.cfg.Debug
	if !cfg.DBGEmbed || cfg.DBGAddr == "" {
		return nil
	}
	// logger and profiler
	// logger := typesdbg.GetLogger(&p, p.OutputDir)
	// typesdbg.StartCpuProfileSrv(ctx, logger, &p)
	// stopProfile := typesdbg.StartCpuProfile(logger, &p)
	// if stopProfile != nil {
	// 	defer stopProfile()
	// }
	// log.SetOutput(logger.Writer())

	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}

	dbgAddr, httpAddr, sshAddr, err := shared.ConfigDbgAddrs(cfg)
	if err != nil {
		return err
	}

	// init the debugger
	dbg, err := debugger.New(a.ctx, debugger.Opts{
		Id: a.cfg.Agent.ID + "-am-dbg",
		// DbgLogLevel:   p.LogLevel,
		// DbgLogger:     logger,
		// ImportData:    p.ImportData,
		OutputClients: true,
		// OutputDiagrams: 1,
		OutputTx:  true,
		OutputLog: true,
		Timelines: 2,
		// ...:           p.FilterLogLevel,
		OutputDir:       a.cfg.Agent.Dir,
		AddrRpc:         dbgAddr,
		AddrHttp:        httpAddr,
		AddrSsh:         sshAddr,
		UiSsh:           true,
		UiWeb:           true,
		EnableMouse:     true,
		EnableClipboard: true,
		// MachUrl:         p.MachUrl,
		// SelectConnected: p.SelectConnected,
		// ShowReader:      p.ViewReader,
		CleanOnConnect: true,
		// MaxMemMb:       p.MaxMemMb,
		// Log2Ttl:    p.LogOpsTtl,
		// ViewNarrow: p.ViewNarrow,
		// ViewRain:   p.ViewRain,
		TailMode: true,
		Version:  version,
		Print: func(txt string, args ...any) {
			// TODO log?
		},
		// Filters: &debugger.OptsFilters{
		// 	SkipOutGroup: p.FilterGroup,
		// 	LogLevel:     p.FilterLogLevel,
		// },
	})
	if err != nil {
		return err
	}
	a.dbg = dbg
	// TODO ErrDebug wait for a.mach
	// err = ampipe.BindErr(dbg.Mach, a.mach, "")
	// if err != nil {
	// 	return err
	// }

	// rpc server
	p := typesdbg.Params{
		OutputDir: a.cfg.Agent.Dir,
		UiWeb:     true,
	}
	go server.StartRpc(dbg.Mach, dbgAddr, nil, p)

	// start and wait till the end
	// TODO move to params
	go dbg.Start("", 0, "", "")
	// TODO fwd err

	// TODO timeout
	<-a.dbg.Mach.When1(ssdbg.Ready, nil)

	return nil
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
	agent shared.AgentBaseAPI, idSuffix, title string, toolStates am.S, toolSchema am.Schema,
) (*Tool, error) {
	// validate the state schema
	if err := amhelp.Implements(toolStates, states.ToolStates.Names()); err != nil {
		return nil, fmt.Errorf("%w: ToolStates not implemented: %w", am.ErrSchema, err)
	}

	// document
	t := &Tool{
		Doc: NewDocument(title),
	}

	// machine
	id := "tool-" + idSuffix + "-" + agent.Mach().Id()
	mach, err := am.NewCommon(agent.Mach().Context(), id, toolSchema, toolStates, nil, agent.Mach(), &am.Opts{
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
	err = ampipe.BindReady(mach, agent.Mach(), "", "")
	if err != nil {
		return nil, err
	}

	// pipe Start from the agent to tool
	err = ampipe.BindStart(agent.Mach(), mach, "", "")
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

// ///// MISC

// ///// ///// /////

type SlogWriter struct {
	Logger *slog.Logger
	Level  slog.Level
}

func (w *SlogWriter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	w.Logger.Log(context.Background(), w.Level, msg, "source", "std_log")

	return len(p), nil
}

// ERRORS

// ErrDB is for [states.AgentBaseStatesDef.ErrDB].
var ErrDB = errors.New("DB error")

// AddErrDB adds [ErrDB].
func AddErrDB(
	event *am.Event, mach *am.Machine, err error, args ...am.A,
) am.Result {
	if err == nil {
		return am.Executed
	}
	err = fmt.Errorf("%w: %w", ErrDB, err)
	return mach.EvAddErrState(event, ss.ErrDB, err, shared.OptArgs(args))
}

// ErrAI is for [states.AgentBaseStatesDef.ErrAI].
var ErrAI = errors.New("AI error")

// AddErrAI adds [ErrAI].
func AddErrAI(
	event *am.Event, mach *am.Machine, err error, args ...am.A,
) am.Result {
	if err == nil {
		return am.Executed
	}
	err = fmt.Errorf("%w: %w", ErrAI, err)
	return mach.EvAddErrState(event, ss.ErrAI, err, shared.OptArgs(args))
}
