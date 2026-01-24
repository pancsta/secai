//go:build !wasm

package shared

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/567-labs/instructor-go/pkg/instructor"
	"github.com/charmbracelet/ssh"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	"github.com/pancsta/asyncmachine-go/tools/debugger"
	"github.com/pancsta/asyncmachine-go/tools/generator"
	"github.com/pancsta/secai/db/sqlc"
)

// A is a struct for node arguments. It's a typesafe alternative to [am.A].
type A struct {
	// ID is a general string ID param.
	ID string `log:"id"`
	// Addr is a network address.
	Addr string `log:"addr"`
	// Timeout is a generic timeout.
	Timeout time.Duration `log:"timeout"`
	// Prompt is a prompt to be sent to LLM.
	Prompt string `log:"prompt"`
	// IntByTimeout means the interruption was caused by timeout.
	IntByTimeout bool `log:"int_by_timeout"`
	// Msg is a single message with an author and text.
	Msg *Msg `log:"msg"`
	// Perform additional checks via LLM
	CheckLLM bool `log:"check_llm"`
	// List of choices
	Choices []string
	// Actions are a list of buttons to be displayed in the UI.
	Actions    []ActionInfo `log:"actions"`
	Stories    []StoryInfo  `log:"stories"`
	StatesList []string     `log:"states_list"`
	// ActivateList is a list of booleans for StatesList, indicating an active state at the given index.
	ActivateList []bool `log:"activate_list"`
	ConfigAI     *ConfigAI
	ClockDiff    [][]int

	// non-RPC fields

	// Result is a buffered channel to be closed by the receiver
	ResultCh chan<- am.Result
	// DBQuery is a function that executes a query on the database.
	DBQuery func(ctx context.Context) error
	// RetStr returns dereferenced user prompts based on the list of offer.
	RetOfferRef chan<- *OfferRef
	SSHServer   *ssh.Server
	SSHSess     ssh.Session
	// Done is a buffered channel to be closed by the receiver
	Done chan<- struct{}
}

// ///// ///// /////

// ///// AGENT

// ///// ///// /////

// AgentInit is an init func for embeddable agent structs (non-top level ones).
type AgentInit interface {
	Init(
		agentImpl AgentAPI, cfg *Config, logArgs am.LogArgsMapperFn, groups any, states am.States, args any,
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

	ConfigBase() *Config
	OpenAI() []*OpenAIClient
	Gemini() []*GeminiClient

	Start() am.Result
	Stop(disposeCtx context.Context) am.Result
	Log(msg string, args ...any)
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

type AgentStore struct {
	M         map[string]any
	ClockDiff [][]int
	Web       fs.FS
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
		SELECT sql 
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
