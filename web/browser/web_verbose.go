//go:build verbose

package browser

import (
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
)

func init() {
	arpc.EnableDebuggingRpc(false)
}
