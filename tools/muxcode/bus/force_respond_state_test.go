package bus

import (
	"os"
	"testing"
)

func TestForceRespondState_RoundTripAndClear(t *testing.T) {
	session := "fr-state-test"
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	if _, ok := ReadForceRespondState(session, "build"); ok {
		t.Fatal("no state file must read as no open episode")
	}

	st := ForceRespondState{Role: "build", Rung: 2, History: []string{"notify@a", "deliver@b"}}
	if err := WriteForceRespondState(session, st); err != nil {
		t.Fatalf("WriteForceRespondState: %v", err)
	}

	got, ok := ReadForceRespondState(session, "build")
	if !ok {
		t.Fatal("expected the episode readable after write")
	}
	if got.Rung != 2 || len(got.History) != 2 || got.History[1] != "deliver@b" {
		t.Errorf("state mangled: %+v", got)
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt must be stamped on write")
	}

	ClearForceRespondState(session, "build")
	if _, ok := ReadForceRespondState(session, "build"); ok {
		t.Error("cleared episode must read as closed")
	}
}
