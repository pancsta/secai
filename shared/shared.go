package shared

import (
	"fmt"
	"slices"
	"strings"

	"github.com/BooleanCat/go-functional/v2/it"
	"github.com/lithammer/dedent"
	"github.com/orsinium-labs/enum"
	"github.com/pancsta/asyncmachine-go/pkg/helpers"
	"github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/rpc"
	amtele "github.com/pancsta/asyncmachine-go/pkg/telemetry"
	amprom "github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	amgen "github.com/pancsta/asyncmachine-go/tools/generator"
)

// Sp formats a de-dented and trimmed string using the provided arguments, similar to fmt.Sprintf.
func Sp(txt string, args ...any) string {
	return fmt.Sprintf(dedent.Dedent(strings.Trim(txt, "\n")), args...)
}

func Sl(txt string, args ...any) string {
	return fmt.Sprintf(txt, args...) + "\n"
}

// P formats and prints the given string after de-denting and trimming it, and returns the number of bytes written and any error.
func P(txt string, args ...any) {
	fmt.Printf(dedent.Dedent(strings.Trim(txt, "\n")), args...)
}

// string join
func Sj(parts ...string) string {
	return strings.Join(parts, " ")
}

func MachTelemetry(mach *machine.Machine, logArgs machine.LogArgsMapper) {
	// default (non-debug) log level
	mach.SetLogLevel(machine.LogChanges)
	// default args mapper
	mach.SetLogArgs(machine.NewArgsMapper(machine.LogArgs, 0))
	// dedicated args mapper
	if logArgs != nil {
		mach.SetLogArgs(logArgs)
	}

	// env-based telemetry

	// connect to an am-dbg instance
	helpers.MachDebugEnv(mach)
	// start a dedicated aRPC server for the REPL, create an addr file
	rpc.MachReplEnv(mach)

	// root machines only
	if mach.ParentId() == "" {

		// export metrics to prometheus
		amprom.MachMetricsEnv(mach)

		// grafana dashboard
		err := amgen.MachDashboardEnv(mach)
		if err != nil {
			mach.AddErr(err, nil)
		}

		// open telemetry traces
		err = amtele.MachBindOtelEnv(mach, false)
		if err != nil {
			mach.AddErr(err, nil)
		}
	}
}

func Map[A, B any](vals []A, f func(A) B) []B {
	return slices.Collect(it.Map(slices.Values(vals), f))
}

// From enum

type From enum.Member[string]

var (
	FromAssistant = From{"assistant"}
	FromSystem    = From{"system"}
	FromUser      = From{"user"}
	FromKind      = enum.New(FromAssistant, FromSystem, FromUser)
)
