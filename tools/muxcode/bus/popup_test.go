package bus

import (
	"strconv"
	"testing"
)

// Every popup must resolve to a real command and a tier, or the tmux binding
// that routes through it silently opens an empty overlay.
func TestDefaultPopupConfigs_Complete(t *testing.T) {
	for _, cfg := range DefaultPopupConfigs() {
		if cfg.Name == "" || cfg.Command == "" {
			t.Errorf("popup %q has an empty name or command", cfg.Name)
		}
		if cfg.Width == "" || cfg.Height == "" {
			t.Errorf("popup %q lacks the percentage fallback used when the client size is unknown", cfg.Name)
		}
		if cfg.Measurer == nil && !cfg.AutoCap {
			t.Errorf("popup %q opts into neither tier, so it would keep the unbounded percentage", cfg.Name)
		}
	}
}

func TestExpandPopupArgs(t *testing.T) {
	tests := []struct {
		in   string
		args []string
		want string
	}{
		{"muxcode history {1}", []string{"build"}, "muxcode history build"},
		{`muxcode spawn start {1} "{2}"`, []string{"review", "check the diff"}, `muxcode spawn start review "check the diff"`},
		{" History: {1} ", []string{"test"}, " History: test "},
		{"no placeholders", []string{"x"}, "no placeholders"},
		{"muxcode history {1}", nil, "muxcode history {1}"},
	}
	for _, tt := range tests {
		if got := expandPopupArgs(tt.in, tt.args); got != tt.want {
			t.Errorf("expandPopupArgs(%q, %v) = %q, want %q", tt.in, tt.args, got, tt.want)
		}
	}
}

// The fitted popup must reach tmux as absolute columns — a percentage here
// would mean the whole feature silently did nothing.
func TestBuildPopupCommand_UsesAbsoluteSize(t *testing.T) {
	stubClient(t, 317, 80)
	cfg := ModalConfig{
		Name: "picker", Title: " New Session ", Width: "60%", Height: "50%",
		Command: "muxcode", Measurer: fixedMeasurer(55, 20),
	}
	args := BuildPopupCommand(cfg, "s", "", nil)

	var width string
	for i, a := range args {
		if a == "-w" && i+1 < len(args) {
			width = args[i+1]
		}
	}
	wantW := strconv.Itoa(55 + PopupChromeCols + defaultModalPadCols)
	if width != wantW {
		t.Errorf("expected absolute width %s, got %q (args: %v)", wantW, width, args)
	}
	if args[len(args)-1] != "muxcode" {
		t.Errorf("expected the command last, got %q", args[len(args)-1])
	}
}

func TestBuildPopupCommand_ExpandsTitleAndCommand(t *testing.T) {
	stubClient(t, 317, 80)
	cfg := ModalConfig{
		Name: "agent-history", Title: " History: {1} ", Width: "80%", Height: "70%",
		Command: "muxcode history {1}", AutoCap: true,
	}
	args := BuildPopupCommand(cfg, "s", "", []string{"build"})

	joined := args[len(args)-1]
	if joined != "muxcode history build" {
		t.Errorf("command not expanded: %q", joined)
	}
	for i, a := range args {
		if a == "-T" && args[i+1] != " History: build " {
			t.Errorf("title not expanded: %q", args[i+1])
		}
	}
}

func TestGetPopup_Unknown(t *testing.T) {
	if _, ok := GetPopup("no-such-popup"); ok {
		t.Error("expected unknown popup to report not found")
	}
	if len(PopupNames()) == 0 {
		t.Error("expected registered popups")
	}
}
