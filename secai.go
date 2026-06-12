package secai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"

	"dario.cat/mergo"
	"github.com/alexflint/go-arg"
	"github.com/gookit/goutil/dump"
	instr "github.com/jxnl/instructor-go/pkg/instructor"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	amhist "github.com/pancsta/asyncmachine-go/pkg/history"
	amhistg "github.com/pancsta/asyncmachine-go/pkg/history/gorm"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ssam "github.com/pancsta/asyncmachine-go/pkg/states"
	ampipe "github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
	"github.com/pancsta/asyncmachine-go/tools/debugger"
	"github.com/pancsta/asyncmachine-go/tools/debugger/server"
	statesdbg "github.com/pancsta/asyncmachine-go/tools/debugger/states"
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

var ssdbg = statesdbg.DebuggerStates
var ss = states.AgentBaseStates

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

	agentImpl  shared.AgentAPI
	logger     *slog.Logger
	cfg        *shared.Config
	mach       *am.Machine
	histMem    *amhist.Memory
	histSQLite *amhistg.Memory
	aiClients  []*shared.AIClient
	maxRetries int
	dbQueries  *sqlc.Queries
	dbPending  []func(ctx context.Context) error
	states     am.S
	machSchema am.Schema
	ctx        context.Context
	id         string
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

// ----- METHODS

func (a *AgentBase) Init(
	agentImpl shared.AgentAPI, cfg *shared.Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args []am.ArgsApi,
) error {

	// validate states schema
	if err := amhelp.Implements(a.states, ss.Names()); err != nil {
		return fmt.Errorf("AgentBaseStates not implemented: %w", err)
	}
	a.agentImpl = agentImpl

	// config
	if err := a.SetConfigBase(cfg); err != nil {
		return err
	}

	// embedded am-dbg TODO ctx?
	err := a.startAmDbg()

	// machine
	mach, err := am.NewCommon(a.ctx, cfg.Agent.ID, a.machSchema, a.states, agentImpl, nil, &am.Opts{
		DontLogId: true,
	})
	if err != nil {
		return err
	}
	a.mach = mach
	mach.SetGroups(groups, states)
	shared.MachTelemetry(mach, logArgs)

	// aRPC for IPC
	mux, err := arpc.NewMux(a.ctx, "localhost:0", cfg.Agent.ID, mach, nil)
	if err != nil {
		return err
	}
	mux.Start(nil)
	go func() {
		<-mux.Mach.When1(ssrpc.MuxStates.Ready, a.ctx)
		err := os.WriteFile(filepath.Join(cfg.Agent.Dir, mach.Id()+".addr"), []byte(mux.Addr), 0644)
		if err != nil {
			mach.AddErr(err, nil)
			return
		}
		a.Log("ipc_ready", "addr", mux.Addr)
	}()

	// REPL for users TODO merge with IPC?
	if cfg.Debug.REPL {
		opts := arpc.ReplOpts{
			AddrDir: filepath.Join(cfg.Agent.Dir, "repl"),
			Args:    slices.Concat(args, shared.ArgsRPC),
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

	a.Log("initialized", "id", cfg.Agent.ID)

	return nil
}

func (a *AgentBase) AgentImpl() shared.AgentAPI {
	return a.agentImpl
}

// Output is a sugar for adding a [schema.AgentBaseStatesDef.Msg] mutation.
func (a *AgentBase) Output(txt string, from shared.From) am.Result {
	// TODO check last msg and avoid dups
	return a.Mach().Add1(ss.UIMsg, am.Pass(&shared.AUIMsg{
		Msg: shared.NewMsg(txt, from),
	}))
}

func (a *AgentBase) Mach() *am.Machine {
	return a.mach
}

func (a *AgentBase) SetMach(m *am.Machine) {
	a.mach = m
}

func (a *AgentBase) AIs() []*shared.AIClient {
	return slices.Clone(a.aiClients)
}

// Start is a sugar for adding a [schema.AgentBaseStatesDef.Start] mutation.
func (a *AgentBase) Start() am.Result {
	return a.Mach().Add1(ss.Start, nil)
}

func (a *AgentBase) Stop(disposeCtx context.Context) {
	amhelp.Dispose(a.Mach())
	<-a.Mach().When1(ss.Disposed, disposeCtx)
}

// Log is an slog logger. It can optionally pipe log entries into the machine log.
func (a *AgentBase) Log(txt string, args ...any) {
	// log into the machine logger TODO config
	if a.cfg.Agent.Log.MachFwd && a.loggerMach != nil {
		a.loggerMach.Info(txt, args...)
	}
	a.logger.Info(txt, args...)
}

// LogDebug is an slog logger. It can optionally pipe log entries into the machine log.
func (a *AgentBase) LogDebug(txt string, args ...any) {
	// log into the machine logger TODO config
	if a.cfg.Agent.Log.MachFwd && a.loggerMach != nil {
		a.loggerMach.Debug(txt, args...)
	}
	a.logger.Debug(txt, args...)
}

// LogWarn is an slog logger. It can optionally pipe log entries into the machine log.
func (a *AgentBase) LogWarn(txt string, args ...any) {
	// log into the machine logger TODO config
	if a.cfg.Agent.Log.MachFwd && a.loggerMach != nil {
		a.loggerMach.Warn(txt, args...)
	}
	a.logger.Warn(txt, args...)
}

func (a *AgentBase) LogErr(msg string, err error, args ...any) {
	args = append([]any{"err", err}, args...)
	if a.cfg.Agent.Log.MachFwd && a.loggerMach != nil {
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
			return nil, shared.ErrHistNil
		}
		return a.histSQLite, nil
	default:
		if a.histMem == nil {
			return nil, shared.ErrHistNil
		}
		return a.histMem, nil
	}
}

func (a *AgentBase) SetConfigBase(cfg *shared.Config) error {
	a.cfg = cfg
	if err := a.buildConfig(); err != nil {
		return err
	}

	// mkdir
	if err := os.MkdirAll(a.cfg.Agent.Dir, 0755); err != nil {
		return err
	}
	logFile := cfg.Agent.LogPath(false)
	logDir := filepath.Dir(logFile)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	// logger
	if a.logger == nil {
		rotator := &lumberjack.Logger{
			Filename:   logFile,
			MaxSize:    10,
			MaxBackups: 5,
			MaxAge:     30,
			Compress:   true,
		}
		a.logger = slog.New(slog.NewJSONHandler(rotator, &slog.HandlerOptions{
			Level: cfg.Agent.Log.Level,
		}))

		// redir legacy logger (eg gotty)
		log.SetFlags(0)
		log.SetPrefix("")
		log.SetOutput(io.Discard)
		if cfg.Debug.Verbose {
			log.SetOutput(&SlogWriter{
				Logger: a.logger,
				Level:  slog.LevelDebug,
			})
		}
	}

	// AI clients
	if err := a.initAI(); err != nil {
		return err
	}

	return nil
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

func (a *AgentBase) StoryActivate(e *am.Event, storyState string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, am.Pass(&shared.AStoryChanged{
		StatesList:   S{storyState},
		ActivateList: []bool{true},
	}))
}

func (a *AgentBase) StoryDeactivate(e *am.Event, storyState string) am.Result {
	mach := a.Mach()

	// TODO check the story group for [story] and return am.Canceled

	return mach.EvAdd(e, S{ss.StoryChanged, ss.CheckStories}, am.Pass(&shared.AStoryChanged{
		StatesList:   S{storyState},
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

var _ = ss.Start

func (a *AgentBase) StartEnter(e *am.Event) bool {
	// TODO err msg
	return a.cfg.Agent.Dir != ""
}

func (a *AgentBase) StartState(e *am.Event) {
	// debug states
	if a.dbg != nil {
		a.mach.EvAdd1(e, ss.Debugger, nil)
	}
	if a.cfg.Debug.REPL {
		a.mach.EvAdd1(e, ss.REPL, nil)
	}
}

var _ = ss.Exception

func (a *AgentBase) ExceptionState(e *am.Event) {
	a.LogErr("exception", am.ParseArgs[am.AException](e.Args).Err)
}

var _ = ss.HistoryDBStarting

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
			shared.AddErrDB(e, mach, err)
		}
		file := filepath.Join(a.cfg.Agent.Dir, "machine")
		backend := amhist.BackendEnum.Parse(c.Backend)
		if backend == nil {
			backend = &amhist.BackendMemory
		}
		switch *backend {

		case amhist.BackendSqlite:
			cfgSQL := amhistg.Config{BaseConfig: histConfig}
			db, _, err := amhistg.NewDb(file, a.cfg.Debug.VerboseSQL)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
			if a.dbHist, err = db.DB(); err != nil {
				mach.AddErr(err, nil)
				return
			}
			a.histSQLite, err = amhistg.NewMemory(mach.Context(), db, mach, cfgSQL, onErr)
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
			// a.histBBolt, err = amhistbb.NewMemory(mach.Context(), db, mach, cfgBB, onErr)
			// if err != nil {
			// 	mach.AddErr(err, nil)
			// 	return
			// }

		default:
			a.histMem, err = amhist.NewMemory(mach.Context(), nil, mach, histConfig, onErr)
			if err != nil {
				mach.AddErr(err, nil)
				return
			}
		}

		// next
		a.Mach().Add1(ss.HistoryDBReady, nil)
	})
}

var _ = ss.HistoryDBReady

func (a *AgentBase) HistoryDBReadyEnd(e *am.Event) {
	hist, _ := a.Hist()
	err := hist.Dispose()
	if err != nil {
		a.Mach().AddErr(err, nil)
	}
}

var _ = ss.BaseDBStarting

func (a *AgentBase) BaseDBStartingState(e *am.Event) {
	mach := a.Mach()
	ctx := mach.NewStateCtx(ss.BaseDBStarting)

	mach.Fork(ctx, e, func() {
		// init DB
		dbFile := filepath.Join(a.cfg.Agent.Dir, "secai.sqlite")
		conn, _, err := db.Open(dbFile)
		if ctx.Err() != nil {
			return // expired
		}
		if err != nil {
			shared.AddErrDB(e, a.mach, err)
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
				shared.AddErrDB(e, a.mach, err)
				return
			}
		}

		if ctx.Err() != nil {
			return // expired
		}
		a.Mach().Add1(ss.BaseDBReady, nil)
	})

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

var _ = ss.BaseDBReady

func (a *AgentBase) BaseDBReadyEnd(e *am.Event) {
	err := a.DbConn.Close()
	if err != nil {
		a.Mach().AddErr(err, nil)
	}
}

var _ = ss.BaseDBSaving

func (a *AgentBase) BaseDBSavingEnter(e *am.Event) bool {
	return am.ParseArgs[shared.ABaseDBSaving](e.Args).DBQuery != nil
}

func (a *AgentBase) BaseDBSavingState(e *am.Event) {
	// postpone if not BaseDBReady
	fn := am.ParseArgs[shared.ABaseDBSaving](e.Args).DBQuery
	if a.Mach().Not1(ss.BaseDBReady) {
		a.dbPending = append(a.dbPending, fn)
		a.Mach().Remove1(ss.BaseDBSaving, nil)

		return
	}

	// save
	ctx := a.Mach().NewStateCtx(ss.BaseDBReady)
	tick := a.Mach().Tick(ss.BaseDBSaving)
	a.Mach().Fork(ctx, e, func() {
		if err := fn(ctx); err != nil {
			shared.AddErrDB(e, a.mach, err)
		}

		// the last one deactivates
		if tick == a.Mach().Tick(ss.BaseDBSaving) {
			a.Mach().Remove1(ss.BaseDBSaving, nil)
		}
	})
}

var _ = ss.RequestingAI

func (a *AgentBase) RequestingAIState(e *am.Event) {
	a.Mach().EvAdd1(e, ss.Requesting, nil)
}

var _ = ss.RequestedAI

func (a *AgentBase) RequestedAIState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.Requesting, nil)
}

var _ = ss.RequestingTool

func (a *AgentBase) RequestingToolState(e *am.Event) {
	a.Mach().EvAdd1(e, ss.Requesting, nil)
}

var _ = ss.RequestedTool

func (a *AgentBase) RequestedToolState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.Requesting, nil)
}

var _ = ss.Requesting

func (a *AgentBase) RequestingExit(e *am.Event) bool {
	m := a.Mach()
	return m.Tick(ss.RequestingAI) == m.Tick(ss.RequestedAI) &&
		m.Not1(ss.RequestingTool) && m.Not1(ss.RequestedTool)
}

var _ = ss.Prompt

func (a *AgentBase) PromptEnter(e *am.Event) bool {
	return am.ParseArgs[shared.APrompt](e.Args).Prompt != ""
}

func (a *AgentBase) PromptState(e *am.Event) {
	a.UserInput = am.ParseArgs[shared.APrompt](e.Args).Prompt
	a.Output(a.UserInput, shared.FromUser)
}

func (a *AgentBase) PromptEnd(e *am.Event) {
	a.UserInput = ""
}

func (a *AgentBase) UIMsgEnter(e *am.Event) bool {
	args := am.ParseArgs[shared.AUIMsg](e.Args)
	return args.Msg != nil
}

var _ = ss.Interrupted

func (a *AgentBase) InterruptedState(e *am.Event) {
	args := am.ParseArgs[shared.AInterrupted](e.Args)
	intByTimeout := args.IntByTimeout

	// remove the current prompt only (allow for offline prompts)
	a.Mach().Remove1(ss.Prompt, nil)
	if intByTimeout {
		a.Output("Interrupted by a timeout", shared.FromSystem)
	} else {
		a.Output("Interrupted by the user", shared.FromSystem)
	}
}

var _ = ss.Resume

func (a *AgentBase) ResumeState(e *am.Event) {
	a.Output("TabReused by the user", shared.FromSystem)
}

var _ = ss.ConfigUpdate

func (a *AgentBase) ConfigUpdateEnter(e *am.Event) bool {
	return am.ParseArgs[shared.AConfigUpdate](e.Args).ConfigAI != nil
}

func (a *AgentBase) ConfigUpdateState(e *am.Event) {
	a.Mach().EvRemove1(e, ss.ConfigUpdate, nil)
	cfg := am.ParseArgs[shared.AConfigUpdate](e.Args).ConfigAI
	if cfg == nil {
		return
	}
	// TODO support >1 backend
	if cfg.OpenAI != nil {
		a.cfg.AI.OpenAI = slices.Concat(a.cfg.AI.OpenAI, cfg.OpenAI)
	}
	if cfg.Gemini != nil {
		a.cfg.AI.Gemini = slices.Concat(a.cfg.AI.Gemini, cfg.Gemini)
	}
	shared.AddErrAI(e, a.mach, a.initAI())

	// restart related states
	a.mach.EvRemove(e, am.S{ss.ErrAI, ss.Exception, ss.InputPending, ss.Mock}, nil)
	a.mach.EvAdd1(e, ss.UICleanOutput, nil)
}

var _ = ss.CheckingMenuRefs

func (a *AgentBase) CheckingMenuRefsEnter(e *am.Event) bool {
	args := am.ParseArgs[shared.ACheckingMenuRefs](e.Args)
	return len(a.OfferList) > 0 && len(args.Prompt) > 0 && args.RetOfferRef != nil
}

func (a *AgentBase) CheckingMenuRefsState(e *am.Event) {
	args := am.ParseArgs[shared.ACheckingMenuRefs](e.Args)
	ret := args.RetOfferRef

	i := shared.NumRef(args.Prompt)
	if i >= 0 && i <= len(a.OfferList) {
		// expand number to value
		text := a.OfferList[i-1]

		ret <- &shared.OfferRef{Index: i - 1, Text: text}
		return
	}

	ret <- nil
}

//

// PRIVATE

//

// initAI initializes all AI providers (besides disabled and no-key ones).
func (a *AgentBase) initAI() error {
	// TODO expose as states

	// open ai
	for i := range a.cfg.AI.OpenAI {
		item := &a.cfg.AI.OpenAI[i]
		if item.Disabled || item.Key == "" {
			continue
		}

		if item.Model == "" {
			item.Model = shared.ConfigDefaultAIOpenAI().Model
		}

		config := openai.DefaultConfig(item.Key)
		if item.URL != "" {
			a.LogDebug("using OpenAI",
				"base", item.URL,
				"model", item.Model,
			)
			config.BaseURL = item.URL
		}
		opts := []instr.Options{instr.WithMaxRetries(item.Retries)}
		if !item.NoEnforceSchema {
			opts = append(opts, instr.WithMode(instr.ModeJSONSchema))
		}
		a.aiClients = append(a.aiClients, &shared.AIClient{
			OpenAI: &shared.OpenAIClient{
				Cfg: item,
				C:   instr.FromOpenAI(openai.NewClientWithConfig(config), opts...),
			},
		})
	}

	// gemini
	for i := range a.cfg.AI.Gemini {
		item := &a.cfg.AI.Gemini[i]
		if item.Disabled || item.Key == "" {
			continue
		}

		a.LogDebug("using Gemini", "base", item.Model)
		client, err := genai.NewClient(a.mach.Context(), &genai.ClientConfig{
			// TODO enforce schema?
			APIKey: item.Key,
		})
		if err != nil {
			return err
		}
		a.aiClients = append(a.aiClients, &shared.AIClient{
			Gemini: &shared.GeminiClient{
				Cfg: item,
				C: instr.FromGoogle(client,
					instr.WithMode(instr.ModeJSONSchema),
					// TODO config
					instr.WithMaxRetries(item.Retries),
				),
			},
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
		baseDefault := shared.ConfigDefaultAIOpenAI()
		if err := mergo.Merge(&baseDefault, item, mergo.WithOverride); err != nil {
			return err
		}
		a.cfg.AI.OpenAI[i] = baseDefault
	}
	for i, item := range a.cfg.AI.Gemini {
		baseDefault := shared.ConfigDefaultAIGemini()
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

	// TODO util
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}

	dbgAddr, _, _, err := cfg.DbgAddrs()
	if err != nil {
		return err
	}

	// default params
	var p typesdbg.Params
	parser, err := arg.NewParser(arg.Config{
		Exit: func(i int) {
			a.Log("dbg", "exit", i)
		},
		Out: shared.NewSlogWriter(a.Logger(), slog.LevelInfo),
	}, &p)
	if err != nil {
		return err
	}
	parser.Parse(nil)

	// custom params
	p.Id = a.cfg.Agent.ID + "-am-dbg"
	p.OutputDir = a.cfg.Agent.Dir
	p.ListenAddr = dbgAddr
	p.UiSsh = true
	p.Version = version
	p.OutputDiagrams = typesdbg.ParamsOutputDiagramsOne
	p.ViewExpandLinks = false
	// TODO dbgconfig option, injeted cfg options from the bot
	// DebugAddr: "localhost:9913",
	p.Print = func(txt string, args ...any) {
		a.Log("dbg", "print", fmt.Sprintf(txt, args...))
	}

	// init the debugger
	dbg, err := debugger.New(a.ctx, p)
	if err != nil {
		return err
	}
	a.dbg = dbg
	// TODO ErrDebug wait for a.mach
	// err = ampipe.BindErr(dbg.Mach, a.mach, "")
	// if err != nil {
	// 	return err
	// }

	// init rpc server
	dbg.ServerMux, dbg.ServerHttp, err = server.New(dbg.Mach, dbgAddr, p)
	if err != nil {
		return err
	}
	// start and wait till the end
	go dbg.Start()
	// TODO fwd err

	// TODO timeout
	<-a.dbg.Mach.When1(ssdbg.Ready, nil)

	return nil
}

// ///// ///// /////

// ///// TOOL

// ///// ///// /////

type Tool struct {
	mach *am.Machine
	Doc  *shared.Document
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
		Doc: shared.NewDocument(title),
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
			AddrDir: filepath.Join(cfg.Agent.Dir, "repl"),
		}
		if err := arpc.MachRepl(mach, "", &opts); err != nil {
			return nil, err
		}
	}

	// pipe Ready from the tool to agent
	_, err = ampipe.BindReady(mach, agent.Mach(), "", "")
	if err != nil {
		return nil, err
	}

	// pipe Start from the agent to tool
	_, err = ampipe.BindStart(agent.Mach(), mach, "", "")
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

func ToolAddToPrompts(t shared.ToolApi, prompts ...shared.PromptApi) {
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
	w.Logger.Log(context.Background(), w.Level, msg, "source", "stdlib/log")

	return len(p), nil
}

// ERRORS
