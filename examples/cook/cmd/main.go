package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/alexflint/go-arg"
	"github.com/joho/godotenv"

	"github.com/pancsta/secai/examples/cook"
	"github.com/pancsta/secai/shared"
)

var id = "cook"

//go:embed layout.kdl
var configFS embed.FS

func init() {
	if os.Getenv("SECAI_NO_DOTENV") == "" {
		// TODO load agent.env?
		godotenv.Load()
	}

	// TODO config
	if os.Getenv("SECAI_DIR") == "" {
		os.Setenv("SECAI_DIR", "tmp")
	}
}

func main() {

	// create a desktop layout config
	if len(os.Args) > 1 && os.Args[1] == "desktop-layout" {
		layout, err := configFS.ReadFile("layout.kdl")
		if err != nil {
			panic(err)
		}

		// fixed-name tmp file
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, "secai-"+id+"-layout.kdl")
		err = os.WriteFile(tmpFile, layout, 0644)
		if err != nil {
			panic(err)
		}
		fmt.Print(tmpFile)
		return
	}

	// regular start
	var cfg = cook.Config{}
	arg.MustParse(&cfg)

	a, err := cook.NewCook(context.Background(), cfg)
	if err != nil {
		panic(err)
	}

	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		version = info.Main.Version
	}

	// show the list of entry points TODO config
	host := os.Getenv("SECAI_TUI_HOST")
	if host == "" {
		host = "localhost"
	}
	label := os.Getenv("SECAI_LABEL")
	if label == "" {
		label = id
	}
	shared.P(`
		%s %s
	
		TUI Chat:
		$ ssh chat@%s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no
	
		TUI Stories:
		$ ssh stories@%s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no
	
		TUI Clock:
		$ ssh clock@%s -p %d -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no
	
		TUI Desktop:
		$ bash <(curl -L https://zellij.dev/launch) --layout $(./%s desktop-layout) attach secai-%s --create
	
		https://ai-gents.work
	
	`, label, version, host, cfg.TUIPort, host, cfg.TUIPort, host, cfg.TUIPort, id, id)

	a.Start()
	<-a.Mach().WhenDisposed()
	print("bye")
}
