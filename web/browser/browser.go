package browser

import (
	"errors"
	"fmt"
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
	ssbase "github.com/pancsta/secai/states"
	"github.com/pancsta/secai/web/browser/states"
	"github.com/pancsta/secai/web/types"
)

// agent base

var ssA = ssbase.AgentBaseStates
var ssAUI = states.AgentUIStates

var ss = states.PageStates

var randID = amhelp.RandId(4)

var AppClass = "p-4"

// ///// ///// /////

// ///// CMD

// ///// ///// /////

func Cmd() {
	goapp.Route("/", goapp.NewZeroComponentFactory(&Dashboard{}))
	goapp.Route("/agent", goapp.NewZeroComponentFactory(&AgentUI{}))
	goapp.RunWhenOnBrowser()
}

// ///// ///// /////

// ///// PAGE BASE

// ///// ///// /////

type BasePage struct {
	goapp.Compo
	*am.ExceptionHandler

	// config

	// ID of this page
	Id types.ID
	// Page schema, defaults to secai/web/browser/states.
	PageSchema am.Schema
	PageStates am.States

	// instances

	App goapp.Context
	// BasePage state machine
	Mach *am.Machine
	// Network machine of AgentClient (nil before ReadyState).
	Agent *arpc.NetworkMachine
	// Agent RPC client, uses the schema from the top-level agent.
	AgentClient *arpc.Client

	// BasePage RPC server
	srv *arpc.Server
	// Agent handler machine
	agentHand *am.Machine

	// data

	Boot *types.DataBoostrap
}

var _ goapp.Initializer = &BasePage{}
var _ goapp.Mounter = &BasePage{}

func (p *BasePage) OnInit() {
	// default machine
	p.PageSchema = states.PageSchema
	p.PageStates = states.PageStates
}

// OnMount gets called when the component is mounted
// This is after Render was called for the first time
func (p *BasePage) OnMount(app goapp.Context) {
	p.App = app

	// TODO config
	// page styles, dark theme TODO proper DOM API
	// a.Html().Class("bg-base-300").DataSet("theme", "dark")
	htmlEl := goapp.Window().Get("document").Get("documentElement")
	htmlEl.Call("setAttribute", "data-theme", "business")
	// TODO colors?
	htmlEl.Call("setAttribute", "class", "bg-base-300")
	goapp.Window().Get("document").Get("body").
		Call("setAttribute", "class", "bg-base-300 "+AppClass)

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

	// initial config
	boot := res.Result().(*types.DataBoostrap)
	if boot.Config == nil || boot.MachStates == nil || boot.MachSchema == nil {
		p.Error(fmt.Errorf("incomplete bootstrap data: %+v", boot))
		return
	}
	p.Boot = boot
	p.Dump(ss.Config, p.Boot.Config)

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
	return p.Mach != nil && p.Mach.Is1(ss.Start)
}

//

// HANDLERS

//

var _ = am.StateAny

func (p *BasePage) AnyState(e *am.Event) {
	// no render on health
	called := e.Transition().TimeIndexCalled()
	if called.Any1(ss.Healthcheck, ss.Heartbeat) {
		return
	}

	// TODO optimize with Diff
	p.Draw()
}

var _ = ss.Config

func (p *BasePage) ConfigEnter(e *am.Event) bool {
	return am.ParseArgs[types.AConfig](e.Args).Config != nil
}

func (p *BasePage) ConfigState(e *am.Event) {
	// config update
	p.Boot.Config = am.ParseArgs[types.AConfig](e.Args).Config
}

//

// METHODS

//

func (p *BasePage) Draw() {
	p.App.Update()
}

// TODO switch to tags verbose
func (p *BasePage) Dump(val ...any) {
	if p.Boot != nil && !p.Boot.Config.Debug.Verbose {
		return
	}
	dump.Println(val...)
}

func (p *BasePage) initBrowserMach() error {
	ctx := p.App.Context
	id := p.Boot.Config.Agent.ID
	cfg := p.Boot.Config.Web
	replAddr := ""
	// TODO IoC
	switch p.Id {
	case types.IDDashboardPage:
		replAddr = cfg.REPLAddrDash()
	case types.IDAgentUIPage:
		replAddr = cfg.REPLAddrAgentUI()
	}

	machID := types.GenBrowserID(p.Id, id, randID)
	mach, err := am.NewCommon(ctx, machID, p.PageSchema, p.PageStates.Names(), nil, nil, nil)
	if err != nil {
		return err
	}
	mach.SemLogger().SetArgsMapper(amhelp.LogArgsMapper)
	amhelp.MachDebugEnv(mach)
	p.Mach = mach
	// re-set the config
	mach.Add1(ss.Config, am.Pass(&types.AConfig{
		Config: p.Boot.Config,
	}))
	repl, err := arpc.MachReplWs(mach, cfg.Addr, &arpc.ReplOpts{
		WebSocketTunnel: arpc.WsListenPath("repl-"+mach.Id(), replAddr),
		Args:            types.ArgsRPC,
	})
	if err == nil {
		repl.Start(nil)
	}

	// RPC Server

	srv, err := arpc.NewServer(ctx, cfg.Addr, machID, mach, &arpc.ServerOpts{
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
	ctx := p.App.Context
	id := p.Boot.Config.Agent.ID
	cfg := p.Boot.Config.Web
	var agentID types.ID
	// TODO IoC
	switch p.Id {
	case types.IDDashboardPage:
		agentID = types.IDDashboardAgent
	case types.IDAgentUIPage:
		agentID = types.IDAgentUIAgent
	}

	// RPC Handlers Machine

	machID := types.GenBrowserID(agentID, id, randID)
	agentHandMach, err := am.NewCommon(ctx, machID, p.Boot.MachSchema, p.Boot.MachStates, nil, p.Mach, &am.Opts{
		Tags: []string{arpc.TagRpcHandler},
	})
	if err != nil {
		return err
	}
	p.agentHand = agentHandMach
	amhelp.MachDebugEnv(agentHandMach)
	agentHandMach.SemLogger().SetArgsMapper(amhelp.LogArgsMapper)

	// RPC Client (Net Machine) via relay

	agentRPC, err := arpc.NewClient(ctx, cfg.Addr, agentHandMach.Id(), p.Boot.MachSchema, &arpc.ClientOpts{
		Parent:    agentHandMach,
		WebSocket: arpc.WsDialPath(id, cfg.AddrAgent()),
	})
	if err != nil {
		return err
	}
	p.AgentClient = agentRPC

	// pipe rpc to Dashboard
	pipeFrom := am.S{ssrpc.ClientStates.Ready, ssrpc.ClientStates.Connecting}
	pipeTo := am.S{ss.RPCConnected, ss.RPCConnecting}
	_, err = ampipe.BindMany(agentRPC.Mach, p.Mach, pipeFrom, pipeTo)
	if err != nil {
		return err
	}

	// finish setting up handlers
	// ah.handMach = agentHandMach

	return nil
}

func (p *BasePage) initDebug() {
	cfg := p.Boot.Config
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
	p.AgentClient.Start(nil)
	// wait for RPC TODO timeout?
	<-p.AgentClient.Mach.When1(ssrpc.ClientStates.Ready, nil)

	// start the Dashboard
	p.Mach.Add1(ss.Start, nil)
	return nil
}

func (p *BasePage) Spinner() goapp.UI {
	return goapp.Progress().Class("progress w-56")
}

// ///// ///// /////

// ///// DASHBOARD

// ///// ///// /////

type Dashboard struct {
	BasePage

	// data & config

	SkipHandlers bool
	Data         *types.DataDashboard

	// Dashboard state
	FormBackend    string
	FormKey        string
	FormURL        string
	FormModel      string
	FormSubmitting bool
	FormErr        string
}

// OnInit constructor
func (d *Dashboard) OnInit() {
	d.BasePage.OnInit()
	d.Id = types.IDDashboardPage

	// defaults
	d.FormBackend = "openai"
	d.Data = &types.DataDashboard{}
}

func (d *Dashboard) OnMount(ctx goapp.Context) {
	d.BasePage.OnMount(ctx)

	// dashboard UI page TODO align Agent UI page
	if !d.SkipHandlers {
		_, err := d.Mach.HandlersBind(d)
		if err != nil {
			d.Error(err)
			return
		}
	} else {
		d.Dump("skipping handlers mount")
	}

	// start (block)
	if err := d.start(); err != nil {
		d.Error(err)
	}
	d.Dump("netAgent mounted")
	netAgent := d.AgentClient.NetMach
	d.Agent = netAgent

	// bind and sync the handler mach to net mach
	d.agentHand.Set(netAgent.ActiveStates(nil), nil)
	if _, err := ampipe.BindAny(netAgent, d.agentHand); err != nil {
		d.Error(err)
		return
	}

	// whole agent time sync
	_, err := NewNetAgent(netAgent, d, nil, d)
	if err != nil {
		d.Error(err)
		return
	}
}

//

// HANDLERS

//

func (d *Dashboard) DataEnter(e *am.Event) bool {
	args := am.ParseArgs[types.AData](e.Args)
	// dump.Println(e.Args)
	// dump.Println(args)
	return args.DataDash != nil
}

func (d *Dashboard) DataState(e *am.Event) {
	dash := am.ParseArgs[types.AData](e.Args).DataDash

	if dash.Metrics != nil {
		d.Data.Metrics = dash.Metrics
	}
	if dash.Splash != "" {
		d.Data.Splash = dash.Splash
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
	a.Id = types.IDAgentUIPage
	a.PageSchema = states.AgentUISchema
	a.PageStates = states.AgentUIStates
	a.data = &types.DataAgent{}
}

func (a *AgentUI) OnMount(ctx goapp.Context) {
	a.BasePage.OnMount(ctx)
	// TODO proper DOM API
	goapp.Window().Get("document").Get("body").
		Call("setAttribute", "class", "bg-base-300 h-screen w-screen "+AppClass)

	// agent UI page
	_, err := a.Mach.HandlersBind(a)
	if err != nil {
		a.Error(err)
		return
	}

	// start (block)
	if err := a.start(); err != nil {
		a.Error(err)
	}
	netAgent := a.AgentClient.NetMach
	a.Agent = netAgent

	// bind and sync the handler mach to net mach
	a.agentHand.Set(netAgent.ActiveStates(nil), nil)
	if _, err := ampipe.BindAny(netAgent, a.agentHand); err != nil {
		a.Error(err)
		return
	}

	// whole agent time sync
	_, err = NewNetAgent(netAgent, a, a, nil)
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
		a.App.Dispatch(func(_ goapp.Context) {
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

var _ = ssAUI.Data

func (a *AgentUI) DataEnter(e *am.Event) bool {
	return am.ParseArgs[types.AData](e.Args).DataAgent != nil
}

func (a *AgentUI) DataState(e *am.Event) {
	// TODO handle all the UIRender* here via Add rel
	data := am.ParseArgs[types.AData](e.Args).DataAgent

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
	a.Dump(ss.Data, data)
}

var _ = ssAUI.UIMsg

func (a *AgentUI) UIMsgEnter(e *am.Event) bool {
	return am.ParseArgs[shared.AUIMsg](e.Args).Msg != nil
}

func (a *AgentUI) UIMsgState(e *am.Event) {
	msg := am.ParseArgs[shared.AUIMsg](e.Args).Msg
	a.data.Msgs = append(a.data.Msgs, msg)
	scrolled := a.msgsScrolled()
	a.Dump("UIMsgState/scroll", scrolled)
	if scrolled {
		a.msgsScrollPending = true
	}
}

var _ = ssAUI.UIRenderClock

func (a *AgentUI) UIRenderClockEnter(e *am.Event) bool {
	return am.ParseArgs[shared.AUIRenderClock](e.Args).ClockDiff != nil
}

func (a *AgentUI) UIRenderClockState(e *am.Event) {
	a.data.ClockDiff = am.ParseArgs[shared.AUIRenderClock](e.Args).ClockDiff
	// a.Dump("UIRenderClockState", a.data.ClockDiff)
	a.updateClock()
}

var _ = ssAUI.UIRenderStories

func (a *AgentUI) UIRenderStoriesState(e *am.Event) {
	args := am.ParseArgs[shared.AUIRenderStories](e.Args)
	// a.Dump("UIRenderStoriesState/Stories", args.Stories)
	// a.Dump("UIRenderStoriesState/Actions", args.Actions)
	if args.Stories != nil {
		a.data.Stories = args.Stories
	}
	if args.Actions != nil {
		a.data.Actions = args.Actions
	}
}

var _ = ssAUI.UICleanOutput

func (a *AgentUI) UICleanOutputState(e *am.Event) {
	a.data.Msgs = nil
}

// ///// ///// /////

// ///// AGENT NETMACH

// ///// ///// /////

type PageAPI interface {
	Draw()
	Dump(...any)
}

// NetAgent is aRPC client handlers
type NetAgent struct {
	page      PageAPI
	pageAgent *AgentUI
	pageDash  *Dashboard
}

func NewNetAgent(netMach *arpc.NetworkMachine, page PageAPI, pageAgent *AgentUI, pageDash *Dashboard) (*NetAgent, error) {
	if page == nil {
		return nil, errors.New("page is nil")
	}

	h := &NetAgent{
		page:      page,
		pageAgent: pageAgent,
		pageDash:  pageDash,
	}
	_, err := netMach.HandlersBind(h)
	if err != nil {
		return nil, err
	}

	return h, nil
}

var _ = am.StateAny

func (h *NetAgent) AnyState(e *am.Event) {
	// arpc syncs have aggregated called states
	diff := e.Transition().TimeIndexTimeDiff()
	num := len(diff.ActiveStates(nil))
	// ignore health-only txs
	if (num == 2 && diff.Is(am.S{ss.Healthcheck, ss.Heartbeat})) ||
		(num == 1 && diff.Any1(ss.Healthcheck, ss.Heartbeat)) {

		return
	}

	// h.page.Dump(am.StateAny, diff.ActiveStates(nil))
	// TODO optimize with Diff + allowlist
	h.page.Draw()
}
