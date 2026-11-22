package shared

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/gob"
	"encoding/xml"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
	"sync"
	"time"
	"unicode"

	"github.com/BooleanCat/go-functional/v2/it"
	"github.com/lithammer/dedent"
	"github.com/orsinium-labs/enum"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
	"github.com/pancsta/instructor-go/pkg/instructor/core"
	"github.com/sblinch/kdl-go"
	"github.com/sblinch/kdl-go/document"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/pancsta/secai/states"
)

const (
	EnvNoDotEnv = "SECAI_NO_DOTENV"
)

var ss = states.AgentBaseStates
var Fmt = message.NewPrinter(language.English)

// From enum

type From enum.Member[string]

var (
	FromAssistant = From{"assistant"}
	FromSystem    = From{"system"}
	FromUser      = From{"user"}
	FromNarrator  = From{"narrator"}

	FromEnum = enum.New(FromAssistant, FromSystem, FromUser)
)

// ValEnc enum

type ValEnc enum.Member[string]

var (
	ValEncJson = ValEnc{"json"}
	ValEncYaml = ValEnc{"yaml"}
	ValEncDump = ValEnc{"dump"}
	ValEncTxt  = ValEnc{"txt"}

	ValEncEnum = enum.New(ValEncJson, ValEncYaml, ValEncDump, ValEncTxt)
	ValEncs    = ValEncEnum
)

type ValFileEnc = ValEnc

var (
	ValFileEncJson = ValEncJson
	ValFileEncYaml = ValEncYaml
	ValFileEncDump = ValEncDump
	ValFileEncTxt  = ValEncTxt

	ValFileEncEnum = ValEncEnum
	ValFileEncs    = ValEncs
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
	// IsVisible is an optional dynamic visibility condition, evaluated on each
	// UI render. It takes precedence over VisibleAgent and VisibleMem.
	IsVisible func() bool
	LabelEnd  string
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
	// ServerMode doesnt autorun the GUI and listens on 0.0.0.0
	ServerMode bool `kdl:"-"`
	DockerMode bool `kdl:"-"`
	// random instance ID
	SessionID string `kdl:"-"`
}

func (c Config) Clone() Config {
	clone := c

	// link root
	clone.Web.Root = &clone
	clone.Debug.Root = &clone
	clone.TUI.Root = &clone

	return clone
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

	Name string `kdl:",omitempty"`
	// Def: 3
	Retries  int      `kdl:",omitempty"`
	Tags     []string `kdl:",omitempty"`
	Priority int      `kdl:",omitempty"`
	// For how long blacklist this provider on error (def 1h).
	FallbackTimeout time.Duration `kdl:",omitempty"`
	// Number of concurrent req for this provider TODO
	Concurrency int    `kdl:",omitempty"`
	URL         string `kdl:",omitempty"`

	// TODO

	// EnabledHoursUTC []int `kdl:",omitempty"`
	// DisabledHoursUTC []int `kdl:",omitempty"`

	// internal

	// temporarily disabled (eg offline)
	DisabledUntil time.Time `kdl:"-"`
	// provider name
	API string `kdl:"-"`
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

	// TODO EnabledHoursUTC, DisabledHoursUTC

	return true
}

type ConfigAIOpenAI struct {
	ConfigAICommon

	// NoEnforceSchema skips `instr.WithMode(instr.ModeJSONSchema)` for local models
	NoEnforceSchema bool
}

type ConfigAIGemini struct {
	ConfigAICommon

	// NoEnforceSchema skips `instr.WithMode(instr.ModeJSONSchema)` for local models
	NoEnforceSchema bool
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
	// log prompts as log level debug
	Prompts bool
	// mark log errs as ErrLog multi state
	FwdErrState bool
	// mark log warning as WarnLog multi state
	FwdWarnState bool
	// mach log level 0-5
	MachLevel am.LogLevel
	// print machine log
	MachPrint bool
	// agent log level
	Level slog.Level
	// enable instructor-go logging for AI requests
	AI bool
	// instructor-go log level TODO
	// AILevel slog.Level
}

type ConfigAgentHistory struct {
	Backend string
	// TODO BackendParsed enum
	Max int
}

type ConfigWeb struct {
	Root *Config `kdl:"-" json:"-"`

	// Main HTTP server
	Addr string
	// Start a DBPort web UI on http://localhost:{DBPort[0-2]}
	DBPort int
	// Start a log web UI on http://localhost:{LogPort}
	// TODO inline into ConfigWeb.Addr under /logs, remove config
	LogPort int
}

type ConfigTUI struct {
	Root *Config `kdl:"-" json:"-"`

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
	Root *Config `kdl:"-" json:"-"`

	// Display extra info about these stories in the machine log
	Story []string
	// Run the mock scenario
	Mock bool
	// Start pprof on addr
	ProfilerAddr string
	// Enable misc debugging modes (am-relay, browser RPC, TRACE logs)
	Verbose bool
	// Enable SQL query logging
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
	// Enable the embedded debugger web UI (livepage) on localhost:{DBGAddr}+1
	DBGEmbedWeb bool
	// Start a web REPL on http://localhost:{REPLWeb}
	// TODO inline into ConfigWeb.Addr under /repl, remove REPLWeb cfg
	REPLWeb int
	// forward dbg to these addresses
	DBGAddrsFwd []string
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
				Level:        slog.LevelInfo,
				FwdErrState:  true,
				FwdWarnState: true,
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
			// Model:   openai.GPT4o,
			Model: "",
			// TODO enum
			API:             "openai",
			FallbackTimeout: time.Hour,
		},
	}
}

func ConfigDefaultAIGemini() ConfigAIGemini {
	return ConfigAIGemini{
		ConfigAICommon: ConfigAICommon{
			Retries: 3,
			// TODO link from genai pkg
			Model: "",
			// TODO enum
			API:             "gemini",
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

func (c *ConfigDebug) DbgAddrListen() (dbgAddr string, err error) {
	dbgAddr = c.DBGAddr
	if dbgAddr == "1" {
		dbgAddr = dbg.DbgAddr
	}
	host, port, err := net.SplitHostPort(dbgAddr)
	if err != nil {
		return "", err
	}
	if c.Root.DockerMode {
		host = "0.0.0.0"
	}

	return host + ":" + port, nil
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

func (c *ConfigWeb) DBAddrsListen() (base, agent, mach string) {
	if c.DBPort == -1 {
		return "", "", ""
	}

	host := "localhost"
	if c.Root.ServerMode {
		host = "0.0.0.0"
	}

	port := c.DBPort
	return host + ":" + strconv.Itoa(port),
		host + ":" + strconv.Itoa(port+1),
		host + ":" + strconv.Itoa(port+2)
}

func (c *ConfigWeb) LogAddr() string {
	// TODO support windows
	if runtime.GOOS == "windows" {
		return ""
	}
	if c.LogPort == -1 {
		return ""
	}

	host := "localhost:"
	if c.Root.DockerMode {
		host = "0.0.0.0:"
	}

	return host + strconv.Itoa(c.LogPort)
}

func (c *ConfigWeb) REPLAddr() string {
	// TODO support windows
	if runtime.GOOS == "windows" {
		return ""
	}
	if !c.Root.Debug.REPL || c.Root.Debug.REPLWeb == -1 {
		return ""
	}

	host := "localhost:"
	if c.Root.DockerMode {
		host = "0.0.0.0:"
	}

	return host + strconv.Itoa(c.Root.Debug.REPLWeb)
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

func (c *ConfigWeb) AddrListen() string {
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return ""
	}
	if c.Root.ServerMode {
		host = "0.0.0.0"
	}

	return host + ":" + port
}

func (c *ConfigWeb) AddrAgent() string {
	host, port, err := net.SplitHostPort(c.AddrListen())
	if err != nil {
		return ""
	}
	webPort, err := strconv.Atoi(port)
	if err != nil {
		return ""
	}
	return host + ":" + strconv.Itoa(webPort+1)
}

func (c *ConfigWeb) DashURL() string {
	return "http://" + c.Addr
}

func (c *ConfigWeb) AgentURL() string {
	return "http://" + c.Addr + "/agent"
}

func (c Config) DotEnv() string {
	// link .Root fields, required by AddrListen/AddrAgent
	c = c.Clone()

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
	writeEnv("ID", c.Agent.ID)
	writeEnv("LABEL", c.Agent.Label)
	writeEnv("DIR", c.Agent.Dir)
	writeEnv("INTRO", c.Agent.Intro)
	writeEnv("INTRO_DASH", c.Agent.IntroDash)
	writeEnv("FOOTER", c.Agent.Footer)

	writeEnv("LOG_FILE", c.Agent.Log.File)
	writeEnv("LOG_PROMPTS", c.Agent.Log.Prompts)
	// TODO log fwd
	writeEnv("LOG_MACH_LEVEL", c.Agent.Log.MachLevel)
	writeEnv("LOG_MACH_PRINT", c.Agent.Log.MachPrint)

	writeEnv("HISTORY_BACKEND", c.Agent.History.Backend)
	writeEnv("HISTORY_MAX", c.Agent.History.Max)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# WEB CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("WEB_ADDR", c.Web.Addr)
	writeEnv("WEB_DB_PORT", c.Web.DBPort)
	writeEnv("WEB_LOG_PORT", c.Web.LogPort)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# TUI CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("TUI_HOST", c.TUI.Host)
	writeEnv("TUI_PORT_SSH", c.TUI.PortSSH)
	writeEnv("TUI_WEB_HOST", c.TUI.WebHost)
	writeEnv("TUI_PORT_WEB", c.TUI.PortWeb)
	writeEnv("TUI_CLOCK_RANGE", c.TUI.ClockRange)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# TOOLS CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("TOOLS_SEARXNG_PORT", c.Tools.SearXNG.Port)
	writeEnv("TOOLS_SEARXNG_URL", c.Tools.SearXNG.URL)

	sb.WriteString("\n# ==========================================\n")
	sb.WriteString("# DEBUG CONFIGURATION\n")
	sb.WriteString("# ==========================================\n")
	writeEnv("DEBUG_STORY", c.Debug.Story)
	writeEnv("DEBUG_MOCK", c.Debug.Mock)
	writeEnv("DEBUG_PROFILER_ADDR", c.Debug.ProfilerAddr)
	writeEnv("DEBUG_VERBOSE", c.Debug.Verbose)
	writeEnv("DEBUG_VERBOSE_SQL", c.Debug.VerboseSQL)
	writeEnv("DEBUG_REPL", c.Debug.REPL)
	writeEnv("DEBUG_DBG_ADDR", c.Debug.DBGAddr)
	writeEnv("DEBUG_DBG_EMBED", c.Debug.DBGEmbed)
	writeEnv("DEBUG_DBG_EMBED_WEB", c.Debug.DBGEmbedWeb)
	writeEnv("DEBUG_REPL_WEB", c.Debug.REPLWeb)

	return sb.String()
}

// ///// ///// /////

// ///// KDL FORMAT

// ///// ///// /////

// kdlTplMarker starts the DEFAULT VALUES section of an agent's KDL template.
const kdlTplMarker = "// ///// DEFAULT VALUES"

// kdlTplHeader is the header of the DEFAULT VALUES section.
const kdlTplHeader = "// /////\n" + kdlTplMarker + "\n// /////\n\n"

// KdlTplWrite replaces the DEFAULT VALUES section of a KDL template file with a
// commented-out dump of cfg, keeping the rest of the file intact. The defaults
// come only from cfg, eg [ConfigDefault]. The file is created from fallback if
// missing.
func KdlTplWrite(path string, fallback []byte, cfg any) error {
	data, err := os.ReadFile(path)
	exists := err == nil
	if !exists {
		data = fallback
	}

	// keep everything above the marker
	head, _, found := strings.Cut(string(data), kdlTplMarker)
	if found {
		// drop the trailing section separator, it's part of the header
		head = strings.TrimSuffix(strings.TrimRight(head, " \t\n"), "// /////")
	}
	out := strings.TrimRight(head, " \t\n") + "\n\n" + kdlTplHeader +
		KdlCfgDump(cfg)

	if exists && out == string(data) {
		return nil
	}

	return os.WriteFile(path, []byte(out), 0o644)
}

// KdlFormat parses a raw KDL string and returns it formatted with proper
// newlines, inherited indentation, and names resolved from the Config struct.
func KdlFormat(input []byte, cfg any) string {
	f := &kdlFmt{nameMap: kdlBuildNameMap(cfg)}

	doc, err := kdl.Parse(bytes.NewReader(input))
	if err != nil {
		return fmt.Sprintf("kdlfmt: parse error: %v\n", err)
	}

	var b strings.Builder
	for _, n := range doc.Nodes {
		f.node(&b, n, 0)
	}
	return b.String()
}

// KdlCfgDump returns cfg as a KDL document in a /* */ comment, with godoc
// comments above each field.
func KdlCfgDump(cfg any) string {
	var b strings.Builder
	b.WriteString("/*\n")
	kdlDumpStruct(&b, reflect.ValueOf(cfg), 0)
	b.WriteString("*/\n")
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

// kdlFmt renders a parsed KDL document.
type kdlFmt struct {
	nameMap map[string]string
}

func (f *kdlFmt) node(b *strings.Builder, n *document.Node, depth int) {
	indent := strings.Repeat("  ", depth)
	name := kdlResolveName(n.Name.Value.(string), f.nameMap)

	children := n.Children
	props := n.Properties.Unordered()
	hasBraces := len(children) > 0 || len(props) > 0

	if !hasBraces {
		b.WriteString(indent + name)
		for _, arg := range n.Arguments {
			b.WriteString(" " + kdlFormatValue(arg))
		}
		b.WriteString("\n")
		return
	}

	b.WriteString(indent + name + " {\n")

	childIndent := strings.Repeat("  ", depth+1)
	for k, v := range props {
		// if kdlIsZeroValue(v) {
		// 	continue
		// }
		b.WriteString(childIndent + kdlResolveName(k, f.nameMap))
		if v.Value != nil {
			b.WriteString(" " + kdlFormatValue(v))
		}
		b.WriteString("\n")
	}

	// output arguments (positional values without keys)
	for _, arg := range n.Arguments {
		b.WriteString(childIndent + kdlFormatValue(arg) + "\n")
	}

	for _, child := range children {
		f.node(b, child, depth+1)
	}

	b.WriteString(indent + "}\n")
}

// kdlTagHas reports whether a kdl tag contains the given attribute, eg
// `kdl:"OpenAI,omitempty"`.
func kdlTagHas(tag, attr string) bool {
	parts := strings.Split(tag, ",")
	for _, p := range parts[1:] {
		if p == attr {
			return true
		}
	}
	return false
}

// ///// KDL DUMP

// kdlLine writes an indented KDL line.
func kdlLine(b *strings.Builder, depth int, s string) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(s)
	b.WriteString("\n")
}

// kdlDumpStruct writes all exported fields of a struct as KDL nodes, with their
// godoc.
func kdlDumpStruct(b *strings.Builder, val reflect.Value, depth int) {
	for val.Kind() == reflect.Ptr || val.Kind() == reflect.Interface {
		if val.IsNil() {
			return
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	t := val.Type()
	docs := kdlTypeDocs(t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("kdl")
		if !f.IsExported() || tag == "-" {
			continue
		}

		// embedded structs are inlined
		if f.Anonymous {
			kdlDumpStruct(b, val.Field(i), depth)
			continue
		}

		fv := val.Field(i)
		if fv.IsZero() && kdlTagHas(tag, "omitempty") {
			continue
		}
		// no arguments means no node
		if (fv.Kind() == reflect.Slice || fv.Kind() == reflect.Array) &&
			fv.Len() == 0 {

			continue
		}

		name := f.Name
		if tagName := strings.SplitN(tag, ",", 2)[0]; tagName != "" {
			name = tagName
		}
		kdlDumpValue(b, name, fv, depth, docs[f.Name])
	}
}

// kdlDumpValue writes a single struct field as commented-out KDL, recursing
// into structs and struct slices.
func kdlDumpValue(
	b *strings.Builder, name string, val reflect.Value, depth int, doc string,
) {
	for val.Kind() == reflect.Ptr || val.Kind() == reflect.Interface {
		if val.IsNil() {
			return
		}
		val = val.Elem()
	}

	// godoc, as a comment for when the line gets uncommented
	for _, l := range strings.Split(doc, "\n") {
		// skip empty lines and TODO notes
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "TODO") {
			continue
		}
		kdlLine(b, depth, "// "+l)
	}

	switch val.Kind() {
	case reflect.Struct:
		kdlLine(b, depth, name+" {")
		kdlDumpStruct(b, val, depth+1)
		kdlLine(b, depth, "}")
	case reflect.Slice, reflect.Array:
		// slices of structs are repeated nodes, others are arguments
		if val.Type().Elem().Kind() == reflect.Struct {
			for i := 0; i < val.Len(); i++ {
				kdlDumpValue(b, name, val.Index(i), depth, doc)
				doc = ""
			}
			return
		}
		s := name
		for i := 0; i < val.Len(); i++ {
			s += " " + kdlFormatScalar(val.Index(i))
		}
		kdlLine(b, depth, s)
	default:
		kdlLine(b, depth, name+" "+kdlFormatScalar(val))
	}
}

// kdlFormatScalar formats a scalar KDL value, including durations and text
// marshalers (eg [slog.Level]).
func kdlFormatScalar(val reflect.Value) string {
	if val.Type() == reflect.TypeOf(time.Duration(0)) {
		d := time.Duration(val.Int())
		if d != 0 && d%time.Hour == 0 {
			return strconv.Quote(strconv.FormatInt(int64(d/time.Hour), 10) + "h")
		}
		return strconv.Quote(d.String())
	}
	if tm, ok := val.Interface().(encoding.TextMarshaler); ok {
		if txt, err := tm.MarshalText(); err == nil {
			return strconv.Quote(string(txt))
		}
	}

	switch val.Kind() {
	case reflect.String:
		return strconv.Quote(val.String())
	case reflect.Bool:
		return strconv.FormatBool(val.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(val.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(val.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(val.Float(), 'f', -1, 64)
	default:
		return strconv.Quote(fmt.Sprint(val))
	}
}

// ///// GODOC

// kdlDocs caches parsed godoc comments per package dir.
var kdlDocs sync.Map

// kdlTypeDocs returns the godoc comments of t's exported fields, keyed by field
// name. It parses the declaring package's source and returns nil when not
// available (eg in prod builds).
func kdlTypeDocs(t reflect.Type) map[string]string {
	dir, ok := kdlPkgDir(t.PkgPath())
	if !ok {
		return nil
	}

	cached, ok := kdlDocs.Load(dir)
	if !ok {
		cached, _ = kdlDocs.LoadOrStore(dir, kdlParseDocs(dir))
	}

	return cached.(map[string]map[string]string)[t.Name()]
}

// kdlPkgDir resolves an import path to a source dir using the closest go.mod.
func kdlPkgDir(pkgPath string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if mod := kdlModulePath(data); mod != "" &&
				(pkgPath == mod || strings.HasPrefix(pkgPath, mod+"/")) {

				rel := strings.TrimPrefix(pkgPath, mod)
				return filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(rel, "/"))), true
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func kdlModulePath(goMod []byte) string {
	for _, line := range strings.Split(string(goMod), "\n") {
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// kdlParseDocs parses all non-test files of a package and returns struct field
// godoc comments as type name -> field name -> comment.
func kdlParseDocs(dir string) map[string]map[string]string {
	out := make(map[string]map[string]string)

	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return out
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}

				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}

					docs := make(map[string]string)
					for _, f := range st.Fields.List {
						if f.Doc == nil {
							continue
						}
						doc := strings.TrimSpace(f.Doc.Text())
						for _, name := range f.Names {
							docs[name.Name] = doc
						}
					}
					out[ts.Name.Name] = docs
				}
			}
		}
	}

	return out
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
	ID     string
	Title  string `jsonschema:"required"`
	Author string
	Link   string `jsonschema:"required"`
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

// feedItemID returns a stable Atom entry ID derived from the title and update
// date of an item.
func feedItemID(title, updated string) string {
	sum := sha256.Sum256([]byte(title + updated))
	return fmt.Sprintf("urn:sha256:%x", sum)
}

// indentLines prefixes every non-empty line of s with n spaces.
func indentLines(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = pad + line
		}
	}
	return strings.Join(lines, "\n")
}

// GenerateAtomFeed renders items as an Atom feed. url is the feed-level
// alternate link; it falls back to the legacy GitHub link when empty.
func GenerateAtomFeed(items []FeedItem, title, url string) string {
	now := time.Now().Format(time.RFC3339)
	if url == "" {
		url = "https://github.com"
	}

	var sb strings.Builder
	sb.WriteString(Sp(`
		<?xml version="1.0" encoding="utf-8"?>
		<feed xmlns="http://www.w3.org/2005/Atom">
		    <title>%s</title>
		    <link href="%s"/>
		    <updated>%s</updated>
		    <id>starred-repos-feed</id>
	`, escapeXML(title), escapeXML(url), now))

	for _, item := range items {
		id := item.ID
		if id == "" {
			id = feedItemID(item.Title, item.Date)
		}
		entry := Sp(`
			<entry>
			    <id>%s</id>
			    <title>%s</title>
			    <link href="%s"/>
			    <updated>%s</updated>
			    <author><name>%s</name></author>
			    <description>%s</description>
			</entry>
		`, escapeXML(id), escapeXML(item.Title), escapeXML(item.Link),
			escapeXML(item.Date), escapeXML(item.Author), escapeXML(item.Desc))

		sb.WriteString(indentLines(strings.TrimSuffix(entry, "\n"), 4))
		sb.WriteString("\n")
	}

	sb.WriteString("</feed>\n")
	return sb.String()
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
	// TODO SortTags for favoring certain models eg "slow"

	// required provider tags
	RequiredTags []string
	// disallowed provider tags
	NoTags []string
	// skip checking if state is active
	SkipState bool
	Reasoning string
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
		docs += "## " + doc.Title() + "\n\n" + strings.Join(doc.Parts(), "\n    ") + "\n\n"
	}
	for _, d := range p.docs {
		c := d.Parts()
		if len(c) == 0 {
			continue
		}
		docs += "## " + d.Title() + "\n\n" + strings.Join(d.Parts(), "\n    ") + "\n\n"
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
	c := core.NewConversation(sys)

	// max len
	limit := max(100, p.HistoryMsgLen)
	if l := len(p.Msgs); l > limit {
		p.Msgs = p.Msgs[l-limit:]
	}

	// inject msgs
	for _, msg := range p.Msgs {
		c.AddMessage(msg.From, msg.Content)
	}

	return c, sys
}

func (p *Prompt[P, R]) HistClean() {
	p.Msgs = nil
	p.lastClient = nil
}

// ///// ///// /////

// ///// DOCUMENT

// ///// ///// /////

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

var (
	ErrHistNil = errors.New("history is nil")
)

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

func NullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

type HasPromptUsage interface {
	GetPromptUsage() *PromptUsage
}

type PromptConfig struct {
	ModelTag     string
	ModelSortTag string
	Reasoning    string
	MaxTries     int
	Concurrency  int
}

func IsStory(state string) bool {
	return strings.HasPrefix(state, states.PrefixStory) && state != ss.StoryChanged &&
		!strings.HasPrefix(state, states.PrefixStoryDisable) && state != ss.StoryAction
}

type PromptUsage struct {
	Model              string
	API                string
	URL                string
	PromptTokens       int
	PromptTokensCached int
	CompletionTokens   int
	TotalTokens        int
	PromptChars        int
	HistoryChars       int
	Provider           string
}

func (p *PromptUsage) Compute() {
	p.TotalTokens = p.PromptTokens + p.PromptTokensCached + p.CompletionTokens
}
