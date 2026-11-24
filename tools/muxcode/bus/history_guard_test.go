package bus

import "testing"

// TestHistoryWriteGuardRefusesWrites covers every cheap write form MUX-185 D3
// names, including the paths only seen as written ($VAR, $(muxcode bus-dir))
// and the forgery that never touches the file: a payload piped into
// `muxcode hook bash`.
func TestHistoryWriteGuardRefusesWrites(t *testing.T) {
	refused := []string{
		`echo '{"source":"hook","exit_code":"0","outcome":"success"}' >> ~/.local/state/muxcode/bus/muxcode-bus-s/test-history.jsonl`,
		`echo x >> "$B/build-history.jsonl"`,
		`echo x >> "$(muxcode bus-dir)/test-history.jsonl"`,
		`echo x > /tmp/muxcode-bus-s/test-history.jsonl`,
		`echo x >| test-history.jsonl`,
		`echo x &> test-history.jsonl`,
		`cd /x && printf '%s\n' row >>test-history.jsonl`,
		`( echo x >> test-history.jsonl )`,
		`echo x | tee -a test-history.jsonl`,
		`echo x | tee /tmp/a.log build-history.jsonl > /dev/null`,
		`sed -i '' 's/"1"/"0"/' test-history.jsonl`,
		`perl -pi -e 's/failure/success/' test-history.jsonl`,
		`cp /tmp/forged.jsonl "$B/test-history.jsonl"`,
		`mv /tmp/forged.jsonl test-history.jsonl`,
		`install -m 644 /tmp/forged.jsonl test-history.jsonl`,
		`ln -sf /tmp/forged.jsonl test-history.jsonl`,
		`dd if=/tmp/forged.jsonl of=test-history.jsonl`,
		`python3 -c "open('/x/test-history.jsonl','a').write('{}\n')"`,
		`node -e "require('fs').appendFileSync('test-history.jsonl', row)"`,
		`perl -e 'open(F, ">>test-history.jsonl"); print F $row'`,
		`awk '{print > "test-history.jsonl"}' /tmp/rows`,
		`cat /tmp/payload.json | muxcode hook bash`,
		`muxcode "hook" "bash" < /tmp/payload.json`,
		`nohup "muxcode" 'hook' 'bash' < /tmp/payload.json`,
		`muxcode hook bash < /tmp/payload.json`,
		`AGENT_ROLE=test /usr/local/bin/muxcode hook bash <<EOF
{"tool_input":{"command":"./test.sh"}}
EOF`,
		`bash -c 'echo x >> test-history.jsonl'`,
		`eval "echo x >> test-history.jsonl"`,
		`ls; echo x >> test-history.jsonl`,
	}
	for _, cmd := range refused {
		if d := CheckHistoryWriteGuard("test", cmd); d == nil || !d.Blocked {
			t.Errorf("not refused: %s", cmd)
		}
	}
}

// TestHistoryWriteGuardAllowsReads is the negative control: a guard that
// refused every mention of a history file would pass the test above.
func TestHistoryWriteGuardAllowsReads(t *testing.T) {
	allowed := []string{
		`cat "$B/test-history.jsonl"`,
		`jq -c 'select(.ts > 5)' test-history.jsonl`,
		`jq . test-history.jsonl > /tmp/rows.json`,
		`tail -5 build-history.jsonl | jq .outcome`,
		`grep -c '"outcome":"failure"' test-history.jsonl`,
		`sed -n '1p' test-history.jsonl`,
		`awk '$1 > 3' test-history.jsonl`,
		`python3 -c "import json; [print(json.loads(l)['ts'] > 0) for l in open('test-history.jsonl')]"`,
		`python3 -c "import sys; sys.stdout.write(open(\"test-history.jsonl\").read())"`,
		`node -e "process.stdout.write(require(\"fs\").readFileSync(\"test-history.jsonl\"))"`,
		`perl -ne 'print if /failure/' test-history.jsonl`,
		`cp test-history.jsonl /tmp/`,
		`wc -l test-history.jsonl 2>&1`,
		`echo x >> /tmp/events.jsonl`,
		`echo x > history.jsonl`,
		`muxcode log test --exit-code 0 "suite passed"`,
		`muxcode send edit test "row written to test-history.jsonl"`,
		`echo "never run echo x >> test-history.jsonl"`,
		`cat > /tmp/notes.md <<EOF
echo x >> test-history.jsonl
EOF`,
		`muxcode hook guard < /tmp/payload.json`,
	}
	for _, cmd := range allowed {
		if d := CheckHistoryWriteGuard("test", cmd); d != nil && d.Blocked {
			t.Errorf("refused a non-write: %s", cmd)
		}
	}
}

// TestGuardDecisionForRefusesHistoryWriteForEveryRole pins the wiring: the
// clause is in the provider-agnostic rule set and binds roles with no
// delegation rules, where CheckGuard's own file-write check never runs.
func TestGuardDecisionForRefusesHistoryWriteForEveryRole(t *testing.T) {
	write := &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: `echo x >> /tmp/muxcode-bus-s/test-history.jsonl`}}
	read := &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: `jq . /tmp/muxcode-bus-s/test-history.jsonl`}}
	for _, role := range []string{"edit", "build", "test", "review", "run", "watch", "commit"} {
		if d := GuardDecisionFor(role, write); d == nil || !d.Blocked {
			t.Errorf("%s: history write allowed", role)
		}
		if d := GuardDecisionFor(role, read); d != nil && d.Blocked {
			t.Errorf("%s: history read refused: %s", role, d.Reason)
		}
	}
}

// TestGuardDecisionForRefusesQuotedHookBash: shellWords keeps quotes, so a
// quoted `"hook" "bash"` must still read as the forged-payload writer — on a
// role with no delegation rules, where nothing else would catch it. The quoted
// `hook guard` beside it is the negative control.
func TestGuardDecisionForRefusesQuotedHookBash(t *testing.T) {
	forged := &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: `muxcode "hook" "bash" < /tmp/payload.json`}}
	guard := &ToolEvent{ToolName: "Bash", ToolInput: ToolInput{Command: `muxcode "hook" "guard" < /tmp/payload.json`}}
	if HasGuardRules("watch") {
		t.Fatal("watch has delegation rules — pick a role without any")
	}
	if d := GuardDecisionFor("watch", forged); d == nil || !d.Blocked {
		t.Error(`quoted muxcode "hook" "bash" allowed`)
	}
	if d := GuardDecisionFor("watch", guard); d != nil && d.Blocked {
		t.Errorf(`quoted muxcode "hook" "guard" refused: %s`, d.Reason)
	}
}
