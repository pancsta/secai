package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"dario.cat/mergo"
	"github.com/joho/godotenv"
	"github.com/sblinch/kdl-go"

	"github.com/pancsta/secai/examples/cook"
	"github.com/pancsta/secai/shared"
)

func init() {
	if os.Getenv("SECAI_NO_DOTENV") == "" {
		godotenv.Load()
	}
}

func main() {
	ctx := context.Background()
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}

	// config TODO param
	cfgFile := "cook.kdl"
	if v := os.Getenv(shared.EnvConfig); v != "" {
		cfgFile = v
	}
	cfgData, err := os.ReadFile(cfgFile)
	var cfgUser cook.Config
	if err := kdl.Unmarshal(cfgData, &cfgUser); err != nil {
		panic(err)
	}
	cfg := cook.ConfigDefault()
	if err := mergo.Merge(&cfg, cfgUser, mergo.WithOverride); err != nil {
		panic(err)
	}

	// init
	a, err := cook.NewCook(ctx, &cfg)
	if err != nil {
		panic(err)
	}

	// REPL info
	repl := "\n"
	if v := os.Getenv("AM_REPL_ADDR"); v != "" {
		dir := "."
		if v := os.Getenv("AM_REPL_DIR"); v != "" {
			dir = v
		}
		repl = fmt.Sprintf("\nREPL:\n$ arpc --dir %s\n", dir)
	}

	// splash
	shared.P(`
		%s %s
	
		TUI:
		$ ssh %s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no
		
		Log:
		$ tail -f %s | fblog -d -x msg -x time -x level
		%s
		https://ai-gents.work
	
	`, cfg.Agent.Label, version, cfg.TUI.Host, cfg.TUI.Port, cfg.Agent.Log.File, repl)

	a.Start()
	<-a.Mach().WhenDisposed()
	print("bye")
}
