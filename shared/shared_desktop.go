//go:build !wasm

package shared

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/jxnl/instructor-go/pkg/instructor"
	"github.com/jxnl/instructor-go/pkg/instructor/core"
	instrg "github.com/jxnl/instructor-go/pkg/instructor/providers/google"
	instroai "github.com/jxnl/instructor-go/pkg/instructor/providers/openai"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	"github.com/pancsta/asyncmachine-go/tools/debugger"
	"github.com/pancsta/asyncmachine-go/tools/generator"
	"github.com/sashabaranov/go-openai"

	"github.com/pancsta/secai/db/sqlc"
)

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
// type A struct {
// 	// DBQuery is a function that executes a query on the database.
// 	DBQuery func(ctx context.Context) error
// 	// RetStr returns dereferenced user prompts based on the list of offer.
// 	RetOfferRef chan<- *OfferRef
// 	SSHServer   *ssh.Server
// 	SSHSess     ssh.Session
// 	// Done is a buffered channel to be closed by the receiver
// 	Done chan<- struct{}
// }

// -----

type ABaseDBSaving struct {
	Args
	// DBQuery is a function that executes a query on the database.
	DBQuery func(ctx context.Context) error
}

func (ABaseDBSaving) ArgsState() string {
	return ss.BaseDBSaving
}

// -----

type ACheckingMenuRefs struct {
	Args
	// RetStr returns dereferenced user prompts based on the list of offer.
	RetOfferRef chan<- *OfferRef
	// Perform additional checks via LLM
	CheckLLM bool `log:"check_llm"`
	// Prompt is a prompt to be checked.
	Prompt string `log:"prompt"`
	// List of choices (optional)
	Choices []string
}

func (ACheckingMenuRefs) ArgsState() string {
	return ss.CheckingMenuRefs
}

// -----

type ASSHReady struct {
	Args
	SSHServer *ssh.Server
}

func (ASSHReady) ArgsState() string {
	return ss.SSHReady
}

// -----

type ASSHConn struct {
	Args
	SSHSess ssh.Session
	// Done is a buffered channel to be closed by the receiver
	Done chan<- struct{}
}

func (ASSHConn) ArgsState() string {
	return ss.SSHConn
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

// AgentInit is an init func for embeddable agent structs (non-top level ones).
type AgentInit interface {
	Init(
		agentImpl AgentAPI, cfg *Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args []am.ArgsApi,
	) error
}

// AgentBaseAPI is the base agent API implemented by the framework.
type AgentBaseAPI interface {
	// AgentImpl returns the top-level agent implementation.
	AgentImpl() AgentAPI
	// Output outputs text to the user.
	Output(txt string, from From) am.Result

	Mach() *am.Machine
	DBG() *debugger.Debugger
	Hist() (history.MemoryApi, error)

	SetConfigBase(*Config) error
	ConfigBase() *Config
	AIs() []*AIClient

	Start() am.Result
	Stop(disposeCtx context.Context)
	Log(msg string, args ...any)
	LogDebug(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogErr(msg string, err error, args ...any)
	Logger() *slog.Logger
	Store() *AgentStore

	QueriesBase() *sqlc.Queries

	DBBase() *sql.DB
	DBHistory() *sql.DB

	// abstract TODO AgentChildAPI

}

// AgentQueries is a generic SQL API.
type AgentQueries[TQueries any] interface {
	Queries() *TQueries
}

// AgentAPI is the top-level API to be implemented by the final agent.
type AgentAPI interface {
	AgentBaseAPI

	Msgs() []*Msg
	Splash() string
	MachSchema() (am.Schema, am.S)
	Actions() []ActionInfo
	Stories() []StoryInfo
	Story(state string) *Story
	DBAgent() *sql.DB
	MachMem() *am.Machine
	// Moves return a list of moves and their descriptions for orienting.
	OrientingMoves() map[string]string
	// HistoryStates returns a list of states to track in the history.
	HistoryStates() am.S
}

type OpenAIClient struct {
	Cfg *ConfigAIOpenAI
	C   *instructor.InstructorOpenAI
}

type GeminiClient struct {
	Cfg *ConfigAIGemini
	C   *instructor.InstructorGoogle
}

// AIClient is the picked AI client from the pool (from either provider).
type AIClient struct {
	OpenAI *OpenAIClient
	Gemini *GeminiClient
}

func (c *AIClient) Cfg() *ConfigAICommon {
	if c.OpenAI != nil {
		return &c.OpenAI.Cfg.ConfigAICommon
	}
	return &c.Gemini.Cfg.ConfigAICommon
}

// ///// ///// /////

// ///// UTILS

// ///// ///// /////

func MachTelemetry(mach *am.Machine, logArgs am.LogArgsMapperFn) {
	semLogger := mach.SemLogger()

	// default (non-debug) log level
	semLogger.SetLevel(am.LogChanges)
	// dedicated args mapper
	if logArgs != nil {
		semLogger.SetArgsMapper(logArgs)
	} else {
		// default args mapper
		semLogger.SetArgsMapper(am.NewLogArgsMapper(0, am.LogArgs))
	}

	// env-based telemetry

	// connect to an am-dbg instance
	amhelp.MachDebugEnv(mach)
	// export metrics to prometheus
	prometheus.MachMetricsEnv(mach)
	// loki logger
	err := telemetry.BindLokiEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}

	// grafana dashboard
	err = generator.MachDashboardEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}

	// open telemetry traces
	err = telemetry.MachBindOtelEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}
}

// TODO format
// "github.com/maxrichie5/go-sqlfmt/sqlfmt"
// config := sqlfmt.NewDefaultConfig()
// formatted := sqlfmt.Format(rawSQL, config)
func GetSQLiteSchema(db *sql.DB) (string, error) {
	// Query the master table for the 'sql' column
	// We filter out internal sqlite_ tables and empty entries
	query := `
		SELECT sql` + ` 
		FROM sqlite_schema 
		WHERE type IN ('table', 'index', 'trigger', 'view') 
		AND name NOT LIKE 'sqlite_%'
		AND sql IS NOT NULL
		ORDER BY name;
	`

	rows, err := db.Query(query)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var sqlStmt string
		if err := rows.Scan(&sqlStmt); err != nil {
			return "", err
		}
		// remove backticks
		sqlStmt = strings.ReplaceAll(sqlStmt, "`", "")
		sb.WriteString(sqlStmt)
		// Append a semicolon and newline for readability
		sb.WriteString(";\n")
	}

	return sb.String(), nil
}

// PROMPT

type Prompt[P any, R any] struct {
	Conditions   string
	Steps        string
	Result       string
	SchemaParams P
	SchemaResult R
	State        string
	A            AgentBaseAPI
	// number of previous messages to include
	HistoryMsgLen int
	Msgs          []*PromptMsg

	tools map[string]ToolApi
	docs  map[string]*Document
}

func NewPrompt[P any, R any](agent AgentBaseAPI, state, condition, steps, results string) *Prompt[P, R] {
	if condition == "" {
		condition = "This is a conversation with a helpful and friendly AI assistant."
	}

	return &Prompt[P, R]{
		Conditions:    Sp(condition),
		Steps:         Sp(steps),
		Result:        Sp(results),
		HistoryMsgLen: 10,
		State:         state,
		A:             agent,

		tools: make(map[string]ToolApi),
		docs:  make(map[string]*Document),
	}
}

// Concurrency returns the number of concurrent requests possible for this prompt, based on configured providers.
func (p *Prompt[P, R]) Concurrency() int {
	return 1
	// TODO read Concurrency cfg of providers with == priority
	//  expose a semaphore
	//  mix with tags somehow?
}

func (p *Prompt[P, R]) ExecMulti(e *am.Event, times int, params P) ([]*R, error) {
	result := make([]*R, 0, times)
	for i := 0; i < times; i++ {
		r, err := p.Exec(e, params)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}

	return result, nil
}

func (p *Prompt[P, R]) Exec(e *am.Event, params P, opts ...PromptOpts) (res *R, err error) {
	mach := p.A.Mach()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: provider panic: %v", ErrAI, r)
		}
	}()

	o := optPromptOpts(opts)

	var ctx context.Context
	if !o.SkipState {
		if p.State == "" {
			return nil, fmt.Errorf("prompt state not assigned")
		}
		if mach.Not1(p.State) {
			return nil, fmt.Errorf("prompt state not active")
		}
		ctx = mach.NewStateCtx(p.State, e)
	} else {
		ctx = context.Background()
	}

	// prep the machine
	cfg := p.A.ConfigBase()
	outDir := cfg.Agent.Dir
	err = os.MkdirAll(filepath.Join(outDir, "prompts"), 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// metrics
	mach.EvAdd1(e, ss.RequestingAI, nil)
	defer mach.EvAdd1(e, ss.RequestedAI, nil)

	// loop over AI provider
	var c *AIClient
	c, err = p.pickClient(o)
	for c != nil && err == nil && ctx.Err() == nil {
		res, err = p.tryProvider(ctx, c, params)
		if err != nil {
			// next
			c, err = p.pickClient(o)
			continue
		}

		// mark the call and ret
		c.Cfg().Calls++
		return res, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// last err
	return nil, err
}

func (p *Prompt[P, R]) pickClient(o PromptOpts) (*AIClient, error) {
	clients := p.A.AIs()
	clients = slices.DeleteFunc(clients, func(c *AIClient) bool {
		if !c.Cfg().IsEnabled() {
			return true
		}

		// required tags
		for _, t := range o.Tags {
			if !slices.Contains(c.Cfg().Tags, t) {
				return true
			}
		}

		// excluded tags
		for _, t := range o.NoTags {
			if slices.Contains(c.Cfg().Tags, t) {
				return true
			}
		}

		return false
	})
	// sort
	slices.SortFunc(clients, func(c1, c2 *AIClient) int {
		// round robin for equal priorities ASC
		if c2.Cfg().Priority == c1.Cfg().Priority {
			return c1.Cfg().Calls - c2.Cfg().Calls
		}

		// priorities DESC
		return c2.Cfg().Priority - c1.Cfg().Priority
	})

	if len(clients) == 0 {
		return nil, fmt.Errorf("%w: no providers for %s", ErrAI, p.State)
	}
	return clients[0], nil
}

func (p *Prompt[P, R]) tryProvider(ctx context.Context, c *AIClient, params P) (*R, error) {
	cfg := p.A.ConfigBase()
	outDir := cfg.Agent.Dir
	e := am.CtxToEv(ctx)

	// gen an LLM prompt
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
		p.A.LogDebug("llm_req",
			"state", p.State,
			"sys_prompt", sys,
			"historyLen", historyLen)
	}
	// brief log
	p.A.LogDebug(p.State, "prompt", params)
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

	// try call the LLM and handle the result
	result, err := p.execProvider(ctx, c, conv)
	if ctx.Err() != nil {
		return nil, ctx.Err() // expired
	}
	if err != nil {
		// disable this client

		// collect provider extra data
		data := []any{"provider", c.Cfg().Provider, "model", c.Cfg().Model}
		if c.OpenAI != nil {
			data = append(data, "url", c.OpenAI.Cfg.URL)
		}
		p.A.LogErr("ai_req", err, data...)

		timeout := c.Cfg().FallbackTimeout
		c.Cfg().DisabledUntil = time.Now().Add(timeout)
		// TODO name +cfg
		// TODO disable inside via ErrAI state, block until processed
		p.A.LogWarn("Disabling AI", "provider", c.Cfg().Provider, "timeout", timeout.String())

		return nil, fmt.Errorf("ai_%s_%s: %w", c.Cfg().Provider, c.Cfg().Model, err)
	}

	p.A.LogDebug(p.State, "result", result)

	// persist in SQL
	resultJ, err := json.MarshalIndent(result, "", "	")
	if err != nil {
		// TODO disable and continue
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	p.saveDB(e, sys, historyLen, contentStr, c, resultJ)
	if ctx.Err() != nil {
		return result, ctx.Err() // expired
	}

	// persist in mem and fs
	if p.HistoryMsgLen > 0 {
		p.Msgs = append(p.Msgs, &PromptMsg{
			From:    core.RoleUser,
			Content: contentStr,
		}, &PromptMsg{
			From:    core.RoleAssistant,
			Content: string(resultJ),
		})
	}
	if outDir != "" {
		filename := filepath.Join(outDir, "prompts", p.State+".result.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// confirm config OK TODO handle better
	p.A.Mach().EvAdd1(e, ss.ConfigValid, nil)

	return result, nil
}

func (p *Prompt[P, R]) execProvider(
	ctx context.Context, c *AIClient, conv *core.Conversation,
) (*R, error) {
	//

	err := fmt.Errorf("%w: no provider found", ErrAI)
	if c == nil || (c.OpenAI == nil && c.Gemini == nil) {
		return nil, err
	}

	var result R
	switch {

	case c.OpenAI != nil:
		req := openai.ChatCompletionRequest{
			Model:    c.OpenAI.Cfg.Model,
			Messages: instroai.ConversationToMessages(conv),
		}
		// TODO collect usage tokens, save in DB
		_, err = c.OpenAI.C.CreateChatCompletion(ctx, req, &result)
		return &result, err

	case c.Gemini != nil:
		req := instructor.GoogleRequest{
			Model:    c.Gemini.Cfg.Model,
			Contents: instrg.ConversationToContents(conv),
		}
		_, err = c.Gemini.C.CreateChatCompletion(ctx, req, &result)
		return &result, err
	}

	return nil, err
}

func (p *Prompt[P, R]) saveDB(
	e *am.Event, sys string, historyLen int64, contentStr string, c *AIClient, resultJ []byte,
) {
	//

	mach := p.A.Mach()
	// TODO unique
	sessID := mach.Id() + "-" + p.State

	args := &ABaseDBSaving{
		DBQuery: func(ctx context.Context) error {
			q := p.A.QueriesBase()

			dbId, err := q.AddPrompt(ctx, sqlc.AddPromptParams{
				SessionID:   sessID,
				Agent:       mach.Id(),
				State:       p.State,
				System:      sys,
				HistoryLen:  historyLen,
				Request:     contentStr,
				Provider:    c.Cfg().Provider,
				Model:       c.Cfg().Model,
				CreatedAt:   time.Now(),
				MachTimeSum: int64(mach.Time(nil).Sum(nil)),
				MachTime:    fmt.Sprintf("%v", mach.Time(nil)),
			})
			if err != nil {
				return err
			}

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
	mach.EvAdd1(e, ss.BaseDBSaving, am.Pass(args))
}
