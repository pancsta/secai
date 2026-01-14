package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"dario.cat/mergo"
	"github.com/joho/godotenv"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	amtele "github.com/pancsta/asyncmachine-go/pkg/telemetry"
	"github.com/sblinch/kdl-go"

	"github.com/pancsta/secai/examples/cook"
	"github.com/pancsta/secai/shared"
)

func init() {
	if os.Getenv(shared.EnvNoDotEnv) == "" {
		godotenv.Load()
	}
}

// TODO cmds:
//  - env (gens .env content from config)
//  - repl (starts a REPL)

func main() {
	ctx := context.Background()
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}

	// config TODO param
	cfgFile := "config.kdl"
	if v := os.Getenv(shared.EnvConfig); v != "" {
		cfgFile = v
	}
	cfgData, err := os.ReadFile(cfgFile)
	if err != nil {
		fmt.Printf("error reading config file: %v\n", err)
		os.Exit(1)
	}
	var cfgUser cook.Config
	if err := kdl.Unmarshal(cfgData, &cfgUser); err != nil {
		panic(err)
	}
	cfg := cook.ConfigDefault()
	if err := mergo.Merge(&cfg, cfgUser, mergo.WithOverride); err != nil {
		panic(err)
	}

	// set env
	if cfg.Debug.DBGAddr != "" {
		os.Setenv(amtele.EnvAmDbgAddr, cfg.Debug.DBGAddr)
		os.Setenv(am.EnvAmLog, "3")
		os.Setenv(amhelp.EnvAmLogFull, "1")
	}

	// init
	a, err := cook.NewCook(ctx, &cfg)
	if err != nil {
		panic(err)
	}

	// REPL info
	repl := "\n"
	if cfg.Debug.REPL {
		repl = fmt.Sprintf("\nREPL:\n$ ./arpc --dir %s\n", cfg.Agent.Dir)
	}

	// splash
	shared.P(`
		%s %s
	
		TUI:
		$ ssh %s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no
		
		Log:
		$ tail -f %s -n 100 | fblog -d -x msg -x time -x level
		%s
		https://ai-gents.work
	`, cfg.Agent.Label, version, cfg.TUI.Host, cfg.TUI.Port, cfg.Agent.Log.File, repl)

	a.Start()
	<-a.Mach().WhenDisposed()
	print("bye")
}
