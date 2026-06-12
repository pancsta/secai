package cook

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"dario.cat/mergo"
	cp "github.com/otiai10/copy"
	amhelp "github.com/pancsta/asyncmachine-go/pkg/helpers"
	amhelpt "github.com/pancsta/asyncmachine-go/pkg/helpers/testing"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	sa "github.com/pancsta/secai/examples/cook/schema"
	"github.com/pancsta/secai/shared"
	"github.com/sblinch/kdl-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	timeout    = 3 * time.Second
	configFile = "config.kdl"
	// amDbg      = false
	amDbg = true
	// amLog = am.LogOps
	// amLog = am.LogChanges
	amLog = am.LogNothing
)

func init() {
	// amhelp.EnableDebugging(true)
	// amhelp.EnableDebugging(false)

	// <-ctx.Done() // TODO DEBUG
	// return
}

func TestAcceptance(t *testing.T) {
	runAcceptance(t, "")
}

func TestAcceptanceHotRun(t *testing.T) {
	runAcceptance(t, "testdata")
}

func runAcceptance(t *testing.T, dbDir string) {
	mock.Active = true

	ctx, cancel := signal.NotifyContext(context.Background(), os.Kill, os.Interrupt)
	defer cancel()

	a := NewTestAgent(t, ctx, "")
	if dbDir != "" {
		for _, dbFile := range []string{"secai.sqlite", "cook.sqlite"} {
			err := cp.Copy(filepath.Join(dbDir, dbFile), filepath.Join(tmpDir(t), dbFile))
			assert.NoError(t, err)
		}
	}
	a.Start()
	<-a.Mach().When1(ss.StepsReady, nil)

	t.Log("Steps ready, adding")
	time.Sleep(100 * time.Millisecond)
	steps, err := a.cookingSteps()
	assert.NoError(t, err)
	for _, step := range steps {
		a.mem.Add1(step, nil)
		time.Sleep(100 * time.Millisecond)
	}

	<-a.Mach().When1(ss.StoryMealReady, nil)

	Dispose(a.Mach())
}

func TestGenSteps(t *testing.T) {
	mock.Active = false

	ctx, cancel := signal.NotifyContext(context.Background(), os.Kill, os.Interrupt)
	defer cancel()

	a := NewTestAgent(t, ctx, "")
	p := sa.NewPromptGenSteps(a)
	params := LoadPrompt[sa.ParamsGenSteps](t, ss.GenSteps, "")

	// TODO panics sometimes https://github.com/jxnl/instructor-go/issues/66
	res, err := p.Exec(nil, params, shared.PromptOpts{
		SkipState: true,
	})
	require.NoError(t, err)
	if err != nil {
		return
	}

	_, _, err = a.processStepSchema(ctx, res)
	amhelpt.AssertNoErrEver(t, a.Mach())

	Dispose(a.Mach())
}

func TestGenStepsProcess(t *testing.T) {
	mock.Active = false

	ctx, cancel := signal.NotifyContext(context.Background(), os.Kill, os.Interrupt)
	defer cancel()

	versions := []string{"", "1", "2", "3"}

	for _, ver := range versions {
		a := NewTestAgent(t, ctx, ver)
		// TODO version "", "1"
		res := LoadPromptResult[sa.ResultGenSteps](t, ss.GenSteps, ver)
		_, _, err := a.processStepSchema(ctx, &res)
		require.NoError(t, err)

		Dispose(a.Mach())
	}
}

// ----- ----- -----

// ----- HELPERS

// ----- ----- -----

func tmpDir(t *testing.T) string {
	return filepath.Join("testdata", "tmp", t.Name())
}

func LoadPromptResult[G any](t *testing.T, name, version string) G {
	loc := filepath.Join("testdata", "Prompts", version, name+".result.json")
	var res G
	bytes, err := os.ReadFile(loc)
	require.NoError(t, err)
	err = json.Unmarshal(bytes, &res)
	require.NoError(t, err)

	return res
}

func LoadPrompt[G any](t *testing.T, name, version string) G {
	loc := filepath.Join("testdata", "Prompts", version, name+".prompt.json")
	var res G
	bytes, err := os.ReadFile(loc)
	require.NoError(t, err)
	err = json.Unmarshal(bytes, &res)
	require.NoError(t, err)

	return res
}

func NewTestAgent(t *testing.T, ctx context.Context, idSuffix string) *Agent {
	cfg := NewConfig(t, idSuffix)
	a := New(ctx)

	assert.NoError(t,
		a.Init(cfg))
	amhelpt.LogToTestLog(t, a.Mach(), amLog)

	return a
}

func Dispose(mach *am.Machine) {
	mach.Add1(ss.Disposing, nil)
	<-mach.WhenDisposed()
	if amhelp.IsDebug() {
		time.Sleep(time.Second)
	}
}

// TODO config fixture
func NewConfig(t *testing.T, idSuffix string) *Config {
	// read
	var cfg Config
	cfgData, err := os.ReadFile(configFile)
	assert.NoError(t, err)

	// merge
	cfg = ConfigDefault()
	var cfgUser Config
	assert.NoError(t, kdl.Unmarshal(cfgData, &cfgUser))
	assert.NoError(t, mergo.Merge(&cfg, cfgUser, mergo.WithOverride))

	cfg.Agent.ID = "t-" + cfg.Agent.ID + "-" + t.Name() + idSuffix
	cfg.File = "test"
	dir := tmpDir(t)
	os.RemoveAll(dir)
	err = os.MkdirAll(dir, 0777)
	assert.NoError(t, err)
	cfg.Agent.Dir = dir
	cfg.Web.Addr = "-1"
	cfg.Web.LogPort = -1
	cfg.TUI.PortWeb = -1
	cfg.TUI.PortSSH = -1
	cfg.Debug.DBGAddr = "1"
	if !amDbg {
		cfg.Debug.DBGAddr = ""
	}
	cfg.Debug.Mock = mock.Active

	return &cfg
}
