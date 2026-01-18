package shared

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BooleanCat/go-functional/v2/it"
	"github.com/gliderlabs/ssh"
	"github.com/lithammer/dedent"
	"github.com/orsinium-labs/enum"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	amtele "github.com/pancsta/asyncmachine-go/pkg/telemetry"
	amprom "github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	amgen "github.com/pancsta/asyncmachine-go/tools/generator"
	"github.com/sashabaranov/go-openai"
)

const (
	// EnvConfig config location
	EnvConfig = "SECAI_CONFIG"
	// EnvAgentDir secai monorepo dir
	EnvAgentDir = "SECAI_AGENT_DIR"
	EnvNoDotEnv = "SECAI_NO_DOTENV"
)

// From enum

type From enum.Member[string]

var (
	FromAssistant = From{"assistant"}
	FromSystem    = From{"system"}
	FromUser      = From{"user"}
	FromNarrator  = From{"narrator"}

	FromEnum = enum.New(FromAssistant, FromSystem, FromUser)
	// TODO FromNarrator
)

type OfferRef struct {
	// Index from 0
	Index int
	Text  string
}

type Msg struct {
	From      From
	Text      string
	CreatedAt time.Time
}

func NewMsg(text string, from From) *Msg {
	return &Msg{
		From:      from,
		Text:      text,
		CreatedAt: time.Now(),
	}
}

func (m *Msg) String() string {
	return m.Text
}

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

const APrefix = "secai"

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
	Actions    []StoryAction `log:"actions"`
	Stories    []StoryInfo   `log:"stories"`
	StatesList []string      `log:"states_list"`
	// ActivateList is a list of booleans for StatesList, indicating an active state at the given index.
	ActivateList []bool `log:"activate_list"`

	// non-RPC fields

	// Result is a buffered channel to be closed by the receiver
	Result chan<- am.Result
	// DBQuery is a function that executes a query on the database.
	DBQuery func(ctx context.Context) error
	// RetStr returns dereferenced user prompts based on the list of offer.
	RetOfferRef chan<- *OfferRef
	SSHServer   *ssh.Server
	SSHSess     ssh.Session
	// Done is a buffered channel to be closed by the receiver
	Done chan<- struct{}
}

// ARpc is a subset of [am.A] that can be passed over RPC.
type ARpc struct {
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
	// Buttons are a list of buttons to be displayed in the UI.
	Buttons      []StoryAction `log:"buttons"`
	Stories      []StoryInfo   `log:"stories"`
	StatesList   []string      `log:"states_list"`
	ActivateList []bool        `log:"activate_list"`
	Result       am.Result
}

// ParseArgs extracts A from [am.Event.Args][APrefix].
func ParseArgs(args am.A) *A {
	// RPC args
	if r, ok := args[APrefix].(*ARpc); ok {
		return amhelp.ArgsToArgs(r, &A{})
	} else if r, ok := args[APrefix].(ARpc); ok {
		return amhelp.ArgsToArgs(&r, &A{})
	}

	// non-RPC args
	if a, _ := args[APrefix].(*A); a != nil {
		return a
	}
	return &A{}
}

// Pass prepares [am.A] from A to pass to further mutations.
func Pass(args *A) am.A {
	return am.A{APrefix: args}
}

// PassRpc prepares [am.A] from A to pass over RPC.
func PassRpc(args *A) am.A {
	return am.A{APrefix: amhelp.ArgsToArgs(args, &ARpc{})}
}

// LogArgs is an args logger for A and [secai.A].
func LogArgs(args am.A) map[string]string {
	a1 := ParseArgs(args)
	if a1 == nil {
		return nil
	}

	return amhelp.ArgsToLogMap(a1, 0)
}

// ///// ///// /////

// ///// UTILS

// ///// ///// /////

// Sp formats a de-dented and trimmed string using the provided arguments, similar to fmt.Sprintf.
func Sp(txt string, args ...any) string {
	txt = dedent.Dedent(strings.Trim(txt, "\n"))
	if len(args) == 0 {
		return txt
	}

	return fmt.Sprintf(txt, args...)
}

// Sl is a string line - expands txt with args and ends with a newline.
func Sl(txt string, args ...any) string {
	return Sp(txt, args...) + "\n"
}

// P formats and prints the given string after de-denting and trimming it, and returns the number of bytes written and
// any error.
func P(txt string, args ...any) {
	fmt.Printf(dedent.Dedent(strings.Trim(txt, "\n")), args...)
}

// Sj is a string join and will join passed string args with a space.
func Sj(parts ...string) string {
	return strings.Join(parts, " ")
}

func MachTelemetry(mach *am.Machine, logArgs am.LogArgsMapperFn) {
	semLogger := mach.SemLogger()

	// default (non-debug) log level
	semLogger.SetLevel(am.LogChanges)
	// dedicated args mapper
	if logArgs != nil {
		semLogger.SetArgsMapper(logArgs)
	} else {
		// default args mapper
		semLogger.SetArgsMapper(am.NewArgsMapper(am.LogArgs, 0))
	}

	// env-based telemetry

	// connect to an am-dbg instance
	amhelp.MachDebugEnv(mach)
	// export metrics to prometheus
	amprom.MachMetricsEnv(mach)
	// loki logger
	err := amtele.BindLokiEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}

	// grafana dashboard
	err = amgen.MachDashboardEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}

	// open telemetry traces
	err = amtele.MachBindOtelEnv(mach)
	if err != nil {
		mach.AddErr(err, nil)
	}
}

// Map maps vals through f and returns a list of returned values from f.
func Map[A, B any](vals []A, f func(A) B) []B {
	return slices.Collect(it.Map(slices.Values(vals), f))
}

// PascalCase converts the input string to pascal case, matching the naming convention of state names.
func PascalCase(in string) string {
	var result strings.Builder
	words := strings.Fields(strings.ToLower(in))
	for _, word := range words {
		if len(word) > 0 {
			result.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}

	return result.String()
}

func RevertPascalCase(in string) string {
	var result strings.Builder
	for i, r := range in {
		if i > 0 && unicode.IsUpper(r) {
			result.WriteRune(' ')
		}
		result.WriteRune(r)
	}

	return result.String()
}

// NumRef returns a number reference from the passed text, or -1 if none found.
func NumRef(text string) int {
	num := strings.Trim(text, " \n\t.")
	i, err := strconv.Atoi(num)
	if err != nil {
		return -1
	}

	return i
}

var rmStyling = regexp.MustCompile(`\[[^\]]*\]`)

func RemoveStyling(str string) string {
	return rmStyling.ReplaceAllString(str, "")
}

func SlicesWithout[S ~[]E, E comparable](coll S, el E) S {
	idx := slices.Index(coll, el)
	ret := slices.Clone(coll)
	if idx == -1 {
		return ret
	}
	return slices.Delete(ret, idx, idx+1)
}

// ///// ///// /////

// ///// STORY

// ///// ///// /////
// TODO add pro-active state triggers based on historical data

type StoryImpl[G any] interface {
	Clone() *G
}

type StoryInfo struct {
	// Name of the bound state (eg StoryFoo).
	State string
	// Tick is the current tick of the bound state.
	Tick uint64
	// Epoch is the sum of all previous memories, before a replacement.
	Epoch uint64
	// TODO htime for last activation

	// Title of this story.
	Title string
	// Description of this story.
	Desc string

	// The story was last deactivated at this human time.
	DeactivatedAt time.Time
	// The story was last active for this many ticks of the AgentLLM machine.
	LastActiveTicks uint64
}

func (s StoryInfo) String() string {
	return s.Title
}

type Story[G any] struct {
	StoryInfo

	// If is an optional function used to confirm that this story can activate. It has access to the whole story struct,
	// so all the involved state machines and their historical snapshots (relative to activation and deactivation of
	// this story).
	CanActivate func(s *G) bool
}

func (s *Story[G]) String() string {
	return fmt.Sprintf("%s: %s", s.Title, s.Desc)
}

func (s *Story[G]) Clone() *Story[G] {
	clone := *s
	return &clone
}

func (s *Story[G]) Check() bool {
	// TODO later bind to When methods, dont re-run each time
	if s.CanActivate == nil {
		return true
	}

	// cast to the outer (precise) type
	// TODO check on init
	outer := any(s).(*G)

	return s.CanActivate(outer)
}

// StoryActor is a binding between a Story and an actor (state machine).
type StoryActor struct {
	Mach *am.Machine

	// actor's time when the story activated
	TimeActivated   am.Time
	TimeDeactivated am.Time

	// When these conditions are met, the story will activate itself.
	Trigger amhelp.Cond
}

type StoryAction struct {

	// state

	// Current value.
	Value func() int
	// Maximum value.
	ValueEnd func() int

	// definition

	Label string
	Desc  string

	// actions (mutations)

	StateAdd    string
	StateRemove string

	// conditions (checking)

	VisibleCook amhelp.Cond
	VisibleMem  amhelp.Cond
	Action      func()
	IsDisabled  func() bool
	LabelEnd    string
	// DisabledCook amhelp.Cond

	// NotQuery string

	Pos         int
	PosInferred bool
}

func (s StoryAction) String() string {
	return s.Label
}

// sorting stories

type StoryActionsByIdx []StoryAction

func (s StoryActionsByIdx) Len() int { return len(s) }
func (s StoryActionsByIdx) Less(i, j int) bool {
	if s[i].Pos != s[j].Pos {
		return s[i].Pos < s[j].Pos
	}

	return !s[i].PosInferred && s[j].PosInferred
}
func (s StoryActionsByIdx) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ///// ///// /////

// ///// CONFIG

// ///// ///// /////

type Config struct {
	AI    ConfigAI
	Agent ConfigAgent
	TUI   ConfigTUI
	Tools ConfigTools
	Debug ConfigDebug
}

type ConfigAI struct {
	OpenAI []ConfigAIOpenAI `kdl:"OpenAI,multiple"`
	Gemini []ConfigAIGemini `kdl:"Gemini,multiple"`
	// Max LLM requests per session.
	ReqLimit int
}

type ConfigAIOpenAI struct {
	Key      string
	Disabled bool
	URL      string
	Model    string
	Tags     []string
	Retries  int
}

type ConfigAIGemini struct {
	Key      string
	Disabled bool
	Model    string
	Tags     []string
	Retries  int
}

type ConfigAgent struct {
	// bot ID
	ID    string
	Label string
	// // dir for tmp files, defaults to CWD
	Dir     string
	Intro   string
	Log     ConfigAgentLog
	History ConfigAgentHistory
}

type ConfigAgentLog struct {
	// duplicate to state-machine log
	Machine bool
	// path to the log file
	File string
	// log level 0-5
	Level   am.LogLevel
	Prompts bool
}

type ConfigAgentHistory struct {
	Backend string
	// TODO BackendParsed enum
	Max int
}

type ConfigTUI struct {
	// SSH port
	Port int
	// SSH host
	Host string
	// Number of transitions to show on the clock
	ClockRange int
}

type ConfigTools struct {
	SearXNG ConfigSearXNG
	// TODO rest
}

type ConfigSearXNG struct {
	// Port to start a local instance on
	Port string
	// URL of an existing instance (disabled Port).
	URL string
}

type ConfigDebug struct {
	// display extra info about these stories in the machine log
	Story []string `kdl:",multiple"`
	// Run the mock scenarios (if any)
	Mock bool
	// Enable misc debugging modes (eg SQL history)
	Misc bool
	// Enable REPL for each machine
	REPL bool
	// Enable debugging in am-dbg ("1" expands to default). am-dbg has to be started BEFORE the bot.
	DBGAddr string
}

// defaults

func ConfigDefault() Config {
	return Config{
		Agent: ConfigAgent{
			Dir: "./tmp",
			History: ConfigAgentHistory{
				Backend: "memory",
				Max:     1_000_000,
			},
		},
		TUI: ConfigTUI{
			Port:       7855,
			Host:       "localhost",
			ClockRange: 10,
		},
		Tools: ConfigTools{
			SearXNG: ConfigSearXNG{
				Port: "7452",
			},
		},
	}
}

func ConfigDefaultOpenAI() ConfigAIOpenAI {
	return ConfigAIOpenAI{
		Retries: 3,
		Model:   openai.GPT4o,
	}
}

func ConfigDefaultGemini() ConfigAIGemini {
	return ConfigAIGemini{
		Retries: 3,
		// TODO link from genai pkg
		Model: "gemini-2.5-flash",
	}
}

// TODO ConfigToEnv(cfg any) (string, error) {
// }

// TODO dump SQL queries
// // LogDB wraps a standard sql.DB or sql.Tx
// type LogDB struct {
//	DB *sql.DB
// }
//
// func (l *LogDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
//	log.Printf("[DUMP] Exec: %s | Args: %v", query, args)
//	return l.DB.ExecContext(ctx, query, args...)
// }
//
// func (l *LogDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
//	log.Printf("[DUMP] Query: %s | Args: %v", query, args)
//	return l.DB.QueryContext(ctx, query, args...)
// }
//
// func (l *LogDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
//	log.Printf("[DUMP] QueryRow: %s | Args: %v", query, args)
//	return l.DB.QueryRowContext(ctx, query, args...)
// }
//
// // Usage:
// // realDB, _ := sql.Open(...)
// // queries := db.New(&LogDB{DB: realDB})
