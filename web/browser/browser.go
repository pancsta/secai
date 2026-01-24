package browser

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/gookit/goutil/dump"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	arpc "github.com/pancsta/asyncmachine-go/pkg/rpc"
	ssrpc "github.com/pancsta/asyncmachine-go/pkg/rpc/states"
	ampipe "github.com/pancsta/asyncmachine-go/pkg/states/pipes"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry/dbg"
	goapp "github.com/pancsta/go-app/pkg/app"
	"resty.dev/v3"

	"github.com/pancsta/secai/shared"
	sabase "github.com/pancsta/secai/states"
	"github.com/pancsta/secai/web/browser/states"
	"github.com/pancsta/secai/web/types"
)

// agent base

var ssA = sabase.AgentBaseStates

var PassRpcBase = shared.PassRPC
var ParseArgsBase = shared.ParseArgs

type ABase = shared.A

// browser

var ss = states.PageStates

var Pass = types.Pass
var PassRpc = types.PassRpc
var ParseArgs = types.ParseArgs

type AWeb = types.A

var randID = amhelp.RandId(4)

// ///// ///// /////

// ///// PAGE BASE

// ///// ///// /////

type BasePage struct {
	goapp.Compo
	*am.ExceptionHandler

	// config

	// ID of this page
	id         string
	pageSchema am.Schema
	pageStates am.States

	// instances

	app goapp.Context
	// BasePage state machine
	mach *am.Machine
	// BasePage RPC server
	srv   *arpc.Server
	agent *arpc.NetworkMachine
	// Agent RPC client
	agentClient *arpc.Client
	// Agent handler machine
	agentHand *am.Machine

	// data

	boot *types.DataBoostrap
}

var _ goapp.Initializer = &BasePage{}
var _ goapp.Mounter = &BasePage{}

func (p *BasePage) OnInit() {
	// default machine
	p.pageSchema = states.PageSchema
	p.pageStates = states.PageStates
}

// OnMount gets called when the component is mounted
// This is after Render was called for the first time
func (p *BasePage) OnMount(ctx goapp.Context) {
	p.app = ctx

	// page styles, dark theme TODO proper DOM API
	// a.Html().Class("bg-base-300").DataSet("theme", "dark")
	htmlEl := goapp.Window().Get("document").Get("documentElement")
	htmlEl.Call("setAttribute", "data-theme", "business")
	// TODO colors?
	htmlEl.Call("setAttribute", "class", "bg-base-300")
	goapp.Window().Get("document").Get("body").
		Call("setAttribute", "class", "p-10 bg-base-300")

	// initial data
	c := resty.New()
	defer c.Close()
	res, err := c.R().
		SetResult(&types.DataBoostrap{}).
		Get("/bootstrap")
	if err != nil {
		p.Error(err)
		return
	}
	boot := res.Result().(*types.DataBoostrap)
	if boot.Config == nil || boot.MachStates == nil || boot.MachSchema == nil {
		p.Error(fmt.Errorf("incomplete bootstrap data: %+v", boot))
		return
	}
	p.boot = boot

	p.initDebug()
	if err := p.initBrowserMach(); err != nil {
		p.Error(err)
		return
	}
	if err := p.initServerMach(); err != nil {
		p.Error(err)
		return
	}
}

func (p *BasePage) OnDismount(ctx goapp.Context) {
	// TODO stop RPCs
}

func (p *BasePage) Error(err error) {
	// TODO UI err
	panic(err)
}

func (p *BasePage) Ready() bool {
	return p.mach != nil && p.mach.Is1(ss.Start)
}

//

// HANDLERS

//

func (p *BasePage) AnyState(e *am.Event) {
	// no render on health
	called := e.Transition().TimeIndexCalled()
	if called.Any1(ss.Healthcheck, ss.Heartbeat) {
		return
	}

	// TODO optimize with Diff
	p.Draw()
}

func (p *BasePage) ConfigEnter(e *am.Event) bool {
	return ParseArgs(e.Args).Config != nil
}

func (p *BasePage) ConfigState(e *am.Event) {
	args := ParseArgs(e.Args)

	p.boot.Config = args.Config
}

//

// METHODS

//

func (p *BasePage) Draw() {
	p.app.Update()
}

func (p *BasePage) Dump(name string, val any) {
	if p.boot == nil || !p.boot.Config.Debug.Verbose {
		return
	}
	if val == nil {
		log.Printf("%s", name)
	} else {
		log.Printf("%s\n%s", name, dump.Format(val))
	}
}

func (p *BasePage) initBrowserMach() error {
	ctx := p.app.Context
	id := p.boot.Config.Agent.ID
	cfg := p.boot.Config.Web
	replAddr := ""
	// TODO IoC
	switch p.id {
	case "dash":
		replAddr = cfg.REPLAddrDash()
	case "agentui":
		replAddr = cfg.REPLAddrAgentUI()
	}

	mach, err := am.NewCommon(ctx, "bro-"+p.id+"-"+id+"-"+randID, p.pageSchema, p.pageStates.Names(), nil, nil, nil)
	if err != nil {
		return err
	}
	mach.SemLogger().SetArgsMapper(am.LogArgsMapperMerge(types.LogArgs, shared.LogArgs))
	amhelp.MachDebugEnv(mach)
	p.mach = mach
	// re-set the config
	mach.Add1(ss.Config, Pass(&AWeb{
		Config: p.boot.Config,
	}))
	repl, err := arpc.MachReplWs(mach, cfg.Addr, &arpc.ReplOpts{
		// TODO should be automatic in WASM
		WebSocketTunnel: arpc.WsListenPath("repl-"+mach.Id(), replAddr),
		Args:            types.ARpc{},
		ParseRpc:        types.ParseRpc,
	})
	if err == nil {
		repl.Start(nil)
	}

	// RPC Server

	srv, err := arpc.NewServer(ctx, cfg.Addr, "bro-"+p.id+"-"+id+"-"+randID, mach, &arpc.ServerOpts{
		// eg localhost:8080/listen/bar/localhost:7070 opens 7070 for "bar"
		// TODO should be automatic in WASM
		WebSocketTunnel: arpc.WsListenPath(mach.Id(), "localhost:0"),
		Parent:          mach,
	})
	if err != nil {
		return err
	}
	p.srv = srv

	return nil
}

func (p *BasePage) initServerMach() error {
	ctx := p.app.Context
	id := p.boot.Config.Agent.ID
	cfg := p.boot.Config.Web
	wsAddr := ""
	// TODO IoC
	switch p.id {
	case "dash":
		wsAddr = cfg.AgentWSAddrDash()
	case "agentui":
		wsAddr = cfg.AgentWSAddrRemoteUI()
	}

	// RPC Handlers Machine

	agentHandMach, err := am.NewCommon(ctx, "bro-agent-"+p.id+"-"+id+"-"+randID, p.boot.MachSchema, p.boot.MachStates,
		nil, p.mach, &am.Opts{
			Tags: []string{arpc.TagRpcHandler},
		})
	if err != nil {
		return err
	}
	p.agentHand = agentHandMach
	amhelp.MachDebugEnv(agentHandMach)
	agentHandMach.SemLogger().SetArgsMapper(types.LogArgs)

	// RPC Client (Net Machine)

	agentRPC, err := arpc.NewClient(ctx, wsAddr, agentHandMach.Id(), p.boot.MachSchema, &arpc.ClientOpts{
		Parent: agentHandMach,
		// automatic in WASM
		WebSocket: "/",
	})
	if err != nil {
		return err
	}
	p.agentClient = agentRPC
	// ah.rpc = agentRPC

	// pipe rpc to Dashboard
	pipeFrom := am.S{ssrpc.ClientStates.Ready, ssrpc.ClientStates.Connecting}
	pipeTo := am.S{ss.RPCConnected, ss.RPCConnecting}
	err = ampipe.BindMany(agentRPC.Mach, p.mach, pipeFrom, pipeTo)
	if err != nil {
		return err
	}

	// finish setting up handlers
	// ah.handMach = agentHandMach

	return nil
}

func (p *BasePage) initDebug() {
	cfg := p.boot.Config
	// log

	os.Setenv(am.EnvAmLog, cfg.Agent.Log.MachLevel.Level())
	if cfg.Agent.Log.MachPrint {
		os.Setenv(amhelp.EnvAmLogPrint, "1")
	}

	// dbg
	if cfg.Debug.DBGAddr == "1" {
		os.Setenv(dbg.EnvAmDbgAddr, "1")
		os.Setenv(amhelp.EnvAmLogFull, "1")

		// TODO move to amhelp
	} else if cfg.Debug.DBGAddr != "" {
		host, port, err := net.SplitHostPort(cfg.Debug.DBGAddr)
		if err != nil {
			p.Error(err)
			return
		}
		portNum, err := strconv.Atoi(port)
		if err != nil {
			p.Error(err)
			return
		}
		os.Setenv(dbg.EnvAmDbgAddr, host+":"+strconv.Itoa(portNum+1))
		os.Setenv(amhelp.EnvAmLogFull, "1")
	}

	// RPC debug
	if cfg.Debug.Verbose {
		if cfg.Debug.DBGAddr != "" {
			os.Setenv(arpc.EnvAmRpcDbg, "1")
		}
		os.Setenv(arpc.EnvAmRpcLogClient, "1")
		os.Setenv(arpc.EnvAmRpcLogServer, "1")
		os.Setenv(amhelp.EnvAmHealthcheck, "1")
	}
}

func (p *BasePage) start() error {
	p.srv.Start(nil)
	p.agentClient.Start(nil)
	// wait for RPC TODO timeout?
	<-p.agentClient.Mach.When1(ssrpc.ClientStates.Ready, nil)

	// start the Dashboard
	p.mach.Add1(ss.Start, nil)
	return nil
}

func (p *BasePage) spinner() goapp.UI {
	return goapp.Progress().Class("progress w-56")
}

// ///// ///// /////

// ///// DASHBOARD

// ///// ///// /////

type Dashboard struct {
	BasePage

	// data

	data *types.DataDashboard

	// Dashboard state
	formBackend    string
	formKey        string
	formURL        string
	formModel      string
	formSubmitting bool
	formErr        string
}

// OnInit constructor
func (d *Dashboard) OnInit() {
	d.BasePage.OnInit()
	d.id = "dash"

	// defaults
	d.formBackend = "openai"
	d.data = &types.DataDashboard{}
}

func (d *Dashboard) OnMount(ctx goapp.Context) {
	d.BasePage.OnMount(ctx)

	// dashboard UI page
	err := d.mach.BindHandlers(d)
	if err != nil {
		d.Error(err)
		return
	}

	// start (block)
	if err := d.start(); err != nil {
		d.Error(err)
	}
	netAgent := d.agentClient.NetMach
	d.agent = netAgent

	// bind and sync the handler mach to net mach
	d.agentHand.Set(netAgent.ActiveStates(nil), nil)
	if err := ampipe.BindAny(netAgent, d.agentHand); err != nil {
		d.Error(err)
		return
	}

	// whole agent time sync
	_, err = newNetAgent(netAgent, d, nil, d)
	if err != nil {
		d.Error(err)
		return
	}
}

//

// HANDLERS

//

func (d *Dashboard) DataEnter(e *am.Event) bool {
	return ParseArgs(e.Args).DataDash != nil
}

func (d *Dashboard) DataState(e *am.Event) {
	dash := ParseArgs(e.Args).DataDash

	if dash.Metrics != nil {
		d.data.Metrics = dash.Metrics
	}
	if dash.Splash != "" {
		d.data.Splash = dash.Splash
	}
}

// ///// ///// /////

// ///// AGENT UI

// ///// ///// /////

type AgentUI struct {
	BasePage

	// data

	data *types.DataAgent

	// Dashboard state
	formPrompt        string
	formSubmitting    bool
	formErr           string
	msgsScrollPending bool
	buttonClicked     string
	clockInit         bool
}

// OnInit constructor
func (a *AgentUI) OnInit() {
	a.BasePage.OnInit()
	a.id = "agentui"
	a.pageSchema = states.AgentUISchema
	a.pageStates = states.AgentUIStates
	a.data = &types.DataAgent{}
}

func (a *AgentUI) OnMount(ctx goapp.Context) {
	a.BasePage.OnMount(ctx)
	// TODO proper DOM API
	goapp.Window().Get("document").Get("body").
		Call("setAttribute", "class", "p-10 bg-base-300 h-screen w-screen")

	// agent UI page
	err := a.mach.BindHandlers(a)
	if err != nil {
		a.Error(err)
		return
	}

	// start (block)
	if err := a.start(); err != nil {
		a.Error(err)
	}
	netAgent := a.agentClient.NetMach
	a.agent = netAgent

	// bind and sync the handler mach to net mach
	a.agentHand.Set(netAgent.ActiveStates(nil), nil)
	if err := ampipe.BindAny(netAgent, a.agentHand); err != nil {
		a.Error(err)
		return
	}

	// whole agent time sync
	_, err = newNetAgent(netAgent, a, a, nil)
	if err != nil {
		a.Error(err)
		return
	}
}

func (a *AgentUI) updateClock() {
	if !a.Ready() {
		return
	}

	if !a.clockInit {
		a.clockInit = true
		a.app.Dispatch(func(_ goapp.Context) {
			time.Sleep(time.Second)
			goapp.Window().Call("clockmojiInit")
			a.updateClockSet()
		})

		return
	}

	a.updateClockSet()
}

func (a *AgentUI) updateClockSet() {
	if len(a.data.ClockDiff) == 0 {
		return
	}

	// TODO extract limit
	limit := 4
	jsDiff := make([]any, limit)
	for i, diff := range a.data.ClockDiff {
		// TODO limit
		if i == limit {
			break
		}

		// fml...
		inner := make([]any, len(diff))
		for ii, v := range diff {
			inner[ii] = v
		}

		jsDiff[i] = inner
	}

	// log.Printf("clockmojiUpdate %+v", jsDiff)
	goapp.Window().Call("clockmojiUpdate", jsDiff)
}

//

// HANDLERS

//

func (a *AgentUI) DataEnter(e *am.Event) bool {
	return ParseArgs(e.Args).DataAgent != nil
}

func (a *AgentUI) DataState(e *am.Event) {
	// TODO handle all the UIRender* here via Add rel
	data := ParseArgs(e.Args).DataAgent

	if a.data == nil {
		a.data = &types.DataAgent{}
	}

	if data.Msgs != nil {
		a.data.Msgs = data.Msgs
		a.msgsScrollPending = true
	}
	if data.Stories != nil {
		a.data.Stories = data.Stories
	}
	if data.Actions != nil {
		a.data.Actions = data.Actions
	}
	if data.ClockDiff != nil {
		a.data.ClockDiff = data.ClockDiff
	}
	a.Dump("DataState", data)
}

func (a *AgentUI) UIMsgEnter(e *am.Event) bool {
	return ParseArgsBase(e.Args).Msg != nil
}

func (a *AgentUI) UIMsgState(e *am.Event) {
	a.data.Msgs = append(a.data.Msgs,
		ParseArgsBase(e.Args).Msg)
	scrolled := a.msgsScrolled()
	a.Dump("UIMsgState/scroll", scrolled)
	if scrolled {
		a.msgsScrollPending = true
	}
}

func (a *AgentUI) UIRenderClockEnter(e *am.Event) bool {
	return ParseArgsBase(e.Args).ClockDiff != nil
}

func (a *AgentUI) UIRenderClockState(e *am.Event) {
	a.data.ClockDiff = ParseArgsBase(e.Args).ClockDiff
	// a.Dump("UIRenderClockState", a.data.ClockDiff)
	a.updateClock()
}

func (a *AgentUI) UIRenderStoriesState(e *am.Event) {
	args := ParseArgsBase(e.Args)
	// a.Dump("UIRenderStoriesState/Stories", args.Stories)
	// a.Dump("UIRenderStoriesState/Actions", args.Actions)
	if args.Stories != nil {
		a.data.Stories = args.Stories
	}
	if args.Actions != nil {
		a.data.Actions = args.Actions
	}
}

func (a *AgentUI) UICleanOutputState(e *am.Event) {
	a.data.Msgs = nil
}

// ///// ///// /////

// ///// AGENT NETMACH

// ///// ///// /////

type PageAPI interface {
	Draw()
	Dump(string, any)
}

// aRPC client handlers
type netAgent struct {
	page      PageAPI
	pageAgent *AgentUI
	pageDash  *Dashboard
}

func newNetAgent(netMach *arpc.NetworkMachine, page PageAPI, pageAgent *AgentUI, pageDash *Dashboard) (*netAgent, error) {
	if page == nil {
		return nil, errors.New("page is nil")
	}

	h := &netAgent{
		page:      page,
		pageAgent: pageAgent,
		pageDash:  pageDash,
	}
	err := netMach.BindHandlers(h)
	if err != nil {
		return nil, err
	}

	return h, nil
}

func (h *netAgent) AnyState(e *am.Event) {
	// arpc syncs have aggregated called states
	diff := e.Transition().TimeIndexTimeDiff()
	num := len(diff.ActiveStates(nil))
	// ignore chealth-only txs
	if (num == 2 && diff.Is(am.S{ss.Healthcheck, ss.Heartbeat})) ||
		(num == 1 && diff.Any1(ss.Healthcheck, ss.Heartbeat)) {

		return
	}

	h.page.Dump("AnyState", diff.ActiveStates(nil))
	// TODO optimize with Diff + allowlist
	h.page.Draw()
}
