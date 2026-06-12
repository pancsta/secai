//go:build js && wasm

package main

import (
	// arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"

	"github.com/pancsta/secai/web/browser"
)

func main() {
	// debug RPC components
	// arpc.EnableDebuggingRpc(false)
	browser.Cmd()
}
