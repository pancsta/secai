package shared

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sblinch/kdl-go"
)

const kdlTestTpl = `// hand-written comment
Web {
  LogPort 12858
}

// /////
// ///// DEFAULT VALUES
// /////

//Web {
//  LogPort 1
//}
`

func TestKdlCfgDump(t *testing.T) {
	out := KdlCfgDump(ConfigDefault())
	t.Logf("output:\n%s", out)

	// a single /* */ block
	if !strings.HasPrefix(out, "/*\n") || !strings.HasSuffix(out, "*/\n") {
		t.Fatalf("not a block comment:\n%s", out)
	}
	if strings.Count(out, "/*") != 1 || strings.Count(out, "*/") != 1 {
		t.Fatalf("expected a single block comment:\n%s", out)
	}
	// values come from ConfigDefault, not from the template
	for _, want := range []string{
		"\nWeb {\n",
		`  Addr "localhost:12854"`,
		`  LogPort 12858`,
		`    Level "INFO"`,
		`    Backend "memory"`,
		`    Max 1000000`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// godoc of the field, as a comment inside the block
	if !strings.Contains(out, "// Start a log web UI") {
		t.Fatalf("missing godoc:\n%s", out)
	}
	// TODO notes are not part of the docs
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "TODO") {
			t.Fatalf("TODO godoc leaked: %q", line)
		}
	}
	// the block is valid KDL
	if _, err := kdl.Parse(strings.NewReader(out)); err != nil {
		t.Fatalf("dump does not parse: %v\n%s", err, out)
	}
}

func TestKdlTplWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.tpl.kdl")
	if err := os.WriteFile(path, []byte(kdlTestTpl), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := KdlTplWrite(path, []byte(kdlTestTpl), ConfigDefault()); err != nil {
		t.Fatal(err)
	}
	out1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// the hand-written part is untouched
	if !strings.HasPrefix(string(out1), "// hand-written comment\nWeb {\n  LogPort 12858\n}\n") {
		t.Fatalf("top changed:\n%s", out1)
	}
	// the defaults section is regenerated from ConfigDefault
	_, tail, ok := strings.Cut(string(out1), kdlTplMarker)
	if !ok {
		t.Fatalf("no marker:\n%s", out1)
	}
	if strings.Contains(tail, "LogPort 1\n") {
		t.Fatalf("stale defaults:\n%s", tail)
	}
	if !strings.Contains(tail, `LogPort 12858`) {
		t.Fatalf("missing defaults:\n%s", tail)
	}
	// the defaults live in a single block comment
	if !strings.HasPrefix(strings.TrimSpace(tail), "// /////\n\n/*") {
		t.Fatalf("defaults not in a block comment:\n%s", tail)
	}
	if !strings.HasSuffix(strings.TrimSpace(tail), "*/") {
		t.Fatalf("block not closed:\n%s", tail)
	}
	// the whole file is still valid KDL
	if _, err := kdl.Parse(bytes.NewReader(out1)); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, out1)
	}

	// a second run is a no-op
	if err := KdlTplWrite(path, []byte(kdlTestTpl), ConfigDefault()); err != nil {
		t.Fatal(err)
	}
	out2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(out1) != string(out2) {
		t.Fatalf("not idempotent:\n---\n%s\n---\n%s", out1, out2)
	}
}
