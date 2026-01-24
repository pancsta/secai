package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dario.cat/mergo"
	"github.com/alexflint/go-arg"
	"github.com/joho/godotenv"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	"github.com/pancsta/asyncmachine-go/tools/repl"
	"github.com/pancsta/fblog-go"
	"github.com/sblinch/kdl-go"

	"github.com/pancsta/secai/examples/cook"
	"github.com/pancsta/secai/shared"
)

func init() {
	if os.Getenv(shared.EnvNoDotEnv) == "" {
		godotenv.Load()
	}
}

type CLI struct {
	Config    string     `arg:"-c,--config,env:SECAI_CONFIG" help:"Path to the config file" default:"config.kdl"`
	Browser   bool       `arg:"-b,--browser" help:"Open the dashboard in the default browser" default:"true"`
	REPL      *REPL      `arg:"subcommand:repl" help:"Start a REPL"`
	Log       *Log       `arg:"subcommand:log" help:"Show the agent's log"`
	Env       *Env       `arg:"subcommand:env" help:"Generate a dotenv file"`
	GenConfig *GenConfig `arg:"subcommand:gen-config" help:"Generate a default config file into --config"`
}

type REPL struct{}

type Log struct {
	Tail  bool `arg:"-t,--tail" help:"Tail the log (keep streaming)."`
	Lines int  `arg:"-n,--lines" help:"Number of lines to show." default:"250"`
}

type Env struct {
	Output string `arg:"-o,--output" help:"Output filename." default:"config.env"`
}

type GenConfig struct{}

var cli CLI

func main() {
	p := arg.MustParse(&cli)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// GEN CONFIG
	if cli.GenConfig != nil {
		err := os.WriteFile(cli.Config, cook.ConfigTpl, 0644)
		if err != nil {
			p.Fail(err.Error())
		}
		fmt.Printf("Generated config file: %s\n", cli.Config)
		os.Exit(0)
	}

	// create template config
	var cfg cook.Config
	cfgData, err := os.ReadFile(cli.Config)
	if err != nil && os.IsNotExist(err) {
		fmt.Printf("Creating config file: %s\n", cli.Config)
		cfgData = cook.ConfigTpl
		err = os.WriteFile(cli.Config, cfgData, 0644)
		if err != nil {
			p.Fail(err.Error())
		}
	}

	// read config
	cfg = cook.ConfigDefault()
	var cfgUser cook.Config
	if err := kdl.Unmarshal(cfgData, &cfgUser); err != nil {
		p.Fail(fmt.Sprintf("config format: %s", err.Error()))
	}
	if err := mergo.Merge(&cfg, cfgUser, mergo.WithOverride); err != nil {
		p.Fail(err.Error())
	}
	cfg.File = cli.Config

	// clean up
	matches, err := filepath.Glob(filepath.Join(cfg.Agent.Dir, "repl-*.addr"))
	if err == nil {
		for _, file := range matches {
			_ = os.Remove(file)
		}
	}

	// REPL
	if cli.REPL != nil {
		err := cmdREPL(ctx, cfg)
		if err != nil {
			err := p.FailSubcommand(fmt.Sprintf(
				"ERROR: running REPL: %v\n", err), "repl")
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		return

		// LOG
	} else if cli.Log != nil {
		err := cmdLog(ctx, cfg)
		if err != nil {
			err := p.FailSubcommand(fmt.Sprintf(
				"ERROR: showing logs: %v\n", err), "log")
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		return

		// Env
	} else if cli.Env != nil {
		err := cmdEnv(ctx, cfg)
		if err != nil {
			err := p.FailSubcommand(fmt.Sprintf(
				"ERROR: generating env file: %v\n", err), "env")
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// BOT

	// init
	a, err := cook.NewCook(ctx, &cfg)
	if err != nil {
		p.Fail(err.Error())
	}

	// welcome
	fmt.Print(a.Splash())

	// start
	a.Start()
	if cli.Browser {
		shared.OpenURL(fmt.Sprintf("http://%s", cfg.Web.Addr))
	}
	<-a.Mach().WhenDisposed()
	print("bye\n")
}

// -----

// REPL

// -----

func cmdREPL(ctx context.Context, cfg cook.Config) error {
	r, err := repl.New(ctx, "repl-"+cfg.Agent.ID)
	if err != nil {
		return err
	}
	rootCmd := repl.NewRootCommand(r, nil, nil)
	args := []string{"--dir", cfg.Agent.Dir}
	if cfg.Debug.DBGAddr != "" {
		args = append(args, "--am-dbg-addr", cfg.Debug.DBGAddr)
	}
	rootCmd.SetArgs(args)
	r.Cmd = rootCmd

	// start cobra
	err = rootCmd.Execute()
	if err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}

	// wait
	<-ctx.Done()
	amhelp.Dispose(r.Mach)

	return nil
}

func cmdLog(ctx context.Context, cfg cook.Config) error {
	f, err := os.Open(shared.ConfigLogPath(cfg.Agent))
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// find the offset and rewind
	offset, err := findStartOffset(f, 100)
	if err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	// fblog
	config := fblog.NewDefaultConfig()
	template, _ := fblog.FblogHandlebarRegistry(config.MainLineFormat, config.AdditionalValueFormat)
	logSettings := fblog.NewDefaultLogSettings()
	logSettings.DumpAll = true // exclude nothing
	logSettings.ExcludedValues = []string{"msg", "time", "level"}

	// read
	reader := bufio.NewReader(f)
	for {
		// Check if the context was cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if !cli.Log.Tail {
					return nil
				}
				// pause and tail
				time.Sleep(200 * time.Millisecond)
				continue
			}
			return fmt.Errorf("error reading file: %w", err)
		}

		// pass to fblog
		err = fblog.ProcessInputLine(&logSettings, line, "", template, os.Stdout)
		if err != nil {
			return err
		}
	}
}

// findStartOffset reads a file backwards in chunks to find the Nth newline from the end
func findStartOffset(f *os.File, linesToRead int) (int64, error) {
	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}

	fileSize := stat.Size()
	var offset = fileSize
	var newlinesFound int
	buf := make([]byte, 4096) // Read in 4KB chunks

	for offset > 0 && newlinesFound <= linesToRead {
		toRead := int64(len(buf))
		if offset < toRead {
			toRead = offset
		}
		offset -= toRead

		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := f.Read(buf[:toRead]); err != nil {
			return 0, err
		}

		// Scan backwards through our buffer
		for i := int(toRead) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				newlinesFound++
				// +1 to skip the newline character itself
				if newlinesFound == linesToRead+1 {
					return offset + int64(i) + 1, nil
				}
			}
		}
	}

	return 0, nil
}

// -----

// ENV

// -----

func cmdEnv(ctx context.Context, cfg cook.Config) error {
	err := os.WriteFile(cli.Env.Output, []byte(cfg.Config.DotEnv()), 0644)
	if err != nil {
		return err
	}

	return nil
}
