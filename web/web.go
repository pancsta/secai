package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ampipe "github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	ssdbg "github.com/pancsta/asyncmachine-go/tools/debugger/states"
	amrelay "github.com/pancsta/asyncmachine-go/tools/relay"
	ssr "github.com/pancsta/asyncmachine-go/tools/relay/states"
	amrelayt "github.com/pancsta/asyncmachine-go/tools/relay/types"
	goapp "github.com/pancsta/go-app/pkg/app"
	"github.com/pancsta/gotty/backend/localcommand"
	gotty "github.com/pancsta/gotty/server"
	"github.com/pancsta/gotty/utils"
	"github.com/pancsta/sqliter-embed"
	"github.com/teivah/onecontext"

	"github.com/pancsta/secai/shared"
	sabase "github.com/pancsta/secai/states"
	ssb "github.com/pancsta/secai/web/browser/states"
	"github.com/pancsta/secai/web/types"
)

//go:embed browser/browser.js
var clockmojiJS []byte

var ss = sabase.AgentBaseStates
var ssB = ssb.PageStates
var Pass = shared.Pass
var PassRpc = types.PassRpc

type A = types.A
type ABase = shared.A

var (
	// ErrWeb is for [sabase.AgentBaseStatesDef.ErrWeb].
	ErrWeb = errors.New("web UI error")
	// ErrWebPTY is for [sabase.AgentBaseStatesDef.ErrWebPTY].
	ErrWebPTY = errors.New("web PTY UI error")
)

type Handlers struct {
	A shared.AgentBaseAPI

	rpcDash        *arpc.Server
	dashboards     []*arpc.Client
	agentUIs       []*arpc.Client
	relay          *amrelay.Relay
	nextDashNum    int
	nextAgentUINum int
	rpcUI          *arpc.Server
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

func (h *Handlers) WebSSHReadyEnter(e *am.Event) bool {
	return h.A.ConfigBase().TUI.PortWeb != -1
}

func (h *Handlers) WebSSHReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase()
	ctx := mach.NewStateCtx(ss.WebSSHReady)
	webPort := strconv.Itoa(cfg.TUI.PortWeb)
	sshPort := strconv.Itoa(cfg.TUI.PortSSH)

	// listen for client disconns
	clientGoneCh := make(chan string, 1)
	mach.Go(ctx, func() {
		defer close(clientGoneCh)
		for {
			select {
			case addr, ok := <-clientGoneCh:
				if !ok {
					return
				}
				mach.EvAdd1(e, ss.SSHDisconn, Pass(&ABase{
					Addr: addr,
				}))
			case <-ctx.Done():
			}
		}
	})

	mach.Fork(ctx, e, func() {
		title := h.A.ConfigBase().Agent.Label + " (TUI)"
		AddErrPTY(e, mach,
			h.ExposeSSH(ctx, e, title, webPort, sshPort, clientGoneCh))
	})
}

func (h *Handlers) UIModeState(e *am.Event) {
	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.UIMode)
	cfg := h.A.ConfigBase()

	// web log
	if cfg.Web.LogPort != -1 {
		mach.Fork(ctx, e, func() {
			title := "log / " + h.A.ConfigBase().Agent.Label
			AddErrPTY(e, mach,
				h.ExposeCmd(ctx, e, title, strconv.Itoa(cfg.Web.LogPort), shared.BinaryPath(cfg),
					[]string{"log", "--tail", "--config", cfg.File}, nil))
		})
	}

	// check config
	if cfg.Web.Addr == "" {
		return
	}

	// HTTP server

	mach.Fork(ctx, e, func() {
		AddErr(e, mach,
			h.StartHTTP(e))
	})

	// RPC Servers TODO keep one mux when relay dialing lands

	// dashboard

	srv, err := arpc.NewServer(ctx, cfg.Web.AgentWSAddrDash(), "server-dash-"+mach.Id(), mach, &arpc.ServerOpts{
		Parent:    mach,
		WebSocket: true,
	})
	if err != nil {
		AddErr(e, mach, err, nil)
		return
	}
	err = arpc.BindServer(srv.Mach, mach, ss.WebRPCReady, ss.WebConnected)
	if err != nil {
		AddErr(e, mach, err, nil)
		return
	}
	srv.PushInterval.Store(new(10 * time.Millisecond))

	// save & start
	h.rpcDash = srv
	srv.Start(e)

	// remote UI

	srv, err = arpc.NewServer(ctx, cfg.Web.AgentWSAddrRemoteUI(), "server-agentui-"+mach.Id(), mach, &arpc.ServerOpts{
		Parent:    mach,
		WebSocket: true,
	})
	if err != nil {
		AddErr(e, mach, err, nil)
		return
	}
	err = arpc.BindServer(srv.Mach, mach, ss.WebRPCReady, ss.WebConnected)
	if err != nil {
		AddErr(e, mach, err, nil)
		return
	}
	srv.PushInterval.Store(new(10 * time.Millisecond))

	// save & start
	h.rpcUI = srv
	srv.Start(e)
}

func (h *Handlers) UIModeEnd(e *am.Event) {
	for _, b := range h.dashboards {
		_ = b.Stop(nil, e, true)
	}
	h.rpcDash.Stop(e, true)

	// TODO dispose agent UIs

	// TODO dispose relay
}

func (h *Handlers) BaseDBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	mach.Fork(mach.NewStateCtx(ss.BaseDBReady), e, func() {
		addr, _, _ := shared.ConfigWebDBAddrs(cfg)
		err := sqliter.New(addr, h.A.DBBase())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

func (h *Handlers) DBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	// TODO move state to ss_secai
	mach.Fork(mach.NewStateCtx("DBReady"), e, func() {
		_, addr, _ := shared.ConfigWebDBAddrs(cfg)
		err := sqliter.New(addr, h.A.AgentImpl().DBAgent())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

func (h *Handlers) HistoryDBReadyState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Web

	// TODO check addr
	if cfg.DBPort == -1 {
		return
	}

	mach.Fork(mach.NewStateCtx(ss.HistoryDBReady), e, func() {
		_, _, addr := shared.ConfigWebDBAddrs(cfg)
		err := sqliter.New(addr, h.A.DBHistory())
		if err != nil {
			AddErr(e, mach, err, nil)
			return
		}
	})
}

func (h *Handlers) WebRPCReadyEnter(e *am.Event) bool {
	ready := ssrpc.ServerStates.RpcReady
	return h.rpcUI.Mach.Is1(ready) && h.rpcDash.Mach.Is1(ready)
}

func (h *Handlers) WebConnectedExit(e *am.Event) bool {
	ready := ssrpc.ServerStates.HandshakeDone
	return h.rpcUI.Mach.Not1(ready) && h.rpcDash.Mach.Not1(ready)
}

func (h *Handlers) RemoteDashReadyState(e *am.Event) {
	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.UIMode)

	srcId := e.Mutation().Source.MachId
	var b *arpc.Client
	for _, b = range h.dashboards {
		if b.Mach.Id() == srcId {
			break
		}
	}
	if b == nil {
		AddErr(e, mach, fmt.Errorf("browser %s not found", srcId), nil)
		return
	}

	// send data
	mach.Fork(ctx, e, func() {
		b.NetMach.EvAdd1(e, ssB.Data, PassRpc(&A{
			DataDash: &types.DataDashboard{
				Splash: h.A.AgentImpl().Splash(),
			},
		}))
	})
}

func (h *Handlers) RemoteUIReadyExit(e *am.Event) bool {
	// check if any still connected
	for _, b := range h.agentUIs {
		if b.Mach.Is1(ssrpc.ClientStates.Ready) {
			return false
		}
	}

	return true
}

func (h *Handlers) RemoteUIReadyState(e *am.Event) {
	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.UIMode)

	srcId := e.Mutation().Source.MachId
	var a *arpc.Client
	for _, a = range h.agentUIs {
		if a.Mach.Id() == srcId {
			break
		}
	}
	if a == nil {
		AddErr(e, mach, fmt.Errorf("browser %s not found", srcId), nil)
		return
	}

	clockDiff := h.A.Store().ClockDiff
	// TODO keep in store
	agent := h.A.AgentImpl()
	msgs := slices.Clone(agent.Msgs())

	// send data
	mach.Fork(ctx, e, func() {
		a.NetMach.EvAdd1(e, ssB.Data, PassRpc(&A{
			DataAgent: &types.DataAgent{
				Msgs:      msgs,
				ClockDiff: clockDiff,
				Stories:   agent.Stories(),
				Actions:   agent.Actions(),
			},
		}))
	})
}

// DebuggerState starts web am-dbg
func (h *Handlers) DebuggerState(e *am.Event) {
	mach := h.A.Mach()
	cfg := h.A.ConfigBase().Debug
	if cfg.DBGEmbedWeb == 0 {
		return
	}

	ctx := mach.NewStateCtx(ss.Debugger)
	_, _, sshAddr, err := shared.ConfigDbgAddrs(cfg)
	if err != nil {
		AddErrPTY(e, mach, err)
		return
	}
	sshAddr2 := strings.Split(sshAddr, ":")
	webPort := strconv.Itoa(cfg.DBGEmbedWeb)

	// listen for client disconns
	clientGoneCh := make(chan string, 1)
	mach.Go(ctx, func() {
		defer close(clientGoneCh)
		for {
			select {
			case _, ok := <-clientGoneCh:
				if !ok {
					return
				}
				h.A.AgentImpl().DBG().Mach.EvAdd1(e, ssdbg.SshDisconn, nil)
			case <-ctx.Done():
			}
		}
	})

	mach.Fork(ctx, e, func() {
		title := "am-dbg / " + h.A.ConfigBase().Agent.Label
		AddErrPTY(e, mach,
			h.ExposeSSH(ctx, e, title, webPort, sshAddr2[1], clientGoneCh))
	})
}

// REPLState starts web REPL
func (h *Handlers) REPLState(e *am.Event) {
	cfg := h.A.ConfigBase()
	if cfg.Debug.REPLWeb == 0 {
		return
	}

	mach := h.A.Mach()
	ctx := mach.NewStateCtx(ss.REPL)
	webPort := strconv.Itoa(cfg.Debug.REPLWeb)

	mach.Fork(ctx, e, func() {
		title := "REPL / " + h.A.ConfigBase().Agent.Label
		AddErrPTY(e, mach,
			h.ExposeCmd(ctx, e, title, webPort, shared.BinaryPath(cfg), []string{"repl", "--config", cfg.File}, nil))
	})
}

// ///// ///// /////

// ///// METHODS

// ///// ///// /////

func (h *Handlers) ExposeSSH(ctx context.Context, e *am.Event, title, webPort, sshPort string, clientGoneCh chan<- string) error {
	cmd := "ssh"
	args := strings.Split(fmt.Sprintf(
		"localhost -p %s "+
			"-o UserKnownHostsFile=/dev/null "+
			"-o StrictHostKeyChecking=no", sshPort), " ")

	return h.ExposeCmd(ctx, e, title, webPort, cmd, args, clientGoneCh)
}

func (h *Handlers) ExposeCmd(
	ctx context.Context, e *am.Event, title, webPort string, cmd string, args []string, clientGoneCh chan<- string,
) error {
	mach := h.A.Mach()

	appOptions := &gotty.Options{}
	backendOptions := &localcommand.Options{}
	if err := utils.ApplyDefaultValues(backendOptions); err != nil {
		return err
	}
	backendOptions.CloseSignal = int(syscall.SIGINT)
	backendOptions.CloseTimeout = 0

	// _, _, err := utils.GenerateFlags(appOptions, backendOptions)
	// if err != nil {
	// 	return err
	// }

	// TODO security
	// if c.IsSet("credential") {
	// 	appOptions.EnableBasicAuth = true
	// }
	// if c.IsSet("tls-ca-crt") {
	// 	appOptions.EnableTLSClientAuth = true
	// }

	// err := appOptions.Validate()
	// if err != nil {
	// 	return err
	// }

	factory, err := localcommand.NewFactory(cmd, args, backendOptions)
	if err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	appOptions.TitleVariables = map[string]interface{}{
		"command":  cmd,
		"argv":     args,
		"hostname": hostname,
	}
	appOptions.TitleFormat = title
	// TODO config
	appOptions.Address = "localhost"
	appOptions.Port = webPort
	// TODO doesnt fully render
	// appOptions.EnableReconnect = true
	// appOptions.ReconnectTime = 5
	appOptions.PermitWrite = true
	appOptions.IndexRewrite = func(s string) string {
		meta := `<meta name="viewport" content="width=device-width, initial-scale=1">`
		return strings.ReplaceAll(s, meta, meta+`
			<style>
				.xterm-dom-renderer-owner-1 .xterm-bg-0 {
					background-color: black!important;
				}
			</style>`)
	}
	appOptions.ClientGoneCh = clientGoneCh

	srv, err := gotty.New(factory, appOptions)
	if err != nil {
		return err
	}

	// start (block)
	AddErr(e, mach, srv.Run(ctx))

	return nil
}

func (h *Handlers) StartHTTP(e *am.Event) error {
	mach := h.A.Mach()

	goappHandler := &goapp.Handler{
		// TODO config
		Name: h.A.ConfigBase().Agent.Label,
		Lang: "en",
		// TODO config
		Author:       "ai-gents.work",
		Title:        h.A.ConfigBase().Agent.Label,
		Image:        "/web/assets/logo-512.png",
		LoadingLabel: "Loading...",
		Icon: goapp.Icon{
			Default:  "/web/assets/logo-192.png",
			Large:    "/web/assets/logo-512.png",
			Maskable: "/web/assets/logo-192.png",
		},
		// TODO compile
		Styles: []string{
			"/web/assets/deps/daisyui@5.css",
			"/web/assets/deps/themes.css",
		},
		Scripts: []string{
			"/web/assets/deps/tailwind@4.js",
			"/web/assets/deps/chart.js",
			"/web/assets/deps/utils.js",
		},
		CacheableResources: []string{
			"/web/assets/logo.svg",
		},
		ServiceWorkerTemplate: " ",
		RawHeaders:            []string{"<script>\n" + string(clockmojiJS) + "\n</script>"},
		Resources:             ResourceFS(h.A.Store().Web),
	}

	goapp.RouteWithRegexp("/.*", goapp.NewZeroComponentFactory(&splashScreen{}))

	// websocket relay

	cfg := h.A.ConfigBase()
	opts := amrelayt.Args{
		Name:   mach.Id() + "-wasm",
		Debug:  cfg.Debug.DBGAddr != "" && cfg.Debug.Verbose,
		Parent: mach,
		Wasm: &amrelayt.ArgsWasm{
			ListenAddr: cfg.Web.Addr,
			// catch UI machines in-process (avoid TCP tunns)
			ClientMatchers: []amrelayt.ClientMatcher{{
				// TODO ID generator
				Id:        regexp.MustCompile("^bro-dash-" + cfg.Agent.ID),
				NewClient: h.newDashFunc(e),
			}, {
				// TODO ID generator
				Id:        regexp.MustCompile("^bro-agentui-" + cfg.Agent.ID),
				NewClient: h.newAgentUIFunc(e),
			}},
		},
		Output: mach.Log,
	}
	// TODO https://gist.github.com/ghstahl/0e082ae6f65822518700cd4bd6ab79af
	// resourceResolver = app.LocalDir("")
	// resourceResolver = ResourceResolverWithVersion(a, "v0.0.42")
	// h := app.Handler{
	//    Resources: resourceResolver,
	// }
	if cfg.Debug.REPL {
		opts.Wasm.ReplAddrDir = cfg.Agent.Dir
	}
	relay, err := amrelay.New(mach.Context(), opts)
	if err != nil {
		return err
	}
	err = ampipe.Bind(relay.Mach, mach, ssr.RelayStates.HttpReady, ss.WebHTTPReady, "")
	if err != nil {
		return err
	}
	relay.Start(e)
	<-relay.Mach.When1(ssr.RelayStates.HttpReady, nil)
	relay.HttpMux.Handle("/", goappHandler)
	relay.HttpMux.HandleFunc("/bootstrap", h.handleBootstrap)

	// TODO maybe race
	h.relay = relay

	return nil
}

func (h *Handlers) handleBootstrap(w http.ResponseWriter, req *http.Request) {

	// machine
	machSchema, machStates := h.A.AgentImpl().MachSchema()
	ret := types.DataBoostrap{
		MachSchema: machSchema,
		MachStates: machStates,
	}

	// config
	cfg := *h.A.ConfigBase()
	for _, ai := range cfg.AI.OpenAI {
		if ai.Key != "" {
			ai.Key = "SET"
		}
	}
	for _, ai := range cfg.AI.Gemini {
		if ai.Key != "" {
			ai.Key = "SET"
		}
	}

	ret.Config = &cfg

	// return JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ret); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handlers) pushMetrics(e *am.Event, browsers []*arpc.Client, store *shared.AgentStore) {
	for _, b := range browsers {
		b.NetMach.EvAdd1(e, ssB.Data, PassRpc(&A{
			DataDash: &types.DataDashboard{
				Metrics: new(types.DataMetrics{
					// TODO
				}),
			},
		}))
	}
}

func (h *Handlers) newDashFunc(e *am.Event) amrelayt.NewClientFunc {
	mach := h.A.Mach()

	return func(ctx context.Context, id string, conn net.Conn) (*arpc.Client, error) {
		// init
		suffix := strconv.Itoa(h.nextDashNum)
		ctx, _ = onecontext.Merge(mach.Context(), ctx)
		dash, err := arpc.NewClient(ctx, "localhost:0", "server-dash-"+mach.Id()+"-"+suffix, ssb.PageSchema,
			&arpc.ClientOpts{
				Parent: mach,
			})
		if err != nil {
			return nil, err
		}
		h.nextDashNum++

		// bind and save
		err = ampipe.BindReady(dash.Mach, mach, ss.RemoteDashReady, ss.RemoteDashReady)
		if err != nil {
			return nil, err
		}
		ok := mach.Eval("relay_new_client_dash", func() {
			h.dashboards = append(h.dashboards, dash)
		}, ctx)
		if !ok {
			dash.Stop(nil, e, true)
			return nil, errors.New("relay_new_client_dash failed")
		}

		// TODO dispose on disconn

		// inject conn, start
		dash.Conn.Store(&conn)
		dash.Start(e)

		return dash, nil
	}
}

func (h *Handlers) newAgentUIFunc(e *am.Event) amrelayt.NewClientFunc {
	mach := h.A.Mach()
	// pipes for states with arguments
	pipes := am.S{ss.UIMsg, ss.UIRenderStories, ss.UIRenderClock, ss.UICleanOutput}

	return func(ctx context.Context, id string, conn net.Conn) (*arpc.Client, error) {
		// init
		suffix := strconv.Itoa(h.nextAgentUINum)
		ctx, _ = onecontext.Merge(mach.Context(), ctx)
		remoteUI, err := arpc.NewClient(ctx, "localhost:0", "server-agentui-"+mach.Id()+"-"+suffix, ssb.PageSchema,
			&arpc.ClientOpts{
				Parent: mach,
			})
		if err != nil {
			return nil, err
		}
		h.nextAgentUINum++

		// bind to local TODO deadlock?
		err = ampipe.BindReady(remoteUI.Mach, mach, ss.RemoteUIReady, ss.RemoteUIReady)
		if err != nil {
			return nil, err
		}
		ok := mach.Eval("relay_new_client_ui", func() {
			h.agentUIs = append(h.agentUIs, remoteUI)
		}, ctx)
		if !ok {
			remoteUI.Stop(nil, e, true)
			return nil, errors.New("relay_new_client_ui failed")
		}

		// TODO dispose on disconn

		// inject conn, start
		remoteUI.Conn.Store(&conn)
		remoteUI.Start(e)

		// bind when connected
		go func() {
			<-remoteUI.Mach.When1(ssrpc.ClientStates.Ready, ctx)
			if ctx.Err() != nil {
				return // expired
			}

			// bind to remote
			err = ampipe.BindMany(mach, remoteUI.NetMach, pipes, nil)
			if err != nil {
				AddErr(e, mach, err, nil)
			}
		}()

		return remoteUI, nil
	}
}

// ///// ///// /////

// ///// MISC

// ///// ///// /////

// splash

type splashScreen struct{ goapp.Compo }

func (c *splashScreen) Render() goapp.UI {
	return goapp.Div().Text("The application is loading...")
}

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

// embed resolver

var _ goapp.ResourceResolver = (*embeddedResourceResolver)(nil)

func ResourceFS(web fs.FS) goapp.ResourceResolver {
	return embeddedResourceResolver{
		Handler: http.FileServer(http.FS(web)),
	}
}

type embeddedResourceResolver struct {
	http.Handler
}

func (r embeddedResourceResolver) Resolve(location string) string {
	if location == "" {
		return "/"
	}
	return location
}
