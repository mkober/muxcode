package bus

import (
	"fmt"
	"sort"
	"strings"
)

// Popups are the transient overlays bound in config/tmux.conf — they run a
// command and close, with none of the PID tracking or toggle behaviour of a
// registered modal. They live here so their size is resolved by the auto-fit
// tiers instead of being hard-coded as a percentage in the tmux binding, which
// on a wide terminal produced popups several times wider than their content.
var popupRegistry = map[string]ModalConfig{}

func init() {
	for _, cfg := range DefaultPopupConfigs() {
		popupRegistry[cfg.Name] = cfg
	}
}

const pressAnyKey = `; echo; read -n1 -p "Press any key..."`

// DefaultPopupConfigs returns the built-in popup definitions. Width and Height
// stay as the percentages the bindings used before, and remain the fallback
// whenever the client size is unknown; the tier fields decide what happens
// when it is known.
func DefaultPopupConfigs() []ModalConfig {
	return []ModalConfig{
		{
			Name: "session-picker", Title: " New Session ",
			Width: "60%", Height: "50%",
			Command:  "TMUX_POPUP=1 muxcode",
			Measurer: MeasureProjectPicker,
		},
		{
			Name: "switch-session", Title: " Switch Session ",
			Width: "60%", Height: "50%",
			Command:  "muxcode-switch-session.sh",
			Measurer: MeasureSwitchSession,
		},
		{
			Name: "agent-status", Title: " Agent Status ",
			Width: "70%", Height: "60%",
			Command:  "muxcode status" + pressAnyKey,
			Measurer: MeasureAgentStatus,
		},
		{
			Name: "agent-history", Title: " History: {1} ",
			Width: "80%", Height: "70%",
			Command: "muxcode history {1}" + pressAnyKey,
			AutoCap: true, // arbitrary history payloads
		},
		{
			Name: "memory-context", Title: " Memory ",
			Width: "80%", Height: "70%",
			Command:  "muxcode memory context" + pressAnyKey,
			Measurer: MeasureMemoryContext,
		},
		{
			Name: "spawn-agent", Title: " Spawn: {1} ",
			Width: "70%", Height: "50%",
			Command: `muxcode spawn start {1} "{2}"` + pressAnyKey,
			AutoCap: true, // arbitrary spawn output
		},
		{
			Name: "proc-list", Title: " Processes ",
			Width: "70%", Height: "50%",
			Command:  `muxcode proc list; echo; echo "---"; muxcode spawn list` + pressAnyKey,
			Measurer: MeasureProcList,
		},
		{
			Name: "cron-list", Title: " Cron Jobs ",
			Width: "70%", Height: "50%",
			Command:  "muxcode cron list" + pressAnyKey,
			Measurer: MeasureCronList,
		},
		{
			Name: "remote-sessions", Title: " Sessions ",
			Width: "80%", Height: "80%",
			Command:  "muxcode remote",
			Measurer: MeasureRemoteSessions,
		},
		{
			Name: "save-memory", Title: " Save Memory ",
			Width: "70%", Height: "50%",
			Command:  "muxcode memory context" + pressAnyKey,
			Measurer: MeasureMemoryContext,
		},
		{
			Name: "edit-config", Title: " Edit Config ",
			Width: "80%", Height: "80%",
			Command: "nvim ~/.config/muxcode/tmux.conf",
			AutoCap: true, // an editor sizes itself to whatever it is given
		},
	}
}

// GetPopup returns a registered popup config by name.
func GetPopup(name string) (ModalConfig, bool) {
	cfg, ok := popupRegistry[name]
	return cfg, ok
}

// PopupNames returns the registered popup names, sorted.
func PopupNames() []string {
	names := make([]string, 0, len(popupRegistry))
	for name := range popupRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// expandPopupArgs substitutes {1}, {2}, ... with the trailing arguments, which
// is how the tmux command-prompt bindings pass a role or task through.
func expandPopupArgs(s string, args []string) string {
	for i, a := range args {
		s = strings.ReplaceAll(s, fmt.Sprintf("{%d}", i+1), a)
	}
	return s
}

// BuildPopupCommand builds the tmux display-popup argument list for a popup,
// resolving its size through the auto-fit tiers first.
func BuildPopupCommand(cfg ModalConfig, session, sizeFlag string, args []string) []string {
	cfg.Title = expandPopupArgs(cfg.Title, args)

	width, height := ResolveSizeIn(cfg, sizeFlag, session)

	out := popupFrameArgs(width, height, cfg.Title)
	return append(out, expandPopupArgs(cfg.Command, args))
}

// OpenPopup opens a registered popup.
func OpenPopup(session, name, sizeFlag string, args []string) error {
	cfg, ok := GetPopup(name)
	if !ok {
		return fmt.Errorf("unknown popup: %s (known: %s)", name, strings.Join(PopupNames(), ", "))
	}
	return TmuxRun(BuildPopupCommand(cfg, session, sizeFlag, args)...)
}
