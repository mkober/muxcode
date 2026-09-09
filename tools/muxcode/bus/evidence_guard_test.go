package bus

import (
	"reflect"
	"strings"
	"testing"
)

// liveBundledBuildCall is the Bash call a codex build agent made on
// 2026-09-09 00:28 answering a spec-to-pr build node — the incident this
// guard exists for.
const liveBundledBuildCall = "muxcode send build startup-ack \"Startup context received and restored.\" --type response --reply-to 1788927528-build-e4c36895\n" +
	"muxcode send spawn-e0035225 file-change-ack \"File change notification received for tools/muxcode/daemon/daemon.go.\" --type response --reply-to 1788927996-spawn-e0035225-ce425ee3\n" +
	"muxcode send spawn-e0035225 file-change-ack \"File change notification received for tools/muxcode/bus/codex_hooks.go.\" --type response --reply-to 1788928045-spawn-e0035225-8df5d3c2\n" +
	"./build.sh\n" +
	"muxcode send edit build-result \"./build.sh completed successfully (exit 0).\" --type response --reply-to 1788928073-daemon-12bed8ee"

func TestCheckEvidenceGuard_LiveBundledCallDenied(t *testing.T) {
	d := CheckEvidenceGuard("build", liveBundledBuildCall)
	if d == nil || !d.Blocked {
		t.Fatalf("CheckEvidenceGuard(build, live call) = %+v, want blocked", d)
	}
	for _, want := range []string{"build command must be the only statement", "`./build.sh`", "4 other statement(s)", "separate call"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason missing %q: %q", want, d.Reason)
		}
	}
	// The row the hook would have written for this call: none — it classifies
	// as a bus command, which is why the guard has to refuse it up front.
	if got := ClassifyCommand(liveBundledBuildCall); got != CmdBus {
		t.Errorf("ClassifyCommand(live call) = %v, want CmdBus", got)
	}
}

func TestCheckEvidenceGuard_BundledDenied(t *testing.T) {
	cases := []struct {
		role, command, kind, stmt string
	}{
		{"build", "./build.sh && go vet ./...", "build", "./build.sh"},
		{"build", "./build.sh 2>&1 | tail -20", "build", "./build.sh 2>&1"},
		{"build", "cd tools/muxcode && go build ./... && echo done", "build", "go build ./..."},
		// A cd joined by `;` is not the exempt prefix: the classifier's
		// stripCommandPrefix misses it too, so no row would follow.
		{"build", "cd /tmp; ./build.sh && echo done", "build", "./build.sh"},
		{"build", "cd /tmp; ./build.sh", "build", "./build.sh"},
		{"build", "set -o pipefail; ./build.sh 2>&1 | tee /tmp/build.log", "build", "./build.sh 2>&1"},
		{"test", "./test.sh; echo EXIT=$?", "test", "./test.sh"},
		{"test", "go test ./... 2>&1 | tail -40", "test", "go test ./... 2>&1"},
		{"test", "muxcode send edit ack \"got it\" --type response --reply-to 1-edit-a; ./test.sh", "test", "./test.sh"},
		{"test", "go vet ./... && go test ./...", "test", "go vet ./..."},
		{"test", "go vet ./... && echo vet-ok", "test", "go vet ./..."},
		{"deploy", "cdk deploy --all || echo failed", "deploy", "cdk deploy --all"},
		{"deploy", "./build.sh && cdk diff", "build", "./build.sh"},
	}
	for _, c := range cases {
		d := CheckEvidenceGuard(c.role, c.command)
		if d == nil || !d.Blocked {
			t.Errorf("CheckEvidenceGuard(%s, %q) = %+v, want blocked", c.role, c.command, d)
			continue
		}
		if !strings.Contains(d.Reason, "the "+c.kind+" command must be the only statement") {
			t.Errorf("CheckEvidenceGuard(%s, %q) names wrong kind: %q", c.role, c.command, d.Reason)
		}
		if !strings.Contains(d.Reason, "`"+c.stmt+"`") {
			t.Errorf("CheckEvidenceGuard(%s, %q) does not name %q: %q", c.role, c.command, c.stmt, d.Reason)
		}
	}
}

// A backgrounded build returns at launch, so the hook would record the launch
// as the verdict. The foreground form of each is the positive control; the
// `0<&0` pair pins that an input fd duplication is not a trailing `&`.
func TestCheckEvidenceGuard_BackgroundDenied(t *testing.T) {
	cases := []struct{ role, background, foreground string }{
		{"build", "./build.sh &", "./build.sh"},
		{"build", "./build.sh 2>&1 &", "./build.sh 2>&1"},
		{"build", "./build.sh 0<&0 &", "./build.sh 0<&0"},
		{"build", "cd tools/muxcode && go build ./... &", "cd tools/muxcode && go build ./..."},
		{"test", "./test.sh &", "./test.sh"},
		{"deploy", "cdk deploy --all &", "cdk deploy --all"},
	}
	for _, c := range cases {
		d := CheckEvidenceGuard(c.role, c.background)
		if d == nil || !d.Blocked {
			t.Errorf("CheckEvidenceGuard(%s, %q) = %+v, want blocked", c.role, c.background, d)
		} else if !strings.Contains(d.Reason, "in the background") {
			t.Errorf("CheckEvidenceGuard(%s, %q) reason is not the background one: %q", c.role, c.background, d.Reason)
		}
		if d := CheckEvidenceGuard(c.role, c.foreground); d != nil {
			t.Errorf("CheckEvidenceGuard(%s, %q) = blocked, want allowed: %q", c.role, c.foreground, d.Reason)
		}
	}
}

func TestCheckEvidenceGuard_LoneCommandAllowed(t *testing.T) {
	allowed := []string{
		"./build.sh",
		"./build.sh 2>&1",
		"./build.sh >/tmp/build.log 2>&1",
		"./build.sh 0<&0",
		"go test ./... <&-",
		"go vet ./...",
		"cd tools/muxcode && go build ./...",
		"GOFLAGS=-mod=mod go build ./...",
		"bash ./build.sh",
		"make 2>&1",
		"./test.sh 2>&1",
		"go test ./... -run 'TestA|TestB' -v",
		"go build -ldflags \"-X main.v=$(git describe; echo x)\" .",
		"cdk deploy --all",
		"cdk diff",
	}
	for _, cmd := range allowed {
		for _, role := range []string{"build", "test", "deploy"} {
			if d := CheckEvidenceGuard(role, cmd); d != nil {
				t.Errorf("CheckEvidenceGuard(%s, %q) = blocked, want allowed: %q", role, cmd, d.Reason)
			}
		}
	}
}

// Negative control for the bundling detector: a compound with no build, test
// or deploy statement is not the guard's business — the definition's own
// log-and-report sequence is exactly such a compound.
func TestCheckEvidenceGuard_NoEvidenceStatementAllowed(t *testing.T) {
	allowed := []string{
		"muxcode send edit build-result \"ok\" --type response --reply-to 1-a; muxcode log build \"Build summary\" --exit-code 0 --command \"./build.sh\"",
		"tmpfile=$(mktemp /tmp/muxcode-log-XXXXXX.txt)\necho \"summary\" > \"$tmpfile\"\nrm -f \"$tmpfile\"",
		"gofmt -l . ; ls | head -5",
		"echo 'go test ./... | tail' && echo \"./build.sh; rm x\"",
	}
	for _, cmd := range allowed {
		for _, role := range []string{"build", "test", "deploy"} {
			if d := CheckEvidenceGuard(role, cmd); d != nil {
				t.Errorf("CheckEvidenceGuard(%s, %q) = blocked, want allowed: %q", role, cmd, d.Reason)
			}
		}
	}
}

func TestCheckEvidenceGuard_RoleScope(t *testing.T) {
	bundled := "./build.sh && echo done"
	for _, role := range []string{"edit", "plan", "run", "watch", "commit", "review", "serve", ""} {
		if HasEvidenceGuard(role) {
			t.Errorf("HasEvidenceGuard(%q) = true, want false", role)
		}
		if d := CheckEvidenceGuard(role, bundled); d != nil {
			t.Errorf("CheckEvidenceGuard(%q, bundled) = %+v, want nil", role, d)
		}
	}
	for _, role := range []string{"build", "test", "deploy"} {
		if !HasEvidenceGuard(role) {
			t.Errorf("HasEvidenceGuard(%q) = false, want true", role)
		}
	}
}

func TestSplitShellStatements(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a; b && c || d | e\nf", []string{"a", "b", "c", "d", "e", "f"}},
		{"echo 'a;b' && echo \"c|d\"", []string{"echo 'a;b'", "echo \"c|d\""}},
		{"echo \"a\\\";b\" ; c", []string{"echo \"a\\\";b\"", "c"}},
		{"x=$(echo a; echo b) && y", []string{"x=$(echo a; echo b)", "y"}},
		{"(cd x && make) | tee log", []string{"(cd x && make)", "tee log"}},
		{"echo `date; date` && z", []string{"echo `date; date`", "z"}},
		{"./build.sh 2>&1 >| out", []string{"./build.sh 2>&1 >| out"}},
		{"cmd &> log", []string{"cmd &> log"}},
		{"./build.sh 0<&0", []string{"./build.sh 0<&0"}},
		{"cmd <&3 2>&1 &", []string{"cmd <&3 2>&1"}},
		{"cmd &", []string{"cmd"}},
		{"cmd1 & cmd2", []string{"cmd1", "cmd2"}},
		{"echo a \\\n  b", []string{"echo a \\\n  b"}},
		{"echo a\\;b; c", []string{"echo a\\;b", "c"}},
		{"  ", nil},
	}
	for _, c := range cases {
		if got, _ := parseShellStatements(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseShellStatements(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A pattern head ends at an executable boundary: the 2026-09-09 gofmt of a
// _test.go file must not classify as a test run, while an adjacent operator
// and a wrapper still do. Every positive is a real executable at the head.
func TestClassifyCommand_PatternHeadAtExecutableBoundary(t *testing.T) {
	cases := []struct {
		command string
		want    CommandType
	}{
		{"gofmt -l tools/muxcode/bus/evidence_guard_test.go", CmdUnknown},
		{"gofmt -l tools/muxcode/bus tools/muxcode/cmd", CmdUnknown},
		{"golangci-lint run ./... --tests", CmdUnknown},
		{"tscfoo --build", CmdUnknown},
		{"makefile-lint .", CmdUnknown},
		{"bashful ./build.sh", CmdUnknown},
		{"go test ./...", CmdTest},
		{"go vet ./...", CmdTestPrecheck},
		{"go build ./...", CmdBuild},
		{"make", CmdBuild},
		{"make install", CmdBuild},
		{"./build.sh 2>&1", CmdBuild},
		{"./build.sh>/tmp/build.log", CmdBuild},
		{"./build.sh;echo done", CmdBuild},
		{"bash ./build.sh", CmdBuild},
		{"npx jest --ci", CmdTest},
		{"cd tools/muxcode && go test ./...", CmdTest},
		{"GOFLAGS=-mod=mod go build ./...", CmdBuild},
	}
	for _, c := range cases {
		if got := ClassifyCommand(c.command); got != c.want {
			t.Errorf("ClassifyCommand(%q) = %v, want %v", c.command, got, c.want)
		}
	}
	if kind := evidenceStatementKind("gofmt -l tools/muxcode/bus/evidence_guard_test.go"); kind != "" {
		t.Errorf("evidenceStatementKind(gofmt on _test.go) = %q, want none", kind)
	}
}

// The documented multiword literal overrides (docs/configuration.md) carry
// their whole literal as the head and must keep matching at the boundary.
func TestClassifyCommand_MultiwordLiteralOverrides(t *testing.T) {
	t.Setenv("MUXCODE_TEST_PATTERNS", "go test|python -m pytest|cargo test")
	t.Setenv("MUXCODE_BUILD_PATTERNS", "cargo build|./build.sh")
	cases := []struct {
		command string
		want    CommandType
	}{
		{"go test ./...", CmdTest},
		{"go test", CmdTest},
		{"python -m pytest -q tests/", CmdTest},
		{"cargo test --workspace", CmdTest},
		{"cargo build --release", CmdBuild},
		{"./build.sh>/tmp/build.log 2>&1", CmdBuild},
		{"go testing-helper ./...", CmdUnknown},
		{"gotest ./...", CmdUnknown},
		{"gofmt -l tools/muxcode/bus/evidence_guard_test.go", CmdUnknown},
	}
	for _, c := range cases {
		if got := ClassifyCommand(c.command); got != c.want {
			t.Errorf("ClassifyCommand(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

func TestParseShellStatements_Separators(t *testing.T) {
	cases := []struct {
		in    string
		stmts []string
		seps  []string
	}{
		{"a && b; c | d || e & f\ng", []string{"a", "b", "c", "d", "e", "f", "g"}, []string{"&&", ";", "|", "||", "&", "\n", ""}},
		{"x 2>&1 &> y >| z", []string{"x 2>&1 &> y >| z"}, []string{""}},
		{"x 0<&0 2>&1", []string{"x 0<&0 2>&1"}, []string{""}},
		{"x 0<&0 &", []string{"x 0<&0"}, []string{"&"}},
		{"cd /tmp && ./build.sh", []string{"cd /tmp", "./build.sh"}, []string{"&&", ""}},
		{"cd /tmp; ./build.sh", []string{"cd /tmp", "./build.sh"}, []string{";", ""}},
		{"./build.sh &", []string{"./build.sh"}, []string{"&"}},
	}
	for _, c := range cases {
		stmts, seps := parseShellStatements(c.in)
		if !reflect.DeepEqual(stmts, c.stmts) || !reflect.DeepEqual(seps, c.seps) {
			t.Errorf("parseShellStatements(%q) = %q %q, want %q %q", c.in, stmts, seps, c.stmts, c.seps)
		}
	}
}
