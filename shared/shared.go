package shared

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BooleanCat/go-functional/v2/it"
	"github.com/jxnl/instructor-go/pkg/instructor/core"
	"github.com/lithammer/dedent"
	"github.com/orsinium-labs/enum"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
	"github.com/sblinch/kdl-go"
	"github.com/sblinch/kdl-go/document"

	"github.com/pancsta/secai/states"
)

const (
	EnvNoDotEnv = "SECAI_NO_DOTENV"
)

var ss = states.AgentBaseStates

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

type AgentStore struct {
	M         map[string]any
	ClockDiff [][]int
	Web       fs.FS
}

// ///// ///// /////

// ///// ARGS

// ///// ///// /////

const APrefix = "secai"

type Args struct {
	am.ArgsBase
}

func (Args) ArgsPrefix() string {
	return APrefix
}

// -----

type AStoryAction struct {
	Args
	// ID of the action.
	ID string `log:"id"`
}

func (AStoryAction) ArgsState() string {
	return ss.StoryAction
}

// -----

type AUIRenderStories struct {
	Args
	Actions []ActionInfo `log:"actions"`
	Stories []StoryInfo  `log:"stories"`
}

func (AUIRenderStories) ArgsState() string {
	return ss.UIRenderStories
}

// -----

type AUIMsg struct {
	Args
	// Msg is a single message with an author and text.
	Msg *Msg `log:"msg"`
}

func (AUIMsg) ArgsState() string {
	return ss.UIMsg
}

// -----

type APrompt struct {
	Args
	// Prompt is a prompt to be sent to LLM.
	Prompt string `log:"prompt"`
}

func (APrompt) ArgsState() string {
	return ss.Prompt
}

// -----

type AStoryChanged struct {
	Args
	StatesList   []string `log:"states_list"`
	ActivateList []bool   `log:"activate_list"`
}

func (AStoryChanged) ArgsState() string {
	return ss.StoryChanged
}

// -----

type AInterrupted struct {
	Args
	IntByTimeout bool `log:"int_by_timeout"`
}

func (AInterrupted) ArgsState() string {
	return ss.Interrupted
}

// -----

type AConfigUpdate struct {
	Args
	ConfigAI *ConfigAI
}

func (AConfigUpdate) ArgsState() string {
	return ss.ConfigUpdate
}

// -----

type AUIRenderClock struct {
	Args
	ClockDiff [][]int
}

func (AUIRenderClock) ArgsState() string {
	return ss.UIRenderClock
}

// -----

type ASSHDisconn struct {
	Args
	Addr string `log:"addr"`
	// Id of the disconnected TUI.
	Id string `log:"id"`
}

func (ASSHDisconn) ArgsState() string {
	return ss.SSHDisconn
}

// -----

func init() {
	for _, arg := range ArgsRPC {
		gob.Register(arg)
	}
}

var ArgsRPC = []am.ArgsApi{AStoryAction{}, AUIRenderStories{}, AUIMsg{}, APrompt{}, AInterrupted{}, AConfigUpdate{}, AUIRenderClock{}}

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

func TextCut(txt string, maxLen int) string {
	if len(txt) > maxLen {
		return txt[:maxLen] + "..."
	}
	return txt
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
	AI    ConfigAI
	Agent ConfigAgent
	Web   ConfigWeb
	TUI   ConfigTUI
	Tools ConfigTools
	Debug ConfigDebug

	// internal

	// File is the path to the loaded config file.
	File      string `kdl:"-"`
	ProdBuild bool   `kdl:"-"`
}

type ConfigAI struct {
	OpenAI []ConfigAIOpenAI `kdl:"OpenAI,multiple"`
	Gemini []ConfigAIGemini `kdl:"Gemini,multiple"`
	// Max LLM requests per session.
	ReqLimit int
}

// ConfigAICommon are shared fields across all AI providers.
type ConfigAICommon struct {
	Model    string
	Key      string
	Disabled bool
	// Def: 3
	Retries  int      `kdl:",omitempty"`
	Tags     []string `kdl:",omitempty"`
	Priority int      `kdl:",omitempty"`
	// For how long blacklist this provider on error (def 1h).
	FallbackTimeout time.Duration `kdl:",omitempty"`
	// Number of concurrent req for this provider TODO
	Concurrency int

	// internal

	// temporarily disabled (eg offline)
	DisabledUntil time.Time `kdl:"-"`
	// provider name
	Provider string `kdl:"-"`
	// num of times this backend was called without errs
	Calls int `kdl:"-"`
}

func (c *ConfigAICommon) IsEnabled() bool {
	if c.Disabled {
		return false
	}
	if c.Key == "" {
		return false
	}
	if !c.DisabledUntil.IsZero() && c.DisabledUntil.After(time.Now()) {
		return false
	}

	return true
}

type ConfigAIOpenAI struct {
	ConfigAICommon

	URL string
	// NoEnforceSchema skips `instr.WithMode(instr.ModeJSONSchema)` for local models
	NoEnforceSchema bool
}

type ConfigAIGemini struct {
	ConfigAICommon
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
	// agent log level
	Level slog.Level
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
	Verbose    bool
	VerboseSQL bool
	// Create value files for inspection
	ValFiles bool
	// Enable REPL for agent, mem, and tools
	REPL bool
	// Connect and send dbg info to am-dbg
	DBGAddr string
	// TODO Pass these vars to WASM, merge with .env
	// WebEnv []string

	// embeds

	// Start an embedded debugger on localhost:{DBGAddr}
	DBGEmbed bool
	// Expose the embedded debugger on http://localhost:{DBGEmbedWeb}
	DBGEmbedWeb int
	// Start a web REPL on http://localhost:{REPLWeb}
	REPLWeb int
}

// defaults

// ConfigDefault produces a default config with ports in the range 12800-12900.
func ConfigDefault() Config {
	return Config{
		Agent: ConfigAgent{
			Dir: "./tmp",
			History: ConfigAgentHistory{
				Backend: "memory",
				Max:     1_000_000,
			},
			Log: ConfigAgentLog{
				Level: slog.LevelInfo,
			},
		},
		Web: ConfigWeb{
			Addr:    "localhost:12854",
			LogPort: 12858,
			DBPort:  -1,
		},
		TUI: ConfigTUI{
			PortSSH:    12868,
			PortWeb:    12878,
			Host:       "localhost",
			ClockRange: 10,
		},
		Tools: ConfigTools{
			SearXNG: ConfigSearXNG{
				Port: "7452",
			},
		},
		Debug: ConfigDebug{
			REPLWeb: -1,
		},
	}
}

func ConfigDefaultAIOpenAI() ConfigAIOpenAI {
	return ConfigAIOpenAI{
		ConfigAICommon: ConfigAICommon{
			Retries: 3,
			// TODO breaks WASM linking
			// Model:   openai.GPT4o,
			Model: "gpt-4o",
			// TODO enum
			Provider:        "openai",
			FallbackTimeout: time.Hour,
		},
	}
}

func ConfigDefaultAIGemini() ConfigAIGemini {
	return ConfigAIGemini{
		ConfigAICommon: ConfigAICommon{
			Retries: 3,
			// TODO link from genai pkg
			Model: "gemini-2.5-flash",
			// TODO enum
			Provider:        "gemini",
			FallbackTimeout: time.Hour,
		},
	}
}

// TODO keep in sync with debugger.Params
func (c *ConfigDebug) DbgAddrs() (dbgAddr, httpAddr, sshAddr string, err error) {
	dbgAddr = c.DBGAddr
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
//	log.Printf("[DUMP] Exec: %s | ArgsBase: %v", query, args)
//	return l.DB.ExecContext(ctx, query, args...)
// }
//
// func (l *LogDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
//	log.Printf("[DUMP] Query: %s | ArgsBase: %v", query, args)
//	return l.DB.QueryContext(ctx, query, args...)
// }
//
// func (l *LogDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
//	log.Printf("[DUMP] QueryRow: %s | ArgsBase: %v", query, args)
//	return l.DB.QueryRowContext(ctx, query, args...)
// }
//
// // Usage:
// // realDB, _ := sql.Open(...)
// // queries := db.New(&LogDB{DB: realDB})

func (c *ConfigWeb) DBAddrs() (base, agent, mach string) {
	if c.DBPort == -1 {
		return "", "", ""
	}

	port := c.DBPort
	return "localhost:" + strconv.Itoa(port),
		"localhost:" + strconv.Itoa(port+1),
		"localhost:" + strconv.Itoa(port+2)
}

func (c *ConfigWeb) ConfigWebLogAddr() string {
	if c.LogPort == -1 {
		return ""
	}

	return "localhost:" + strconv.Itoa(c.LogPort)
}

// DirPath returns an absolute path to the agent's data directory.
func (c *ConfigAgent) DirPath() string {
	dir, err := filepath.Abs(c.Dir)
	if err != nil {
		return c.Dir
	}
	return dir
}

func (c *ConfigAgent) LogPath(absolute bool) string {
	logFile := filepath.Join(c.Dir, "logs", c.ID+".jsonl")
	if v := c.Log.File; v != "" {
		logFile = v
	}

	if absolute {
		logFile, _ = filepath.Abs(logFile)
	}
	return logFile
}

func BinaryPath(absolute bool) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	bin := filepath.Base(exePath)

	// go build
	if strings.HasPrefix(bin, "__") || absolute {
		return exePath
	}

	if !absolute && runtime.GOOS != "windows" {
		bin = "./" + bin
	}
	return bin
}

// TODO extract to configAPIs
func (c *ConfigWeb) AddrAgent() string {
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

func (c *ConfigWeb) REPLAddrDash() string {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}

	// REPLs are always random, as they'd collide without tunnel matchers
	return host + ":0"
}

func (c *ConfigWeb) REPLAddrAgentUI() string {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}

	// REPLs are always random, as they'd collide without tunnel matchers
	return host + ":0"
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
	writeEnv("WEB_ADDR_AGENT", cfg.Web.AddrAgent())
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
	writeEnv("DEBUG_VERBOSE_SQL", cfg.Debug.VerboseSQL)
	writeEnv("DEBUG_REPL", cfg.Debug.REPL)
	writeEnv("DEBUG_DBG_ADDR", cfg.Debug.DBGAddr)
	writeEnv("DEBUG_DBG_EMBED", cfg.Debug.DBGEmbed)
	writeEnv("DEBUG_DBG_EMBED_WEB", cfg.Debug.DBGEmbedWeb)
	writeEnv("DEBUG_REPL_WEB", cfg.Debug.REPLWeb)

	return sb.String()
}

// ///// ///// /////

// ///// KDL FORMAT

// ///// ///// /////

// KdlFormat parses a raw KDL string and returns it formatted with proper
// newlines, inherited indentation, and names resolved from the Config struct.
func KdlFormat(input []byte, cfg any) string {
	nameMap := kdlBuildNameMap(cfg)

	doc, err := kdl.Parse(bytes.NewReader(input))
	if err != nil {
		return fmt.Sprintf("kdlfmt: parse error: %v\n", err)
	}

	var b strings.Builder
	for _, n := range doc.Nodes {
		b.WriteString(kdlFormatNode(n, 0, nameMap))
	}
	return b.String()
}

// kdlBuildNameMap walks the cfg struct via reflection and returns a map from
// lowercase KDL names to the corresponding Go field names.
func kdlBuildNameMap(v any) map[string]string {
	m := make(map[string]string)
	kdlCollectFields(reflect.ValueOf(v), m)
	return m
}

func kdlCollectFields(val reflect.Value, m map[string]string) {
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}
	t := val.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		kdlTag := f.Tag.Get("kdl")
		if kdlTag == "-" {
			continue
		}
		kdlName := kdlTagName(kdlTag, f.Name)
		m[kdlName] = f.Name

		// recurse into struct fields for child node names
		fv := val.Field(i)
		if fv.Kind() == reflect.Struct {
			kdlCollectFields(fv, m)
		} else if fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.Struct {
			kdlCollectFields(fv, m)
		} else if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.Struct {
			kdlCollectFields(reflect.New(fv.Type().Elem()).Elem(), m)
		}
	}
}

func kdlTagName(tag, goName string) string {
	if tag != "" {
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name := tag[:comma]
			if name != "" {
				return strings.ToLower(name)
			}
		} else {
			return strings.ToLower(tag)
		}
	}
	return strings.ToLower(goName)
}

func kdlResolveName(kdlName string, nameMap map[string]string) string {
	if name, ok := nameMap[strings.ToLower(kdlName)]; ok {
		return name
	}
	return kdlCapitalizeFallback(kdlName)
}

func kdlCapitalizeFallback(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func kdlFormatNode(n *document.Node, depth int, nameMap map[string]string) string {
	var b strings.Builder
	indent := strings.Repeat("  ", depth)

	name := kdlResolveName(n.Name.Value.(string), nameMap)
	b.WriteString(indent)
	b.WriteString(name)

	children := n.Children
	props := n.Properties.Unordered()
	hasBraces := len(children) > 0 || len(props) > 0

	if !hasBraces {
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(" {\n")

	childIndent := strings.Repeat("  ", depth+1)
	for k, v := range props {
		// if kdlIsZeroValue(v) {
		// 	continue
		// }
		key := kdlResolveName(k, nameMap)
		b.WriteString(childIndent)
		b.WriteString(key)
		if v.Value != nil {
			b.WriteString(" ")
			b.WriteString(kdlFormatValue(v))
		}
		b.WriteString("\n")
	}

	// output arguments (positional values without keys)
	for _, arg := range n.Arguments {
		b.WriteString(childIndent)
		b.WriteString(kdlFormatValue(arg))
		b.WriteString("\n")
	}

	for _, child := range children {
		b.WriteString(kdlFormatNode(child, depth+1, nameMap))
	}

	b.WriteString(indent)
	b.WriteString("}\n")

	return b.String()
}

// func kdlIsZeroValue(v *document.Value) bool {
// 	if v.Value == nil {
// 		return true
// 	}
// 	switch x := v.Value.(type) {
// 	case string:
// 		return x == ""
// 	case bool:
// 		return !x
// 	case int:
// 		return x == 0
// 	case int8:
// 		return x == 0
// 	case int16:
// 		return x == 0
// 	case int32:
// 		return x == 0
// 	case int64:
// 		return x == 0
// 	case uint:
// 		return x == 0
// 	case uint8:
// 		return x == 0
// 	case uint16:
// 		return x == 0
// 	case uint32:
// 		return x == 0
// 	case uint64:
// 		return x == 0
// 	case float32:
// 		return x == 0.0
// 	case float64:
// 		return x == 0.0
// 	}
// 	return false
// }

func kdlFormatValue(v *document.Value) string {
	if v.Value == nil {
		return "null"
	}
	switch x := v.Value.(type) {
	case bool:
		return strconv.FormatBool(x)
	case string:
		return strconv.Quote(x)
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(x)
	default:
		return strconv.Quote(fmt.Sprint(x))
	}
}

// ///// ///// /////

// ///// RSS

// ///// ///// /////

type FeedItem struct {
	Title  string
	Author string
	Link   string
	Date   string
	Desc   string
}

func escapeXML(input string) string {
	var buffer bytes.Buffer
	// xml.EscapeText writes safely escaped XML text to the buffer
	if err := xml.EscapeText(&buffer, []byte(input)); err != nil {
		// This error is very rare (buffer write error), but good to handle
		return ""
	}
	return buffer.String()
}

func GenerateAtomFeed(items []FeedItem, title string) string {
	now := time.Now().Format(time.RFC3339)
	feed := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
		<feed xmlns="http://www.w3.org/2005/Atom">
		  <title>%s</title>
		  <link href="https://github.com"/>
		  <updated>%s</updated>
		  <id>starred-repos-feed</id>
		`, title, now)

	for _, item := range items {
		title := escapeXML(item.Title)
		feed += fmt.Sprintf(`
			<entry>
		    <title>%s</title>
		    <link href="%s"/>
		    <updated>%s</updated>
		    <author>%s</author>
		    <description>%s</description>
		  </entry>
		`, title, item.Link, item.Date, item.Author, item.Desc)
	}

	feed += `</feed>`
	return feed
}

// ///// ///// /////

// ///// LOGGER

// ///// ///// /////

// SlogWriter adapts an slog.Logger to the io.Writer interface.
type SlogWriter struct {
	logger *slog.Logger
	level  slog.Level
}

// NewSlogWriter creates a new io.Writer that writes to the provided slog.Logger
// at the specified log level.
func NewSlogWriter(logger *slog.Logger, level slog.Level) *SlogWriter {
	return &SlogWriter{
		logger: logger,
		level:  level,
	}
}

// Write implements the io.Writer interface.
func (w *SlogWriter) Write(p []byte) (n int, err error) {
	// Trim trailing newlines because slog will append its own formatting.
	// This prevents empty lines in your log output.
	msg := bytes.TrimSuffix(p, []byte{'\n'})

	// Log the message using the stored logger and level.
	// context.Background() is used because the io.Writer interface
	// does not provide a way to pass a context.
	w.logger.Log(context.Background(), w.level, string(msg))

	// Return the original length of p to satisfy the io.Writer contract,
	// even though we trimmed the newline internally.
	return len(p), nil
}

// ///// ///// /////

// ///// PROMPT

// ///// ///// /////

// TODO RemoveTool, RemoveDoc

type PromptMsg struct {
	From    core.Role
	Content string
}

type PromptOpts struct {
	// required provider tags
	Tags []string
	// disallowed provider tags
	NoTags []string
	// skip checking if state is active
	SkipState bool
}

// optBindOpts will return the first [BindOpts] from a list.
func optPromptOpts(args []PromptOpts) PromptOpts {
	if len(args) > 0 {
		return args[0]
	}
	return PromptOpts{}
}

// TODO incomplete?
type PromptApi interface {
	AddTool(tool ToolApi)
	AddDoc(doc *Document)

	GenSysPrompt() string
	Conversation() (*core.Conversation, string)
	HistClean()
}

type PromptSchemaless = Prompt[any, any]

var _ PromptApi = &Prompt[any, any]{}

// AddTool registers a SECAI TOOL which then exports it's documents into the system prompt. This is different from an AI tool.
func (p *Prompt[P, R]) AddTool(tool ToolApi) {
	p.tools[tool.Mach().Id()] = tool
}

// AddDoc adds a document into the system prompt.
func (p *Prompt[P, R]) AddDoc(doc *Document) {
	p.docs[doc.Title()] = doc
}

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
	return strings.Trim(Sp(`
		%s
		%s
		%s
		%s
		`, cond, steps, result, docs), "\n ")
}

// Conversation will create a conversation with history and system prompt, return sys prompt on the side.
func (p *Prompt[P, R]) Conversation() (*core.Conversation, string) {
	sys := p.GenSysPrompt()
	// TODO marshal sys prompts, add generic params schema for attached documents
	c := core.NewConversation(sys)
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

var (
	ErrHistNil = errors.New("history is nil")
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

// TOOL

type ToolApi interface {
	Mach() *am.Machine
	SetMach(*am.Machine)
	Document() *Document
}

// ///// ///// /////

// ///// ERRORS

// ///// ///// /////

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
	return mach.EvAddErrState(event, ss.ErrDB, err, OptArgs(args))
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
	return mach.EvAddErrState(event, ss.ErrAI, err, OptArgs(args))
}

// ///// ///// /////

// ///// UTILS

// ///// ///// /////

func GetVersion() string {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}

	ver := build.Main.Version
	if ver == "" {
		return "(devel)"
	}

	return ver
}
