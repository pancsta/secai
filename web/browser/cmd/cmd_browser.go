//go:build js && wasm

package main

import (
	goapp "github.com/pancsta/go-app/pkg/app"

	"github.com/pancsta/secai/web/browser"
)

func main() {
	goapp.Route("/", goapp.NewZeroComponentFactory(&browser.Dashboard{}))
	goapp.Route("/agent", goapp.NewZeroComponentFactory(&browser.AgentUI{}))
	goapp.RunWhenOnBrowser()
}
