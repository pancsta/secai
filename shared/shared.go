package shared

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BooleanCat/go-functional/v2/it"
	"github.com/lithammer/dedent"
	"github.com/orsinium-labs/enum"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
)

const (
	// EnvConfig config location
	EnvConfig   = "SECAI_CONFIG"
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

func init() {
	gob.Register(ARPC{})
}

const APrefix = "secai"

// ARPC is a subset of [am.A] that can be passed over RPC.
type ARPC struct {
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
	Actions      []ActionInfo `log:"actions"`
	Stories      []StoryInfo  `log:"stories"`
	StatesList   []string     `log:"states_list"`
	ActivateList []bool       `log:"activate_list"`
	Result       am.Result
	ConfigAI     *ConfigAI
	ClockDiff    [][]int
}

// ParseArgs extracts A from [am.Event.Args][APrefix].
func ParseArgs(args am.A) *A {
	// RPC args
	if r, ok := args[APrefix].(*ARPC); ok {
		return amhelp.ArgsToArgs(r, &A{})
	} else if r, ok := args[APrefix].(ARPC); ok {
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

// PassRPC prepares [am.A] from A to pass over RPC.
func PassRPC(args *A) am.A {
	return am.A{APrefix: amhelp.ArgsToArgs(args, &ARPC{})}
}

// LogArgs is an args logger for A and [secai.A].
func LogArgs(args am.A) map[string]string {
	a1 := ParseArgs(args)
	if a1 == nil {
		return nil
	}

	return amhelp.ArgsToLogMap(a1, 0)
}

// ParseRpc parses am.A to *ARPC wrapped in am.A. Useful for REPLs.
func ParseRpc(args am.A) am.A {
	ret := am.A{APrefix: &ARPC{}}
	jsonArgs, err := json.Marshal(args)
	// TODO pre-gen json
	if err == nil {
		json.Unmarshal(jsonArgs, ret[APrefix])
	}

	return ret
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

// OptArgs will read the first A from an optional list.
func OptArgs(args []am.A) am.A {
	if len(args) > 0 {
		return args[0]
	}
	return nil
}

// OpenURL opens the specified URL in the default browser of the user.
// https://gist.github.com/sevkin/9798d67b2cb9d07cb05f89f14ba682f8
func OpenURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd.exe"
		args = []string{
			"/c", "rundll32", "url.dll,FileProtocolHandler",
			strings.ReplaceAll(url, "&", "^&"),
		}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		if isWSL() {
			cmd = "cmd.exe"
			args = []string{"start", url}
		} else {
			cmd = "xdg-open"
			args = []string{url}
		}
	}

	e := exec.Command(cmd, args...)
	err := e.Start()
	if err != nil {
		return err
	}
	err = e.Wait()
	if err != nil {
		return err
	}

	return nil
}

// isWSL checks if the Go program is running inside Windows Subsystem for Linux
func isWSL() bool {
	releaseData, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(releaseData)), "microsoft")
}

// ///// ///// /////

// ///// STORY

// ///// ///// /////
// TODO add pro-active state triggers based on historical data

// StoryInfo is a static model for [Story].
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

// Story is the basis for all stories.
type Story struct {
	StoryInfo

	Agent  StoryActor
	Memory StoryActor

	// If is an optional function used to confirm that this story can activate. It has access to the whole story struct,
	// so all the involved state machines and their historical snapshots (relative to activation and deactivation of
	// this story).
	CanActivate func(instance *Story) bool
	Actions     []Action
}

// New returns a copy of the story with the actions bound. Used to create new instances.
func (s *Story) New(actions []Action) *Story {
	clone := *s
	clone.Actions = actions
	return &clone
}

func (s *Story) String() string {
	return fmt.Sprintf("%s: %s", s.Title, s.Desc)
}

func (s *Story) Check() bool {
	// TODO later bind to When methods, dont re-run each time
	if s.CanActivate == nil {
		return true
	}

	// TODO check on init ?

	return s.CanActivate(s)
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

type Action struct {
	// random ID
	ID string

	// state

	// Current value.
	Value func() int
	// Maximum value.
	ValueEnd func() int

	// definition

	Label  string
	Desc   string
	Action func()

	// actions (mutations)

	StateAdd    string
	StateRemove string

	// conditions (checking)

	VisibleAgent amhelp.Cond
	VisibleMem   amhelp.Cond
	IsDisabled   func() bool
	LabelEnd     string
	// DisabledCook amhelp.Cond

	// NotQuery string

	Pos         int
	PosInferred bool
}

// ActionInfo is a static model for [Action].
type ActionInfo struct {
	// random ID
	ID string

	// state

	// Current value.
	Value int
	// Maximum value.
	ValueEnd int

	// definition

	Label  string
	Desc   string
	Action bool

	// actions (mutations)

	StateAdd    string
	StateRemove string

	// conditions (checking)

	VisibleAgent bool
	VisibleMem   bool
	IsDisabled   bool
	LabelEnd     string
	// DisabledCook amhelp.Cond

	// NotQuery string

	Pos         int
	PosInferred bool
}

func (s Action) String() string {
	return s.Label
}

// sorting stories

type StoryActionsByIdx []Action

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
	// File is the path to the loaded config file.
	File  string `kdl:"-"`
	AI    ConfigAI
	Agent ConfigAgent
	Web   ConfigWeb
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
	Retries  int `kdl:",omitempty"`
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
	Dir       string
	Intro     string
	IntroDash string
	Footer    string
	Log       ConfigAgentLog
	History   ConfigAgentHistory
}

type ConfigAgentLog struct {
	// path to the log file
	File string
	// log prompts
	Prompts bool
	// duplicate log to state-machine log
	MachFwd bool
	// mach log level 0-5
	MachLevel am.LogLevel
	// print machine log
	MachPrint bool
}

type ConfigAgentHistory struct {
	Backend string
	// TODO BackendParsed enum
	Max int
}

type ConfigWeb struct {
	// Base address for HTTP services:
	// - main HTTP server
	// - +1 WebSocket addr of the agent's RPC server (dashboard)
	// - +2 WebSocket addr of the agent's RPC server (agent UI)
	// - +3 TCP addr of the dashboard REPL server (WS tunnel)
	// - +4 TCP addr of the agent UI REPL server (WS tunnel)
	Addr string
	// Start a DBPort web UI on http://localhost:{DBPort[0-2]}
	DBPort int
	// Start a log web UI on http://localhost:{LogPort}
	LogPort int
}

type ConfigTUI struct {
	// TODO Addr
	// TODO WebAddr
	// SSH host
	Host string
	// SSH port
	PortSSH int
	// Web host
	WebHost string
	// Web port
	PortWeb int
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
	// URL of an existing instance (disables Port).
	URL string
}

type ConfigDebug struct {
	// Display extra info about these stories in the machine log
	Story []string
	// Run the mock scenario
	Mock bool
	// Start pprof on addr
	ProfilerAddr string
	// Enable misc debugging modes (SQL history, am-relay, browser RPC)
	// TODO extract SQL
	Verbose bool
	// Create value files for inspection
	ValFiles bool
	// Enable REPL for agent, mem, and tools
	REPL bool
	// Connect and send dbg info to am-dbg
	DBGAddr string
	// TODO Pass these vars to WASM, merge with .env
	// WebEnv []string

	// embeds

	// Start an embedded debugger on localhost:{DBGEmbed}
	DBGEmbed bool
	// Expose the embedded debugger on http://localhost:{DBGEmbedWeb}
	DBGEmbedWeb int
	// Start a web REPL on http://localhost:{REPLWeb}
	REPLWeb int
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
		Web: ConfigWeb{
			Addr:    "localhost:12854",
			LogPort: 12858,
			DBPort:  -1,
		},
		TUI: ConfigTUI{
			PortSSH:    7855,
			PortWeb:    7856,
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
		// TODO breaks WASM linking
		// Model:   openai.GPT4o,
		Model: "gpt-4o",
	}
}

func ConfigDefaultGemini() ConfigAIGemini {
	return ConfigAIGemini{
		Retries: 3,
		// TODO link from genai pkg
		Model: "gemini-2.5-flash",
	}
}

// TODO config method
func ConfigDbgAddrs(cfg ConfigDebug) (dbgAddr, httpAddr, sshAddr string, err error) {
	dbgAddr = cfg.DBGAddr
	if dbgAddr == "1" {
		dbgAddr = dbg.DbgAddr
	}
	host, port, err := net.SplitHostPort(dbgAddr)
	if err != nil {
		return "", "", "", err
	}
	dbgPort, err := strconv.Atoi(port)
	if err != nil {
		return "", "", "", err
	}
	httpPort := dbgPort + 1
	sshPort := httpPort + 1
	httpAddr = host + ":" + strconv.Itoa(httpPort)
	sshAddr = host + ":" + strconv.Itoa(sshPort)

	return dbgAddr, httpAddr, sshAddr, nil
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

// TODO config method
func ConfigWebDBAddrs(cfg ConfigWeb) (base, agent, mach string) {
	if cfg.DBPort == -1 {
		return "", "", ""
	}

	port := cfg.DBPort
	return "localhost:" + strconv.Itoa(port),
		"localhost:" + strconv.Itoa(port+1),
		"localhost:" + strconv.Itoa(port+2)
}

// TODO config method
func ConfigWebLogAddr(cfg ConfigWeb) string {
	if cfg.LogPort == -1 {
		return ""
	}

	return "localhost:" + strconv.Itoa(cfg.LogPort)
}

// TODO config method
func ConfigLogPath(cfg ConfigAgent) string {
	logFile := filepath.Join(cfg.Dir, cfg.ID+".jsonl")
	if v := cfg.Log.File; v != "" {
		logFile = v
	}

	return logFile
}

// TODO config method
func BinaryPath(cfg *Config) string {
	bin := "./" + cfg.Agent.ID
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if v := os.Getenv("SECAI_AGENT_DIR"); v != "" {
		bin = filepath.Join(v, bin)
	}

	return bin
}

func (c *ConfigWeb) AgentWSAddrDash() string {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	webPort, err := strconv.Atoi(port)
	if err != nil {
		return ""
	}
	return host + ":" + strconv.Itoa(webPort+1)
}

// TODO remove when dialing via relay lands
func (c *ConfigWeb) AgentWSAddrRemoteUI() string {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	webPort, err := strconv.Atoi(port)
	if err != nil {
		return ""
	}
	return host + ":" + strconv.Itoa(webPort+2)
}

func (c *ConfigWeb) REPLAddrDash() string {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	webPort, err := strconv.Atoi(port)
	if err != nil {
		return ""
	}
	return host + ":" + strconv.Itoa(webPort+3)
}

func (c *ConfigWeb) REPLAddrAgentUI() string {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	webPort, err := strconv.Atoi(port)
	if err != nil {
		return ""
	}
	return host + ":" + strconv.Itoa(webPort+4)
}

func (c *ConfigWeb) DashURL() string {
	return "http://" + c.Addr
}

func (c *ConfigWeb) AgentURL() string {
	return "http://" + c.Addr + "/agent"
}

func (cfg *Config) DotEnv() string {
	var sb strings.Builder

	writeEnv := func(key string, value any) {
		switch v := value.(type) {
		case string:
			sb.WriteString(fmt.Sprintf("SECAI_%s=\"%s\"\n", key, v))
		case []string:
			sb.WriteString(fmt.Sprintf("SECAI_%s=\"%s\"\n", key, strings.Join(v, ",")))
		default:
			sb.WriteString(fmt.Sprintf("SECAI_%s=%v\n", key, v))
		}
	}

	sb.WriteString("# ==========================================\n")
	sb.WriteString("# AGENT CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("ID", cfg.Agent.ID)
	writeEnv("LABEL", cfg.Agent.Label)
	writeEnv("DIR", cfg.Agent.Dir)
	writeEnv("INTRO", cfg.Agent.Intro)
	writeEnv("INTRO_DASH", cfg.Agent.IntroDash)
	writeEnv("FOOTER", cfg.Agent.Footer)

	writeEnv("LOG_FILE", cfg.Agent.Log.File)
	writeEnv("LOG_PROMPTS", cfg.Agent.Log.Prompts)
	writeEnv("LOG_MACH_FWD", cfg.Agent.Log.MachFwd)
	writeEnv("LOG_MACH_LEVEL", cfg.Agent.Log.MachLevel)
	writeEnv("LOG_MACH_PRINT", cfg.Agent.Log.MachPrint)

	writeEnv("HISTORY_BACKEND", cfg.Agent.History.Backend)
	writeEnv("HISTORY_MAX", cfg.Agent.History.Max)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# WEB CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("WEB_ADDR", cfg.Web.Addr)
	writeEnv("WEB_WS_ADDR_DASH", cfg.Web.AgentWSAddrDash())
	writeEnv("WEB_WS_ADDR_REMOTEUI", cfg.Web.AgentWSAddrRemoteUI())
	writeEnv("WEB_DASH_REPL_ADDR", cfg.Web.REPLAddrDash())
	writeEnv("WEB_AGENTUI_REPL_ADDR", cfg.Web.REPLAddrAgentUI())
	writeEnv("WEB_DB_PORT", cfg.Web.DBPort)
	writeEnv("WEB_LOG_PORT", cfg.Web.LogPort)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# TUI CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("TUI_HOST", cfg.TUI.Host)
	writeEnv("TUI_PORT_SSH", cfg.TUI.PortSSH)
	writeEnv("TUI_WEB_HOST", cfg.TUI.WebHost)
	writeEnv("TUI_PORT_WEB", cfg.TUI.PortWeb)
	writeEnv("TUI_CLOCK_RANGE", cfg.TUI.ClockRange)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# TOOLS CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("TOOLS_SEARXNG_PORT", cfg.Tools.SearXNG.Port)
	writeEnv("TOOLS_SEARXNG_URL", cfg.Tools.SearXNG.URL)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# DEBUG CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("DEBUG_STORY", cfg.Debug.Story)
	writeEnv("DEBUG_MOCK", cfg.Debug.Mock)
	writeEnv("DEBUG_PROFILER_ADDR", cfg.Debug.ProfilerAddr)
	writeEnv("DEBUG_VERBOSE", cfg.Debug.Verbose)
	writeEnv("DEBUG_REPL", cfg.Debug.REPL)
	writeEnv("DEBUG_DBG_ADDR", cfg.Debug.DBGAddr)
	writeEnv("DEBUG_DBG_EMBED", cfg.Debug.DBGEmbed)
	writeEnv("DEBUG_DBG_EMBED_WEB", cfg.Debug.DBGEmbedWeb)
	writeEnv("DEBUG_REPL_WEB", cfg.Debug.REPLWeb)

	return sb.String()
}
