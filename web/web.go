package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ampipe "github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	amrelay "github.com/pancsta/asyncmachine-go/tools/relay"
	ssr "github.com/pancsta/asyncmachine-go/tools/relay/states"
	amrelayt "github.com/pancsta/asyncmachine-go/tools/relay/types"
	tclive "github.com/pancsta/cview/tcell-livepage"
	"github.com/pancsta/sqliter-embed"

	"github.com/pancsta/secai/shared"
	"github.com/pancsta/secai/states"
	"github.com/pancsta/secai/web/browser"
)

var ss = states.AgentBaseStates

type Args struct {
	am.ArgsBase
}

func (Args) ArgsPrefix() string {
	return shared.APrefix
}

var (
	// ErrWeb is for [sabase.AgentBaseStatesDef.ErrWeb].
	ErrWeb = errors.New("web UI error")
	// ErrWebPTY is for [sabase.AgentBaseStatesDef.ErrWebPTY].
	ErrWebPTY = errors.New("web PTY UI error")
)

// PagesFn mounts the livepage UIs on the local mux and returns a disposer.
type PagesFn func(ctx context.Context, model browser.Model, mux *http.ServeMux) (func(), error)

type Handlers struct {
	A  shared.AgentBaseAPI
	ID string

	// Pages optionally overrides the default livepage UIs.
	Pages PagesFn

	// Relay is the web UI websocket relay.
	Relay atomic.Pointer[amrelay.Relay]
	// ExtraTunnelMatchers are appended to the built-in tunnel matchers.
	ExtraTunnelMatchers []amrelayt.TunnelMatcher
	// ExtraDialMatchers are appended to the built-in dial matchers.
	ExtraDialMatchers []amrelayt.DialMatcher

	rpcMux *arpc.Mux
	relay  *amrelay.Relay

	// disposePages releases the livepage UIs on UIMode end.
	disposePages func()
}

// NewHandlers creates new web handlers.
func NewHandlers(a shared.AgentBaseAPI) *Handlers {
	return &Handlers{
		ID: "web.Handlers",
		A:  a,
	}
}

// NewMuxServer creates a new aRPC server on the web UI's mux, sharing the same
// source machine. Useful for extra clients (eg a chrome extension).
func (h *Handlers) NewMuxServer(
	ctx context.Context, id string, conn net.Conn,
) (*arpc.Server, error) {
	if h.rpcMux == nil {
		return nil, errors.New("web rpc mux is not initialized")
	}

	return h.rpcMux.NewServer(nil, id, conn)
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

var _ = ss.UIMode

func (h *Handlers) UIModeState(e *am.Event) {
	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.UIMode)
	cfg := h.A.ConfigBase()

	// web log
	if logAddr := cfg.Web.LogAddr(); logAddr != "" {
		mach.Fork(ctx, e, func() {
			title := "log / " + h.A.ConfigBase().Agent.Label
			args := []string{"log", "--tail", "--lvl-debug"}
			if cfg.File != "config.kdl" {
				args = append(args, "--config", cfg.File)
			}
			cmd := exec.CommandContext(ctx, shared.BinaryPath(true), args...)

			err := tclive.RunCmd(ctx, cmd, tclive.CmdOpts{
				Title:  title,
				Addr:   logAddr,
				Printf: mach.Log,
			})
			if ctx.Err() != nil {
				return // expired
			}
			AddErrPTY(e, mach, err)
		})
	}

	// check config
	if cfg.Web.Addr == "" {
		return
	}

	// HTTP server with the server-rendered UIs

	mach.Fork(ctx, e, func() {
		AddErr(e, mach,
			h.StartHTTP(e))
	})

	// RPC Muxer TODO extract to APIs

	// TODO ID gen
	mux, err := arpc.NewMux(ctx, cfg.Web.AddrAgent(), "server-"+mach.Id(), mach, &arpc.MuxOpts{
		Parent: mach,
		NewServerFn: func(mux *arpc.Mux, id string, conn net.Conn) (*arpc.Server, error) {
			srv, err := mux.NewDefaultServer(id)
			if err != nil {
				return nil, err
			}

			// instant push
			srv.PushInterval.Store(new(10 * time.Millisecond))
			srv.Conn = conn
			srv.Start(e)

			return srv, nil
		},
	})
	if err != nil {
		AddErr(e, mach, err, nil)
		return
	}

	// pipes
	if _, err := ampipe.BindReady(mux.Mach, mach, ss.WebRPCReady, ""); err != nil {
		AddErr(e, mach, err, nil)
		return
	}
	if _, err := arpc.BindMux(mux.Mach, mach, ss.WebConnected, ""); err != nil {
		AddErr(e, mach, err, nil)
		return
	}

	// save & start
	h.rpcMux = mux
	mux.Start(e)
}

func (h *Handlers) UIModeEnd(e *am.Event) {
	if h.disposePages != nil {
		h.disposePages()
		h.disposePages = nil
	}
	if h.rpcMux != nil {
		h.rpcMux.Stop(e, true)
	}

	// TODO dispose relay
}

var _ = ss.BaseDBReady

func (h *Handlers) BaseDBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	mach.Fork(mach.NewStateCtx(ss.BaseDBReady), e, func() {
		addr, _, _ := cfg.DBAddrsListen()
		err := sqliter.New(addr, h.A.DBBase())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

var _ = ss.DBReady

func (h *Handlers) DBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	// TODO move state to ss_secai
	mach.Fork(mach.NewStateCtx(ss.DBReady), e, func() {
		_, addr, _ := cfg.DBAddrsListen()
		err := sqliter.New(addr, h.A.AgentImpl().DBAgent())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

var _ = ss.HistoryDBReady

func (h *Handlers) HistoryDBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	mach.Fork(mach.NewStateCtx(ss.HistoryDBReady), e, func() {
		_, _, addr := cfg.DBAddrsListen()
		err := sqliter.New(addr, h.A.DBHistory())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

var _ = ss.REPL

// REPLState starts the web REPL over livepage
func (h *Handlers) REPLState(e *am.Event) {
	cfg := h.A.ConfigBase()
	if cfg.Debug.REPLWeb <= 0 {
		return
	}
	// the livepage REPL runs on a Unix PTY
	if runtime.GOOS == "windows" {
		h.A.LogWarn("web_repl", nil, "web REPL disabled on Windows")
		return
	}

	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.REPL)

	// TODO full config path
	mach.Fork(ctx, e, func() {
		mach.Log("web repl: http://localhost:%d/", cfg.Debug.REPLWeb)
		cmd := exec.CommandContext(ctx, shared.BinaryPath(true),
			"repl", "--config", cfg.File)
		// let the livepage parent kill its PTY child
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.WaitDelay = 3 * time.Second

		err := cmd.Run()
		if ctx.Err() != nil {
			return // expired
		}
		AddErrPTY(e, mach, err)
	})
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

// StartHTTP starts the web server with the server-rendered livepage UIs and the
// static assets. The websocket relay is kept for extra tunnel clients.
func (h *Handlers) StartHTTP(e *am.Event) error {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase()
	addr := cfg.Web.AddrListen()
	if addr == "" {
		return nil
	}

	ctx := mach.NewStateCtx(ss.UIMode)

	// websocket relay
	opts := amrelayt.CliArgs{
		Name:   mach.Id(),
		Debug:  cfg.Debug.DBGAddr != "" && cfg.Debug.Verbose,
		Parent: mach,
		Wasm: &amrelayt.ArgsWasm{
			ListenAddr:     addr,
			TunnelMatchers: h.ExtraTunnelMatchers,
			DialMatchers:   h.ExtraDialMatchers,
		},
		Output: mach.Log,
	}
	if cfg.Debug.REPL {
		opts.Wasm.ReplAddrDir = filepath.Join(cfg.Agent.Dir, "repl")
	}
	relay, err := amrelay.New(mach.Context(), opts)
	if err != nil {
		return err
	}

	// bind
	_, err = ampipe.Bind(relay.Mach, mach, ssr.RelayStates.HttpReady, ss.WebHTTPReady, "")
	if err != nil {
		return err
	}
	relay.Start(e)
	<-relay.Mach.When1(ssr.RelayStates.HttpReady, nil)

	h.relay = relay
	h.Relay.Store(relay)

	// livepage UIs bound to the local machine
	if h.Pages != nil {
		dispose, err := h.Pages(ctx, h.A.AgentImpl(), relay.HttpMux)
		if err != nil {
			return err
		}
		h.disposePages = dispose
	} else {
		pages, err := browser.NewPages(ctx, h.A.AgentImpl(), browser.Opts{})
		if err != nil {
			return err
		}
		pages.Mount(relay.HttpMux)
		h.disposePages = pages.Dispose
	}

	// static assets
	if webFs := h.A.Store().Web; webFs != nil {
		sub, err := fs.Sub(webFs, "web")
		if err != nil {
			sub = webFs
		}
		relay.HttpMux.Handle("GET /web/", http.StripPrefix("/web/", http.FileServerFS(sub)))
	}

	h.A.Log("web UI listening", "addr", addr,
		"dash", cfg.Web.DashURL(), "agent", cfg.Web.AgentURL())

	return nil
}

// ///// ///// /////

// ///// MISC

// ///// ///// /////

// error mutations TODO add to /docs/manual as a reference

// AddErr adds [ErrWeb].
func AddErr(
	event *am.Event, mach *am.Machine, err error, args ...am.A,
) am.Result {
	if err == nil {
		return am.Executed
	}
	err = fmt.Errorf("%w: %w", ErrWeb, err)

	return mach.EvAddErrState(event, ss.ErrWeb, err, shared.OptArgs(args))
}

// AddErrPTY adds [ErrWebPTY].
func AddErrPTY(
	event *am.Event, mach *am.Machine, err error, args ...am.A,
) am.Result {
	if err == nil {
		return am.Executed
	}
	err = fmt.Errorf("%w: %w", ErrWebPTY, err)

	return mach.EvAddErrState(event, ss.ErrWebPTY, err, shared.OptArgs(args))
}
