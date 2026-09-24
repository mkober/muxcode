package bus

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// provenanceCases pairs every manual-launch assertion with its negative
// control: a DescribeRunCreator hardcoded to the user's wording would pass the
// first case alone and mislabel every autonomous run (MUX-182 AC 3 and 4).
var provenanceCases = []struct {
	name, actor, want, mustNot string
}{
	{"manual launch", "", "the user, by hand", "autonomous"},
	{"autonomous launch", "auto", "auto (autonomous)", "the user"},
}

func TestDescribeRunCreator(t *testing.T) {
	for in, want := range map[string]string{
		ActorUser:    "the user, by hand",
		"auto":       "auto (autonomous)",
		"edit":       "edit (autonomous)",
		ActorUnknown: "could not be established",
		"":           "unrecorded",
	} {
		if got := DescribeRunCreator(in); got != want {
			t.Errorf("DescribeRunCreator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProvenanceSurfaces(t *testing.T) {
	for _, tc := range provenanceCases {
		t.Run(tc.name, func(t *testing.T) {
			pinActor(t, tc.actor)
			run := createTestRun(t, linearGraph())

			raw, err := os.ReadFile(graphRunPath(runTestSession, run.ID))
			if err != nil {
				t.Fatal(err)
			}
			var onDisk map[string]any
			if err := json.Unmarshal(raw, &onDisk); err != nil {
				t.Fatal(err)
			}
			if onDisk["provenance"] != tc.want {
				t.Errorf("run.json provenance = %v, want %q", onDisk["provenance"], tc.want)
			}
			if onDisk["created_by"] != run.CreatedBy {
				t.Errorf("run.json created_by = %v, want the raw %q beside it", onDisk["created_by"], run.CreatedBy)
			}

			g, _ := ReadGraphRunGraph(runTestSession, run.ID)
			statuses, _ := ReadAllNodeStatuses(runTestSession, run.ID)
			status := FormatGraphRun(run, g, statuses)
			if !strings.Contains(status, "Launched by: "+tc.want) || strings.Contains(status, tc.mustNot) {
				t.Errorf("graph status does not render %q (or renders %q):\n%s", tc.want, tc.mustNot, status)
			}
			if strings.Contains(status, "Started by:") {
				t.Errorf("graph status still renders the bare actor:\n%s", status)
			}
		})
	}
}

func TestGraphRunCreatedEventNamesProvenance(t *testing.T) {
	for _, tc := range provenanceCases {
		t.Run(tc.name, func(t *testing.T) {
			pinActor(t, tc.actor)
			run := createTestRun(t, linearGraph())
			msgs, _ := Peek(runTestSession, "edit")
			var found bool
			for _, m := range msgs {
				if m.Action != "graph-run-created" || !strings.Contains(m.Payload, run.ID) {
					continue
				}
				found = true
				if !strings.Contains(m.Payload, "launched by: "+tc.want) || strings.Contains(m.Payload, tc.mustNot) {
					t.Errorf("graph-run-created %q does not carry %q (or carries %q)", m.Payload, tc.want, tc.mustNot)
				}
			}
			if !found {
				t.Fatal("no graph-run-created event reached edit")
			}
		})
	}
}

// The decoded run must not change shape: provenance is derived on every write
// and never read back into the struct.
func TestGraphRunJSONRoundTrip(t *testing.T) {
	orig := GraphRun{ID: "r", Template: "t", State: GraphRunRunning, CreatedBy: "auto", CreatedAt: 1}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got GraphRun
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != orig.ID || got.CreatedBy != orig.CreatedBy || got.State != orig.State {
		t.Errorf("round trip = %+v, want %+v", got, orig)
	}
}

func sendFromSpawn(t *testing.T, runID string) Message {
	t.Helper()
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{
		{ID: "1-spawn-abcd1234", SpawnRole: "spawn-abcd1234", RunID: runID, Status: "running"},
	}); err != nil {
		t.Fatal(err)
	}
	m := NewMessage("spawn-abcd1234", "plan", "request", "update-docs", "edit the spec", "")
	m.OriginCreatedBy = ActorUser // a forged claim the bus must discard
	if err := SendNoCC(runTestSession, m); err != nil {
		t.Fatalf("send: %v", err)
	}
	msgs, _ := Peek(runTestSession, "plan")
	for _, got := range msgs {
		if got.ID == m.ID {
			return got
		}
	}
	t.Fatal("spawn message did not reach plan")
	return Message{}
}

func TestSpawnMessageCarriesRunOrigin(t *testing.T) {
	for _, tc := range provenanceCases {
		t.Run(tc.name, func(t *testing.T) {
			pinActor(t, tc.actor)
			run := createTestRun(t, linearGraph())
			got := sendFromSpawn(t, run.ID)

			wantBy := run.CreatedBy
			if got.OriginRun != run.ID || got.OriginCreatedBy != wantBy || got.OriginRunState != GraphRunRunning {
				t.Errorf("origin = run %q by %q state %q, want %q by %q state running",
					got.OriginRun, got.OriginCreatedBy, got.OriginRunState, run.ID, wantBy)
			}
			if got.GraphRun != "" || got.GraphNode != "" {
				t.Errorf("a worker's send must not carry executor provenance: GraphRun=%q GraphNode=%q", got.GraphRun, got.GraphNode)
			}
			line := FormatMessage(got)
			if !strings.Contains(line, "launched by: "+tc.want) || strings.Contains(line, tc.mustNot) {
				t.Errorf("rendered origin lacks %q (or carries %q):\n%s", tc.want, tc.mustNot, line)
			}
		})
	}
}

func TestSpawnMessageFromCancelledRunSaysSo(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())
	run.State = GraphRunCanceled
	if err := atomicWriteJSON(graphRunPath(runTestSession, run.ID), run); err != nil {
		t.Fatal(err)
	}
	out := FormatMessage(sendFromSpawn(t, run.ID))
	if !strings.Contains(out, "run state at send: canceled") || !strings.Contains(out, "do not act on this") {
		t.Errorf("a cancelled run's prompt is not marked as such:\n%s", out)
	}
}

// Negative control for the cancelled warning: a running run's prompt is live work.
func TestSpawnMessageFromRunningRunNotWarned(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())
	if out := FormatMessage(sendFromSpawn(t, run.ID)); strings.Contains(out, "do not act on this") {
		t.Errorf("a running run's prompt carries the cancelled warning:\n%s", out)
	}
}

// TestUnprovenancedMessagesSayNoUserRequest also carries the negative
// controls: edit is the consent boundary, and a plain agent response is not
// a prompt — neither gets an origin line.
func TestUnprovenancedMessagesSayNoUserRequest(t *testing.T) {
	cases := []struct {
		name string
		m    Message
		want string
	}{
		{"spawn with no run", Message{From: "spawn-deadbeef", To: "plan", Type: "request"}, "tied to no graph run — carries no record of a user request"},
		{"agent request", Message{From: "review", To: "plan", Type: "request"}, "sent by review, not by the user"},
		{"daemon request", Message{From: "daemon", To: "plan", Type: "request"}, "sent by daemon, not by the user"},
		{"human prompt", Message{From: "build", To: "prompt", Type: "request", OriginCreatedBy: ActorUser}, "typed by the user at the Prompt surface"},
	}
	for _, tc := range cases {
		if out := FormatMessage(tc.m); !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q in:\n%s", tc.name, tc.want, out)
		}
	}
	for _, m := range []Message{
		{From: "edit", To: "plan", Type: "request"},
		{From: "build", To: "edit", Type: "response"},
	} {
		if out := FormatMessage(m); strings.Contains(out, "Origin:") {
			t.Errorf("%s→%s %s rendered an origin line:\n%s", m.From, m.To, m.Type, out)
		}
	}
}

func TestHumanPromptArrivesMarkedAsTheUser(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-prompt-origin"
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := SendHumanPrompt(session, "build", "approve the gate"); err != nil {
		t.Fatalf("SendHumanPrompt: %v", err)
	}
	// Negative control: an ordinary agent request on the same bus stays unmarked.
	if err := SendNoCC(session, NewMessage("review", "plan", "request", "update-docs", "x", "")); err != nil {
		t.Fatalf("send: %v", err)
	}
	prompts, _ := Peek(session, "prompt")
	if len(prompts) != 1 || !strings.Contains(FormatMessage(prompts[0]), "typed by the user") {
		t.Errorf("human prompt not marked as the user's: %+v", prompts)
	}
	plans, _ := Peek(session, "plan")
	if len(plans) != 1 || plans[0].OriginCreatedBy != "" {
		t.Errorf("agent request carries a human origin: %+v", plans)
	}
}

// The human mark is set only by the bus on SendHumanPrompt's road; a sender
// that writes it itself has it wiped.
func TestStampDiscardsForgedHumanOrigin(t *testing.T) {
	useTempBusDir(t)
	m := Message{From: "review", To: "plan", Type: "request", OriginCreatedBy: ActorUser, OriginRun: "forged"}
	stampMessageOrigin(runTestSession, &m, false)
	if m.OriginCreatedBy != "" || m.OriginRun != "" {
		t.Errorf("forged origin survived the stamp: %+v", m)
	}
	stampMessageOrigin(runTestSession, &m, true)
	if m.OriginCreatedBy != ActorUser {
		t.Errorf("SendHumanPrompt's road not stamped as the user: %+v", m)
	}
}
