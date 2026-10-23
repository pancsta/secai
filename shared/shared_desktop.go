//go:build !wasm

package shared

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
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/pancsta/instructor-go/pkg/instructor"
	"github.com/pancsta/instructor-go/pkg/instructor/core"
	instrg "github.com/pancsta/instructor-go/pkg/instructor/providers/google"
	instroai "github.com/pancsta/instructor-go/pkg/instructor/providers/openai"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	"github.com/pancsta/asyncmachine-go/tools/debugger"
	"github.com/pancsta/asyncmachine-go/tools/generator"
	"github.com/sashabaranov/go-openai"
	oaischema "github.com/sashabaranov/go-openai/jsonschema"
	"google.golang.org/genai"

	"github.com/pancsta/secai/db/sqlc"
)

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

type ARequestingTool struct {
	Args
	Name   string `log:"name"`
	Source string `log:"source"`
	Target string `log:"target"`
}

func (ARequestingTool) State() string {
	return ss.RequestingTool
}

// -----

type ARequestingAI struct {
	Args
	Length string `log:"length"`
	Source string `log:"source"`
}

func (ARequestingAI) State() string {
	return ss.RequestingAI
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
	LogTrace(msg string, args ...any)
	LogDebug(msg string, args ...any)
	LogWarn(msg string, err error, args ...any)
	LogErr(msg string, err error, args ...any)

	LogCtx(ctx context.Context, txt string, args ...any)
	LogCtxDebug(ctx context.Context, txt string, args ...any)
	LogCtxTrace(ctx context.Context, txt string, args ...any)
	LogCtxWarn(ctx context.Context, txt string, err error, args ...any)
	LogCtxErr(ctx context.Context, msg string, err error, args ...any)

	LogEv(e *am.Event, txt string, args ...any)
	LogEvDebug(e *am.Event, txt string, args ...any)
	LogEvTrace(e *am.Event, txt string, args ...any)
	LogEvWarn(e *am.Event, txt string, err error, args ...any)
	LogEvErr(e *am.Event, msg string, err error, args ...any)

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

func (c *AIClient) Model() string {
	if c.OpenAI != nil {
		return c.OpenAI.Cfg.Model
	}
	return c.Gemini.Cfg.Model
}

func (c *AIClient) Provider() string {
	cfg := c.Cfg()
	if cfg.Name != "" {
		return cfg.Name
	}
	if cfg.URL != "" {
		return cfg.URL
	}

	return cfg.API
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
	err := telemetry.MachBindLokiEnv(mach)
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

// ///// ///// /////

// ///// PROMPT

// ///// ///// /////

type Prompt[P any, R any] struct {
	Conditions string
	Steps      string
	Result     string
	State      string
	A          AgentBaseAPI
	// number of previous messages to include (excluding system msg)
	HistoryMsgLen int
	// Msg holds msgs for this prompt and is used to build the Conversation instance (on-demand)
	Msgs      []*PromptMsg
	Thread    string
	Reasoning string
	LastModel string
	Godoc map[string]string

	tools      map[string]ToolApi
	docs       map[string]*Document
	lastClient *AIClient
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
		Godoc: make(map[string]string),

		tools: make(map[string]ToolApi),
		docs:  make(map[string]*Document),
	}
}

// SetGodoc sets the godoc comment map for the prompt schema.
func (p *Prompt[P, R]) SetGodoc(godoc map[string]string) *Prompt[P, R] {
	p.Godoc = godoc
	return p
}

// AddGodoc adds entries to the godoc comment map for the prompt schema.
func (p *Prompt[P, R]) AddGodoc(godoc map[string]string) *Prompt[P, R] {
	if p.Godoc == nil {
		p.Godoc = make(map[string]string, len(godoc))
	}
	for k, v := range godoc {
		p.Godoc[k] = v
	}
	return p
}

// Concurrency returns the number of concurrent requests possible for this prompt, based on configured providers.
func (p *Prompt[P, R]) Concurrency() int {
	return 1
	// TODO read Concurrency cfg of providers with == priority
	//  expose a semaphore
	//  mix with tags somehow?
}

// ExecMulti executes [Prompt.Exec] multiple times and returns all the results. Useful for scoring content.
func (p *Prompt[P, R]) ExecMulti(
	e *am.Event, times int, params P, cachedParams []*P, opts ...PromptOpts,
) ([]*R, []*PromptUsage, error) {
	//

	// TODO parallel
	result := make([]*R, 0, times)
	usages := make([]*PromptUsage, 0, times)
	for i := 0; i < times; i++ {
		r, u, err := p.Exec(e, params, cachedParams, opts...)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, r)
		usages = append(usages, u)
	}

	return result, usages, nil
}

func (p *Prompt[P, R]) QueryProviders(opts ...PromptOpts) []*AIClient {
	// TODO parse opts like in Exec, and return an ordered list of matches
	return nil
}

// Exec sends the AI prompt to the AI provider and returns unmarshalled output. [cachedParams] are useful for caching
// common data between similar prompts. Unlike historical messages, cache prompts never triggered an AI response.
func (p *Prompt[P, R]) Exec(e *am.Event, params P, cachedParams []*P, opts ...PromptOpts) (res *R, usage *PromptUsage, err error) {
	// TODO move cache to a dedicated table with hash-based IDs
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
			return nil, nil, fmt.Errorf("prompt state not assigned")
		}
		if mach.Not1(p.State) {
			return nil, nil, fmt.Errorf("prompt state not active")
		}
		ctx = mach.NewStateCtx(p.State, e)
	} else {
		ctx = context.Background() // TODO per-prompt ctx
	}

	// prep the machine
	cfg := p.A.ConfigBase()
	outDir := cfg.Agent.Dir
	err = os.MkdirAll(filepath.Join(outDir, "prompts"), 0755)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// metrics
	mach.EvAdd1(e, ss.RequestingAI, am.Pass(ARequestingAI{
		Source: p.State,
	}))
	defer mach.EvAdd1(e, ss.RequestedAI, nil)

	// loop over AI provider
	var c *AIClient
	c, err = p.pickClient(o)
	for c != nil && err == nil && ctx.Err() == nil {
		res, usage, err = p.tryProvider(ctx, c, params, cachedParams, o)
		if err != nil {
			// next
			c, err = p.pickClient(o)
			continue
		}

		// mark the call
		c.Cfg().Calls++
		// memorize for cache
		p.lastClient = c

		// ok
		return res, usage, nil
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	// last err
	return nil, nil, err
}

func (p *Prompt[P, R]) pickClient(o PromptOpts) (*AIClient, error) {
	// TODO check if disabled
	if p.lastClient != nil {
		return p.lastClient, nil
	}

	clients := p.A.AIs()
	clients = slices.DeleteFunc(clients, func(c *AIClient) bool {
		if !c.Cfg().IsEnabled() {
			return true
		}

		// required tags
		for _, t := range o.RequiredTags {
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

func (p *Prompt[P, R]) tryProvider(ctx context.Context, c *AIClient, params P, cachedParams []*P, opts PromptOpts) (*R, *PromptUsage, error) {
	cfg := p.A.ConfigBase()
	outDir := cfg.Agent.Dir

	// prompt, err := json.Marshal(req)
	prompt, err := json.Marshal(params)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal params: %w", err)
	}
	promptStr := string(prompt)
	paramsJ, _ := json.Marshal(params)
	paramsStr := string(paramsJ)
	conv, sys := p.Conversation()
	var cachedParamsStr string
	var cachedParamsJ []byte
	if len(cachedParams) > 0 {
		cachedParamsJ, _ = json.Marshal(cachedParams)
		cachedParamsStr = string(cachedParamsJ)
		for _, cp := range cachedParams {
			j, _ := json.Marshal(cp)
			str := string(j)
			conv.AddUserMessage(str)
		}
	}
	conv.AddUserMessage(promptStr)
	// skip sys prompt and new msg
	historyLen := int64(len(conv.GetMessages()) - 2)

	// log
	logArgs := []any{
		"ai", true,
		"model", c.Model(),
		"provider", c.Provider(),
		"prompt_len", Fmt.Sprintf("%v", len(prompt)),
	}
	if historyLen > 0 {
		logArgs = append(logArgs, "history_len", historyLen)
	}
	if cfg.Agent.Log.Prompts {
		logArgs = append(logArgs,
			"sys_prompt", TextCut(sys, 300),
			"prompt", TextCut(paramsStr, 300),
		)
	}
	p.A.LogCtxDebug(ctx, "ai_req", logArgs...)

	// dir log
	if outDir != "" {
		// save sys msg to output dir under "statename.sys.md"
		filename := filepath.Join(outDir, "prompts", p.State+".sys.md")
		if err := os.WriteFile(filename, []byte(sys), 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt to output dir under "statename.prompt.json"
		filename = filepath.Join(outDir, "prompts", p.State+".prompt.json")
		if err := os.WriteFile(filename, prompt, 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt cache to output dir under "statename.cache.json"
		if len(cachedParams) > 0 {
			filename = filepath.Join(outDir, "prompts", p.State+".cache.json")
			if err := os.WriteFile(filename, cachedParamsJ, 0644); err != nil {
				return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
			}
		}
	}

	// DB pre-save
	id := p.saveDB(ctx, c, historyLen, sys, paramsStr, cachedParamsStr)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err() // expired
	}

	start := time.Now()

	// call AI and handle the result
	var result *R
	var usage *PromptUsage
	// 40x retries
	for range c.Cfg().Retries {
		result, usage, err = p.execProvider(ctx, c, conv, opts)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err() // expired
		}
		if err != nil {
			// retry on traffic spikes err
			var errApi genai.APIError
			if (errors.As(err, &errApi) && errApi.Code == 503) ||
				strings.Contains(err.Error(), "TLS handshake timeout") {

				// TODO config
				if !amhelp.Wait(ctx, time.Second) {
					return nil, nil, ctx.Err() // expired
				}
				continue
			}

			// disable this client, unless input not ok TODO catch better 4xx status code
			if !strings.Contains(err.Error(), "prompt is too long") {
				// collect provider extra data
				data := []any{"ai", true, "provider", c.Provider(), "model", c.Cfg().Model}
				if c.OpenAI != nil {
					data = append(data, "url", c.OpenAI.Cfg.URL)
				}
				p.A.LogCtxErr(ctx, "ai_req", err, data...)

				timeout := c.Cfg().FallbackTimeout
				c.Cfg().DisabledUntil = time.Now().Add(timeout)
				// TODO name +cfg
				// TODO disable inside via ErrAI state, block until processed
				p.A.LogCtxWarn(ctx, "Disabling AI", err,
					"ai", true,
					"provider", c.Provider(),
					"timeout", timeout.String())
			}

			return nil, nil, fmt.Errorf("ai_%s_%s: %w", c.Cfg().API, c.Cfg().Model, err)
		}

		// ok
		break
	}

	// result
	usage.Compute()
	usage.HistoryChars = conv.LengthChars()
	usage.PromptChars = len(promptStr)
	resultJ, _ := json.MarshalIndent(result, "", "	")
	resultStr := string(resultJ)

	if cfg.Agent.Log.Prompts {
		logArgs = append(logArgs, "result", TextCut(resultStr, 300))
	}
	p.A.LogCtxDebug(ctx, "ai_resp", logArgs...)
	p.A.LogCtxTrace(ctx, "ai_resp", "usage", usage)

	// persist in DB
	timeTaken := time.Since(start).Round(time.Second).String()
	p.updateDB(ctx, id, resultStr, usage, timeTaken)
	if ctx.Err() != nil {
		return result, usage, ctx.Err() // expired
	}

	// persist in mem and fs
	if p.HistoryMsgLen > 0 {
		p.Msgs = append(p.Msgs, &PromptMsg{
			From:    core.RoleUser,
			Content: promptStr,
		}, &PromptMsg{
			From:    core.RoleAssistant,
			Content: resultStr,
		})
	}
	if outDir != "" {
		filename := filepath.Join(outDir, "prompts", p.State+".result.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	return result, usage, nil
}

func (p *Prompt[P, R]) execProvider(
	ctx context.Context, c *AIClient, conv *core.Conversation, opts PromptOpts,
) (*R, *PromptUsage, error) {
	//

	// default return
	err := fmt.Errorf("%w: no provider found", ErrAI)
	if c == nil || (c.OpenAI == nil && c.Gemini == nil) {
		return nil, nil, err
	}

	if len(p.Godoc) > 0 {
		// TODO ctx is not DI, pass via clients
		ctx = core.ContextWithGodoc(ctx, p.Godoc)
	}

	var result R
	var usage = &PromptUsage{
		Provider: c.Provider(),
		Model:    c.Cfg().Model,
		API:      c.Cfg().API,
		URL:      c.Cfg().URL,
	}

	switch {

	case c.OpenAI != nil:

		// set up
		req := openai.ChatCompletionRequest{
			Model:    c.Cfg().Model,
			Messages: instroai.ConversationToMessages(conv),
			// TODO MaxCompletionTokens
			// TODO? Stream
			// TODO? ResponseFormat: jsonschema.GenerateSchemaForType
		}
		// TODO enum
		if opts.Reasoning == "high" || p.Reasoning == "xhigh" {
			req.ReasoningEffort = p.Reasoning
		}

		// call
		resp, err := c.OpenAI.C.CreateChatCompletion(ctx, req, &result)
		cachedTokens := 0
		if resp.Usage.PromptTokensDetails != nil {
			cachedTokens = resp.Usage.PromptTokensDetails.CachedTokens
		}
		if err == nil {
			p.LastModel = req.Model
			usage.PromptTokens = resp.Usage.PromptTokens
			usage.PromptTokensCached = cachedTokens
			usage.CompletionTokens = resp.Usage.CompletionTokens
		}

		return &result, usage, err

	case c.Gemini != nil:
		req := instructor.GoogleRequest{
			Model:    c.Gemini.Cfg.Model,
			Contents: instrg.ConversationToContents(conv),
			// TODO SafetySettings...ResponseSchema ?
		}
		// TODO usage
		_, err = c.Gemini.C.CreateChatCompletion(ctx, req, &result)
		if err == nil {
			p.LastModel = req.Model
			usage = &PromptUsage{
				Model: req.Model,
			}
		}

		return &result, usage, err
	}

	return nil, nil, err
}


func (p *Prompt[P, R]) saveDB(
	ctx context.Context, c *AIClient, historyLen int64, sys, prompt, cache string,
) int64 {
	// sys prompt only for the 1st user prompt
	if historyLen > 1 {
		sys = ""
	}
	mach := p.A.Mach()
	q := p.A.QueriesBase()

	var thread sql.NullString
	if p.Thread != "" {
		thread = sql.NullString{String: p.Thread, Valid: true}
	}

	dbId, err := q.AddPrompt(ctx, sqlc.AddPromptParams{
		SessionID:   p.A.ConfigBase().SessionID,
		Agent:       mach.Id(),
		State:       p.State,
		System:      sys,
		HistoryLen:  historyLen,
		Request:     prompt,
		Cache:       sql.NullString{String: cache, Valid: cache != ""},
		CreatedAt:   time.Now(),
		MachTimeSum: int64(mach.Time(nil).Sum(nil)),
		MachTime:    fmt.Sprintf("%v", mach.Time(nil)),
		Thread:      thread,
	})
	if err != nil {
		p.A.LogErr("saveDB", err, "ai", true)
	}

	return dbId
}

func (p *Prompt[P, R]) updateDB(
	ctx context.Context, id int64, result string, usage *PromptUsage, timeTaken string,
) {
	if id == 0 {
		return
	}
	e := am.CtxToEv(ctx)
	usageJ, _ := json.MarshalIndent(usage, "", "  ")
	mach := p.A.Mach()
	args := &ABaseDBSaving{
		DBQuery: func(ctx context.Context) error {
			q := p.A.QueriesBase()
			return q.AddPromptResponse(ctx, sqlc.AddPromptResponseParams{
				Response:   sql.NullString{String: result, Valid: true},
				Usage:      sql.NullString{String: string(usageJ), Valid: true},
				HTimeTaken: sql.NullString{String: timeTaken, Valid: timeTaken != ""},
				ID:         id,
			})
		},
	}
	mach.EvAdd1(e, ss.BaseDBSaving, am.Pass(args))
}

//

// PROMPT_RAW - stock clients, no instructor-go, no generics, not maintained ATM

//

var _ PromptApi = &PromptRaw{}

// GenSysPrompt generates a system prompt.
func (p *PromptRaw) GenSysPrompt() string {

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
	return strings.Trim(Sp(`
		%s
		%s
		%s
		%s
		`, cond, steps, result, docs), "\n ")
}

// Conversation will create a conversation with history and system prompt, return sys prompt on the side.
func (p *PromptRaw) Conversation() (*core.Conversation, string) {
	sys := p.GenSysPrompt()
	// TODO marshal sys prompts, add generic params schema for attached documents
	c := core.NewConversation(sys)
	limit := max(100, p.HistoryMsgLen)
	if l := len(p.Msgs); l > limit {
		p.Msgs = p.Msgs[l-limit:]
	}
	for i := len(p.Msgs) - 1; i >= 0; i-- {
		msg := p.Msgs[i]
		c.AddMessage("TODO:msg.From", msg.Content)
	}

	return c, sys
}

// PromptRawRole is a simple role type, replacing core.Role from instructor-go.
type PromptRawRole string

const (
	PromptRawRoleSystem    PromptRawRole = "system"
	PromptRawRoleUser      PromptRawRole = "user"
	PromptRawRoleAssistant PromptRawRole = "assistant"
)

// PromptRawMsg is a message in a PromptRaw conversation.
type PromptRawMsg struct {
	Role    PromptRawRole
	Content string
}

// RawOpenAIClient wraps the stock OpenAI client.
type RawOpenAIClient struct {
	Cfg *ConfigAIOpenAI
	C   *openai.Client
}

// RawGeminiClient wraps the stock Google GenAI client.
type RawGeminiClient struct {
	Cfg *ConfigAIGemini
	C   *genai.Client
}

// RawAIClient is the picked AI client from the pool for PromptRaw (stock clients).
type RawAIClient struct {
	OpenAI *RawOpenAIClient
	Gemini *RawGeminiClient
}

func (c *RawAIClient) Cfg() *ConfigAICommon {
	if c.OpenAI != nil {
		return &c.OpenAI.Cfg.ConfigAICommon
	}
	return &c.Gemini.Cfg.ConfigAICommon
}

// PromptRaw is a prompt that uses stock clients (openai.Client, genai.Client)
// instead of instructor-go for structured output parsing. Params are a raw
// string (typically JSON), and ResultSchema defines the expected JSON Schema
// for structured output enforcement. The raw JSON response is returned
// via Exec / ExecInto.
type PromptRaw struct {
	Conditions string
	Steps      string
	Result     string
	// TODO use better schema pkg
	ResultSchema *oaischema.Definition
	State        string
	A            AgentBaseAPI
	// number of previous messages to include
	HistoryMsgLen int
	Msgs          []*PromptRawMsg

	tools map[string]ToolApi
	docs  map[string]*Document
}

func NewPromptRaw(agent AgentBaseAPI, state, condition, steps, result string, resultEmpty any) (*PromptRaw, error) {

	var schema *oaischema.Definition

	if resultEmpty != nil {
		var err error
		schema, err = oaischema.GenerateSchemaForType(resultEmpty)
		if err != nil {
			return nil, err
		}
	}

	if condition == "" {
		condition = "This is a conversation with a helpful and friendly AI assistant."
	}

	return &PromptRaw{
		Conditions:    Sp(condition),
		Steps:         Sp(steps),
		Result:        Sp(result),
		ResultSchema:  schema,
		HistoryMsgLen: 10,
		State:         state,
		A:             agent,

		tools: make(map[string]ToolApi),
		docs:  make(map[string]*Document),
	}, nil
}

// Concurrency returns the number of concurrent requests possible for this prompt.
func (p *PromptRaw) Concurrency() int {
	return 1
}

// ExecMulti calls Exec multiple times and collects results as JSON byte slices.
func (p *PromptRaw) ExecMulti(e *am.Event, times int, params string) ([][]byte, []*PromptUsage, error) {
	result := make([][]byte, 0, times)
	usages := make([]*PromptUsage, 0, times)
	for i := 0; i < times; i++ {
		r, u, err := p.Exec(e, params)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, r)
		usages = append(usages, u)
	}

	return result, usages, nil
}

// ExecInto calls Exec and unmarshals the raw JSON response into result.
func (p *PromptRaw) ExecInto(e *am.Event, params string, result any, opts ...PromptOpts) (*PromptUsage, error) {
	raw, u, err := p.Exec(e, params, opts...)
	if err != nil {
		return nil, err
	}
	return u, json.Unmarshal(raw, result)
}

// Exec sends params as a user message and returns the raw JSON response bytes.
func (p *PromptRaw) Exec(e *am.Event, params string, opts ...PromptOpts) (res []byte, usage *PromptUsage, err error) {
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
			return nil, nil, fmt.Errorf("prompt state not assigned")
		}
		if mach.Not1(p.State) {
			return nil, nil, fmt.Errorf("prompt state not active")
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
		return nil, nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// metrics
	mach.EvAdd1(e, ss.RequestingAI, nil)
	defer mach.EvAdd1(e, ss.RequestedAI, nil)

	// loop over AI provider
	var c *RawAIClient
	c, err = p.pickClient(o)
	for c != nil && err == nil && ctx.Err() == nil {
		res, usage, err = p.tryProvider(ctx, c, params)
		if err != nil {
			// next
			c, err = p.pickClient(o)
			continue
		}

		// mark the call and ret
		c.Cfg().Calls++
		return res, usage, nil
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	// last err
	return nil, nil, err
}

func (p *PromptRaw) pickClient(o PromptOpts) (*RawAIClient, error) {
	clients := p.A.AIs()
	clients = slices.DeleteFunc(clients, func(c *AIClient) bool {
		if !c.Cfg().IsEnabled() {
			return true
		}

		// required tags
		for _, t := range o.RequiredTags {
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

	// Convert AIClient to RawAIClient (create stock clients on demand)
	c := clients[0]
	if c.OpenAI != nil {
		config := openai.DefaultConfig(c.OpenAI.Cfg.Key)
		if c.OpenAI.Cfg.URL != "" {
			config.BaseURL = c.OpenAI.Cfg.URL
		}
		return &RawAIClient{
			OpenAI: &RawOpenAIClient{
				Cfg: c.OpenAI.Cfg,
				C:   openai.NewClientWithConfig(config),
			},
		}, nil
	}
	// if c.Gemini != nil {
	// 	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
	// 		APIKey: c.Gemini.Cfg.Key,
	// 	})
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to create genai client: %w", err)
	// 	}
	// 	return &RawAIClient{
	// 		Gemini: &RawGeminiClient{
	// 			Cfg: c.Gemini.Cfg,
	// 			C:   client,
	// 		},
	// 	}, nil
	// }

	return nil, fmt.Errorf("%w: no valid provider in client", ErrAI)
}

func (p *PromptRaw) tryProvider(ctx context.Context, c *RawAIClient, params string) ([]byte, *PromptUsage, error) {
	cfg := p.A.ConfigBase()
	outDir := cfg.Agent.Dir
	e := am.CtxToEv(ctx)

	contentStr := params
	sys := p.genSysPrompt()
	// add user message to in-memory history
	p.Msgs = append(p.Msgs, &PromptRawMsg{
		Role:    PromptRawRoleUser,
		Content: contentStr,
	})

	// detailed log
	historyLen := int64(len(p.Msgs) - 1)
	if cfg.Agent.Log.Prompts {
		p.A.LogCtxDebug(ctx, "ai_req",
			"sys_prompt", sys,
			"historyLen", historyLen)
	}
	// brief log
	p.A.LogCtxDebug(ctx, p.State, "prompt", params)
	if outDir != "" {
		// save sys msg to output dir under "statename.sys.md"
		filename := filepath.Join(outDir, "prompts", p.State+".sys.md")
		if err := os.WriteFile(filename, []byte(sys), 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}

		// save the prompt to output dir under "statename.prompt.json"
		filename = filepath.Join(outDir, "prompts", p.State+".prompt.json")
		if err := os.WriteFile(filename, []byte(contentStr), 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	// try call the LLM and handle the result
	result, usage, err := p.execProvider(ctx, c, sys, p.ResultSchema)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err() // expired
	}
	if err != nil {
		// disable this client

		// collect provider extra data
		data := []any{"api", c.Cfg().API, "model", c.Cfg().Model}
		if c.Cfg().URL != "" {
			data = append(data, "url", c.Cfg().URL)
		}
		p.A.LogErr("ai_req", err, data...)

		timeout := c.Cfg().FallbackTimeout
		c.Cfg().DisabledUntil = time.Now().Add(timeout)
		p.A.LogWarn("Disabling AI", err,
			"provider", c.Cfg().API,
			"timeout", timeout.String())

		return nil, nil, fmt.Errorf("ai_%s_%s: %w", c.Cfg().API, c.Cfg().Model, err)
	}

	p.A.LogCtxDebug(ctx, p.State, "result", string(result))

	// persist in SQL
	resultJ, err := json.MarshalIndent(json.RawMessage(result), "", "\t")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	p.saveDB(e, sys, historyLen, contentStr, c, resultJ)
	if ctx.Err() != nil {
		return result, usage, ctx.Err() // expired
	}

	// persist in mem and fs
	if p.HistoryMsgLen > 0 {
		p.Msgs = append(p.Msgs, &PromptRawMsg{
			Role:    PromptRawRoleAssistant,
			Content: string(resultJ),
		})
	}
	if outDir != "" {
		filename := filepath.Join(outDir, "prompts", p.State+".result.json")
		if err := os.WriteFile(filename, resultJ, 0644); err != nil {
			return nil, nil, fmt.Errorf("failed to write prompt file: %w", err)
		}
	}

	return result, usage, nil
}

func (p *PromptRaw) execProvider(
	ctx context.Context, c *RawAIClient, sys string, schema *oaischema.Definition,
) ([]byte, *PromptUsage, error) {
	//

	err := fmt.Errorf("%w: no provider found", ErrAI)
	if c == nil || (c.OpenAI == nil && c.Gemini == nil) {
		return nil, nil, err
	}

	switch {

	case c.OpenAI != nil:
		// Build messages: system + history + latest user message
		msgs := make([]openai.ChatCompletionMessage, 0, len(p.Msgs)+1)
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    string(PromptRawRoleSystem),
			Content: sys,
		})
		for _, m := range p.Msgs {
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}

		req := openai.ChatCompletionRequest{
			Model:    c.OpenAI.Cfg.Model,
			Messages: msgs,
		}

		// Inject JSON Schema if provided
		if schema != nil {
			schemaBytes, err := json.Marshal(schema)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal schema: %w", err)
			}

			if strings.Contains(c.OpenAI.Cfg.Model, "deepseek") {
				req.ResponseFormat = &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONObject,
				}
				req.Messages[0].Content = "Always respond in the following JSON Schema\n\n" + string(schemaBytes) + "\n\n"
				// TODO validate schema for deepseek
			} else {
				req.ResponseFormat = &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
					JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
						Name:   p.State + "_result",
						Schema: json.RawMessage(schemaBytes),
					},
				}
			}
		}

		resp, err := c.OpenAI.C.CreateChatCompletion(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		if len(resp.Choices) == 0 {
			return nil, nil, fmt.Errorf("no choices in response")
		}

		var usage *PromptUsage
		if resp.Usage.TotalTokens > 0 {
			usage = &PromptUsage{
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
			}
		}

		return []byte(resp.Choices[0].Message.Content), usage, nil

		// case c.Gemini != nil:
		// 	contents := make([]*genai.Content, 0, len(p.Msgs))
		// 	for _, m := range p.Msgs {
		// 		role := string(m.Role)
		// 		if role == string(PromptRawRoleAssistant) {
		// 			role = "model"
		// 		}
		// 		contents = append(contents, genai.NewContentFromText(m.Content, genai.Role(role)))
		// 	}
		//
		// 	config := &genai.GenerateContentConfig{
		// 		SystemInstruction: genai.NewContentFromText(sys, genai.Role(string(PromptRawRoleSystem))),
		// 	}
		// 	config.ResponseMIMEType = "application/json"
		// 	// TODO: convert jsonschema.Definition to genai.Schema for ResponseSchema
		//
		// 	resp, err := c.Gemini.C.Models.GenerateContent(ctx, c.Gemini.Cfg.Model, contents, config)
		// 	if err != nil {
		// 		return nil, err
		// 	}
		// 	text := resp.Text()
		// 	if text == "" {
		// 		return nil, fmt.Errorf("empty response from Gemini")
		// 	}
		// 	return []byte(text), nil
	}

	return nil, nil, err
}

func (p *PromptRaw) saveDB(
	e *am.Event, sys string, historyLen int64, contentStr string, c *RawAIClient, resultJ []byte,
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

// genSysPrompt generates a system prompt.
func (p *PromptRaw) genSysPrompt() string {

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
	return strings.Trim(Sp(`
		%s
		%s
		%s
		%s
		`, cond, steps, result, docs), "\n ")
}

// AddTool registers a SECAI TOOL which then exports its documents into the system prompt.
func (p *PromptRaw) AddTool(tool ToolApi) {
	p.tools[tool.Mach().Id()] = tool
}

// AddDoc adds a document into the system prompt.
func (p *PromptRaw) AddDoc(doc *Document) {
	p.docs[doc.Title()] = doc
}

// HistClean clears the message history.
func (p *PromptRaw) HistClean() {
	p.Msgs = nil
}

// ///// ///// /////

// ///// MCP

// ///// ///// /////

// Adapter for MCP-via-JSON, allows for tool calling via Go types.
// TODO add listing MCP servers, metadata

// MCPCallToolParams represents the inner parameters of a tools/call request
type MCPCallToolParams struct {
	Name      string          `json:"name" jsonschema:"description=The name of the target MCP tool"`
	Arguments json.RawMessage `json:"arguments" jsonschema:"description=JSON object of arguments matching the tool schema"`
}

// MCPCallToolRequest enforces the exact JSON-RPC 2.0 MCP request structure
type MCPCallToolRequest struct {
	JSONRPC string            `json:"jsonrpc" jsonschema:"const=2.0"`
	ID      int               `json:"id" jsonschema:"description=Request identifier"`
	Method  string            `json:"method" jsonschema:"const=tools/call"`
	Params  MCPCallToolParams `json:"params"`
}
