package shared

import (
	"context"
	"log/slog"

	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
)

type Prompt[P any, R any] struct {
	Conditions   string
	Steps        string
	Result       string
	SchemaParams P
	SchemaResult R
	State        string
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

		tools: make(map[string]ToolApi),
		docs:  make(map[string]*Document),
	}
}

// AgentBaseAPI is the base agent API implemented by the framework.
type AgentBaseAPI interface {
	// AgentImpl returns the top-level agent implementation.
	// AgentImpl() AgentAPI
	// Output outputs text to the user.
	Output(txt string, from From) am.Result

	Mach() *am.Machine
	// DBG() *debugger.Debugger
	Hist() (history.MemoryApi, error)

	SetConfigBase(*Config) error
	ConfigBase() *Config
	// AIs() []*AIClient

	Start() am.Result
	Stop(disposeCtx context.Context) am.Result
	Log(msg string, args ...any)
	LogErr(msg string, err error, args ...any)
	Logger() *slog.Logger
	Store() *AgentStore

	// QueriesBase() *sqlc.Queries
	//
	// DBBase() *sql.DB
	// DBHistory() *sql.DB

	// abstract TODO AgentChildAPI

}
