package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Dracula popup style constants — used by BuildPopupArgs and documented in
// config/tmux.conf for static display-popup calls. Requires tmux >= 3.3.
const (
	PopupBorderStyle = "rounded"      // tmux display-popup -b value
	PopupBorderColor = "fg=colour141" // Dracula purple (#bd93f9)
)

// PopupStyleArgs returns the tmux display-popup arguments for Dracula-themed
// modal borders. Returns nil if the tmux version doesn't support popup styling.
func PopupStyleArgs() []string {
	if !TmuxSupportsPopupStyle() {
		return nil
	}
	return []string{"-b", PopupBorderStyle, "-S", PopupBorderColor}
}

// Popup chrome and auto-fit clamps. tmux display-popup has no content-fit
// mode — -w/-h take only an absolute count or a percentage — so a popup that
// should hug its content must be measured and clamped here before the size
// reaches tmux.
const (
	PopupChromeCols = 2 // rounded border, one cell each side
	PopupChromeRows = 2 // rounded border, one row top and bottom

	defaultModalMinCols = 40
	defaultModalMaxCols = 160
	modalMinRows        = 10

	// A popup never occupies more than this share of the client, so it always
	// reads as an overlay rather than a replacement for the screen.
	modalMaxClientPct = 90
)

// ModalMinCols returns the auto-fit width floor, overridable via
// MUXCODE_MODAL_MIN_COLS.
func ModalMinCols() int { return modalColsEnv("MUXCODE_MODAL_MIN_COLS", defaultModalMinCols) }

// ModalMaxCols returns the auto-fit width cap, overridable via
// MUXCODE_MODAL_MAX_COLS.
func ModalMaxCols() int { return modalColsEnv("MUXCODE_MODAL_MAX_COLS", defaultModalMaxCols) }

func modalColsEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// FitSize converts a measured content size into popup dimensions: it adds the
// border chrome, then applies the width floor/cap and the share-of-client
// ceiling. The title floor is applied by the caller afterwards, against the
// result, since FitSize has no view of the modal's title.
//
// Returns (0, 0) to mean "no auto-fit available" — when the client size could
// not be resolved, or the measurer produced nothing. That sentinel is why the
// clamps below never bottom out at zero for a client that did resolve: a
// clamped-to-nothing result would be indistinguishable from an absent one and
// would silently fall back to the percentage defaults.
func FitSize(contentW, contentH, clientW, clientH int) (w, h int) {
	if clientW <= 0 || clientH <= 0 || contentW <= 0 || contentH <= 0 {
		return 0, 0
	}

	// Capping after the floor means an inverted range — a configured floor
	// above the cap — resolves in favour of the cap, which is the bound that
	// exists to prevent the overshoot.
	w = min(max(contentW+PopupChromeCols, ModalMinCols()), ModalMaxCols())
	h = max(contentH+PopupChromeRows, modalMinRows)

	// The share-of-client ceiling is applied last so it outranks both floors —
	// a popup must never be wider or taller than the terminal showing it. Each
	// ceiling is itself floored at 1: a tiny client must not clamp the result
	// down onto the (0, 0) sentinel.
	return min(w, max(clientW*modalMaxClientPct/100, 1)),
		min(h, max(clientH*modalMaxClientPct/100, 1))
}

// ModalConfig defines a modal window configuration.
type ModalConfig struct {
	Name    string               // unique identifier (e.g. "api", "logs", "memory")
	Title   string               // tmux popup title bar text
	Width   string               // modal width (e.g. "62%")
	Height  string               // modal height (e.g. "62%")
	Command string               // primary command to run in the modal
	Split   *ModalSplit          // optional pane split
	Sizes   map[string][2]string // size presets: "compact"->["60%","50%"], "full"->["95%","95%"]
	Role    string               // bus role name for inbox routing (empty = no bus integration)

	// Auto-sizing tier. A modal opts in explicitly: with neither field set it
	// keeps the legacy percentage sizing, which is what lets existing configs
	// and tests behave exactly as before.
	Measurer ContentMeasurer // fit tier — measure the content it will render
	AutoCap  bool            // cap tier — convert the percentage to absolute and clamp
}

// ModalSplit defines a pane split inside a modal.
type ModalSplit struct {
	Direction string // "v" (vertical) or "h" (horizontal)
	Size      string // size of the secondary pane (e.g. "20%")
	Command   string // command for the secondary pane
	Primary   string // "top" or "bottom" / "left" or "right" — which pane gets focus
}

// modalRegistry stores registered modal configs by name.
var modalRegistry = map[string]ModalConfig{}

func init() {
	for _, cfg := range DefaultModalConfigs() {
		RegisterModal(cfg)
	}
}

// DefaultModalConfigs returns the built-in modal configurations.
func DefaultModalConfigs() []ModalConfig {
	return []ModalConfig{
		{
			Name:    "api",
			Title:   " API Testing ",
			Width:   "62%",
			Height:  "62%",
			Command: "muxcode agent launch api",
			Split: &ModalSplit{
				Direction: "v",
				Size:      "20%",
				Command:   "muxcode console api",
				Primary:   "top",
			},
			Sizes: map[string][2]string{
				"compact": {"50%", "40%"},
				"full":    {"95%", "95%"},
			},
			Role: "api",
		},
		{
			Name:    "provider",
			Title:   " Provider Selector ",
			Width:   "50%",
			Height:  "60%",
			Command: providerModalCommand(),
			Sizes: map[string][2]string{
				"compact": {"40%", "50%"},
				"full":    {"60%", "70%"},
			},
		},
	}
}

// providerModalCommand builds the command for the provider selector modal.
// The reload runs directly inside the Go binary (no trigger file needed).
func providerModalCommand() string {
	return `muxcode provider-select`
}

// RegisterModal adds a modal config to the registry.
func RegisterModal(cfg ModalConfig) {
	modalRegistry[cfg.Name] = cfg
}

// GetModal returns a registered modal config by name.
func GetModal(name string) (ModalConfig, bool) {
	cfg, ok := modalRegistry[name]
	return cfg, ok
}

// ListModals returns all registered modal configs sorted by name.
func ListModals() []ModalConfig {
	configs := make([]ModalConfig, 0, len(modalRegistry))
	for _, cfg := range modalRegistry {
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs
}

// IsModalRole returns true if the role is registered as a modal.
func IsModalRole(role string) bool {
	for _, cfg := range modalRegistry {
		if cfg.Role == role {
			return true
		}
	}
	return false
}

// explicitSize resolves the tiers the user sets directly — CLI dimensions, a
// named preset, then the per-modal env var. The bool is false when the user
// expressed no preference, leaving the caller to auto-fit or fall back.
func explicitSize(cfg ModalConfig, sizeFlag string) (string, string, bool) {
	if sizeFlag != "" {
		if w, h, ok := parseSizeWidthHeight(sizeFlag); ok {
			return w, h, true
		}
		if dims, ok := cfg.Sizes[sizeFlag]; ok {
			return dims[0], dims[1], true
		}
	}

	envKey := "MUXCODE_MODAL_SIZE_" + strings.ToUpper(strings.ReplaceAll(cfg.Name, "-", "_"))
	if envVal := os.Getenv(envKey); envVal != "" {
		if w, h, ok := parseSizeWidthHeight(envVal); ok {
			return w, h, true
		}
	}
	return "", "", false
}

// ResolveSize resolves the modal size from the 4-tier priority:
// 1. CLI --size WxH (explicit dimensions)
// 2. CLI --size preset (named preset from Sizes map)
// 3. MUXCODE_MODAL_SIZE_<NAME> env var (WxH)
// 4. Config default (Width, Height)
func ResolveSize(cfg ModalConfig, sizeFlag string) (width, height string) {
	if w, h, ok := explicitSize(cfg, sizeFlag); ok {
		return w, h
	}
	return cfg.Width, cfg.Height
}

// ResolveSizeIn is ResolveSize with the session needed by the auto-fit tier.
// Auto-fit sits below the env var and above the config default: it must never
// outrank a size the user asked for explicitly.
func ResolveSizeIn(cfg ModalConfig, sizeFlag, session string) (width, height string) {
	if w, h, ok := explicitSize(cfg, sizeFlag); ok {
		return w, h
	}
	if w, h, ok := autoFitSize(cfg, session); ok {
		return w, h
	}
	return cfg.Width, cfg.Height
}

// clientDimensions is the client-size source, overridable in tests so sizing
// is deterministic whether or not the suite runs inside tmux.
var clientDimensions = resolveClientDimensions

// resolveClientDimensions reads MUXCODE_MODAL_CLIENT_SIZE ("317x80") before
// asking tmux, so sizing can be exercised and debugged from a shell without an
// attached client of that size.
func resolveClientDimensions() (int, int, error) {
	if v := os.Getenv("MUXCODE_MODAL_CLIENT_SIZE"); v != "" {
		if ws, hs, ok := parseSizeWidthHeight(v); ok {
			w, werr := strconv.Atoi(strings.TrimSuffix(ws, "%"))
			h, herr := strconv.Atoi(strings.TrimSuffix(hs, "%"))
			if werr == nil && herr == nil && w > 0 && h > 0 {
				return w, h, nil
			}
		}
	}
	return TmuxClientDimensions()
}

// autoFitSize resolves the fit or cap tier to absolute dimensions. The bool is
// false whenever auto-sizing cannot answer — the modal did not opt in, the
// client size is unknown, or the measurer found nothing — leaving the caller
// on the percentage default.
func autoFitSize(cfg ModalConfig, session string) (string, string, bool) {
	if cfg.Measurer == nil && !cfg.AutoCap {
		return "", "", false
	}

	clientW, clientH, err := clientDimensions()
	if err != nil || clientW <= 0 || clientH <= 0 {
		return "", "", false
	}

	var w, h int
	if cfg.Measurer != nil {
		cols, rows := cfg.Measurer(session)
		w, h = FitSize(cols, rows, clientW, clientH)
	}
	if w == 0 {
		w, h = capSize(cfg, clientW, clientH)
	}
	if w == 0 || h == 0 {
		return "", "", false
	}

	// The title renders inside the top border, so a popup narrower than its
	// title would clip it. The client bound still wins over the title.
	w = min(max(w, titleFloorCols(cfg.Title)), clientW)

	return strconv.Itoa(w), strconv.Itoa(h), true
}

// capSize converts a percentage default into absolute dimensions and applies
// the same cap as the fit tier, for modals whose content cannot be measured.
func capSize(cfg ModalConfig, clientW, clientH int) (int, int) {
	w := absDimension(cfg.Width, clientW)
	h := absDimension(cfg.Height, clientH)
	if w == 0 || h == 0 {
		return 0, 0
	}
	// Same floors as the fit tier: a modal configured with a small percentage
	// would otherwise open unusably small on a narrow client.
	w = min(min(max(w, ModalMinCols()), ModalMaxCols()), max(clientW*modalMaxClientPct/100, 1))
	h = min(max(h, modalMinRows), max(clientH*modalMaxClientPct/100, 1))
	return w, h
}

// absDimension resolves "70%" against total, or a bare "120" as itself.
// Returns 0 when the value is neither.
func absDimension(dim string, total int) int {
	if dim == "" {
		return 0
	}
	if strings.HasSuffix(dim, "%") {
		pct, err := strconv.Atoi(strings.TrimSuffix(dim, "%"))
		if err != nil || pct <= 0 {
			return 0
		}
		return max(total*pct/100, 1)
	}
	n, err := strconv.Atoi(dim)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// titleFloorCols is the width a popup needs to show its title: the visible
// title plus the two corner cells of the border.
func titleFloorCols(title string) int {
	if title == "" {
		return 0
	}
	cols, _ := MeasureText(title)
	return cols + 2
}

// tmuxVersionRunner is the function used to get the tmux version.
// Override in tests to avoid invoking tmux.
var tmuxVersionRunner = defaultTmuxVersion

func defaultTmuxVersion() (string, error) {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TmuxSupportsPopupStyle returns true if the tmux version supports
// display-popup -b (border) and -S (style) flags (tmux >= 3.3).
func TmuxSupportsPopupStyle() bool {
	versionStr, err := tmuxVersionRunner()
	if err != nil {
		return false
	}
	return parseTmuxVersionAtLeast(versionStr, 3, 3)
}

// parseTmuxVersionAtLeast parses a tmux version string (e.g. "tmux 3.4")
// and returns true if it is >= major.minor.
func parseTmuxVersionAtLeast(versionStr string, major, minor int) bool {
	// Strip "tmux " prefix and any suffix like "-rc" or "next-"
	v := strings.TrimPrefix(versionStr, "tmux ")
	v = strings.TrimPrefix(v, "next-")

	parts := strings.SplitN(v, ".", 2)
	if len(parts) < 1 {
		return false
	}

	maj, err := strconv.Atoi(extractDigits(parts[0]))
	if err != nil {
		return false
	}

	min := 0
	if len(parts) >= 2 {
		min, _ = strconv.Atoi(extractDigits(parts[1]))
	}

	if maj != major {
		return maj > major
	}
	return min >= minor
}

// extractDigits returns the leading digits from a string.
func extractDigits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			break
		}
	}
	return b.String()
}

// parseSizeWidthHeight parses a "WxH" size string (e.g. "80%x70%", "120x80").
// Returns width, height, and true if the string is a valid WxH format.
// Both sides must end with '%' or be pure digits to qualify.
func parseSizeWidthHeight(s string) (string, string, bool) {
	// Find the last 'x' that's preceded by a digit or '%' — avoids matching
	// words like "nonexistent" that happen to contain 'x'.
	idx := -1
	for i := len(s) - 1; i > 0; i-- {
		if s[i] == 'x' {
			prev := s[i-1]
			if prev == '%' || (prev >= '0' && prev <= '9') {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return "", "", false
	}

	w, h := s[:idx], s[idx+1:]
	if w == "" || h == "" {
		return "", "", false
	}
	if isDimension(w) && isDimension(h) {
		return w, h, true
	}
	return "", "", false
}

// isDimension returns true if s looks like a CSS-style dimension: digits
// optionally followed by '%' (e.g. "80%", "120").
func isDimension(s string) bool {
	if len(s) == 0 {
		return false
	}
	end := s
	if s[len(s)-1] == '%' {
		end = s[:len(s)-1]
	}
	if len(end) == 0 {
		return false
	}
	for _, c := range end {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// BuildModalCommand builds the command string to run inside the modal popup.
// For non-split modals, returns the command directly.
// For split modals, creates a temporary tmux session on a separate server
// (-L muxcode-modal) to get real split panes inside the popup overlay.
// tmux display-popup doesn't support split-window in its single popup pane,
// so the separate server provides a full multi-pane environment.
func BuildModalCommand(cfg ModalConfig) string {
	if cfg.Split == nil {
		return cfg.Command
	}

	s := cfg.Split
	splitFlag := "-v"
	if s.Direction == "h" {
		splitFlag = "-h"
	}

	// Use relative pane targets ({top}/{bottom}/{left}/{right}) instead of
	// numeric indices — the separate tmux server uses default base-index (1),
	// not the parent session's config, so :0.0 / :0.1 would fail.
	selectPane := "{top}"
	if s.Direction == "h" {
		selectPane = "{left}"
	}
	if s.Primary == "bottom" || s.Primary == "right" {
		if s.Direction == "h" {
			selectPane = "{right}"
		} else {
			selectPane = "{bottom}"
		}
	}

	// Session name based on modal name for uniqueness.
	// Uses -L muxcode-modal for a separate tmux server to avoid conflicts
	// with the parent session. TMUX= unsets the env var so attach works.
	// select-pane before attach persists on the separate server since
	// there's no competing client to reset focus.
	//
	// The primary command is wrapped to kill the nested session on exit —
	// when the LLM agent exits (/exit, Ctrl-C), the entire modal tears
	// down (console pane, session, popup) instead of hanging on the
	// still-running secondary pane.
	primaryWrapped := fmt.Sprintf(
		`%s; tmux -L muxcode-modal kill-session -t "muxcode-modal-%s" 2>/dev/null`,
		cfg.Command, cfg.Name,
	)

	return fmt.Sprintf(
		`MSESS="muxcode-modal-%s"; `+
			`tmux -L muxcode-modal new-session -d -s "$MSESS" '%s'; `+
			`tmux -L muxcode-modal split-window -t "$MSESS" %s -l %s '%s'; `+
			`tmux -L muxcode-modal select-pane -t "%s"; `+
			`TMUX= tmux -L muxcode-modal attach-session -t "$MSESS"; `+
			`tmux -L muxcode-modal kill-session -t "$MSESS" 2>/dev/null`,
		cfg.Name, primaryWrapped, splitFlag, s.Size, s.Command, selectPane,
	)
}

// popupFrameArgs builds the display-popup flags every popup shares: the
// Dracula frame (tmux 3.3+), the resolved size, and the optional title.
func popupFrameArgs(width, height, title string) []string {
	args := []string{"display-popup", "-E", "-d", "#{pane_current_path}"}
	if style := PopupStyleArgs(); style != nil {
		args = append(args, style...)
	}
	args = append(args, "-w", width, "-h", height)
	if title != "" {
		args = append(args, "-T", title)
	}
	return args
}

// BuildPopupArgs builds the tmux display-popup argument list for a modal.
// This is a pure function suitable for testing.
func BuildPopupArgs(cfg ModalConfig, session, sizeFlag string) []string {
	width, height := ResolveSizeIn(cfg, sizeFlag, session)

	args := popupFrameArgs(width, height, cfg.Title)

	// Set MUXCODE_MODAL=1 and track PID; clean up PID file on exit
	pidPath := ModalPidPath(session, cfg.Name)
	modalCmd := BuildModalCommand(cfg)
	wrappedCmd := fmt.Sprintf(
		"echo $$ > %s && MUXCODE_MODAL=1 %s; rm -f %s",
		pidPath, modalCmd, pidPath,
	)

	args = append(args, wrappedCmd)
	return args
}

// OpenModal opens a modal window or closes it if already open (toggle behavior).
func OpenModal(session, name, sizeFlag string) error {
	cfg, ok := GetModal(name)
	if !ok {
		return fmt.Errorf("unknown modal: %s", name)
	}

	// Toggle: if already open, close it
	if IsModalOpen(session, name) {
		return CloseModal(session, name)
	}

	// Ensure modal PID directory exists
	if err := os.MkdirAll(ModalDir(session), 0755); err != nil {
		return fmt.Errorf("create modal dir: %w", err)
	}

	args := BuildPopupArgs(cfg, session, sizeFlag)
	return TmuxRun(args...)
}

// IsModalOpen checks if a modal is currently displayed by verifying its PID file.
func IsModalOpen(session, name string) bool {
	pidPath := ModalPidPath(session, name)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		// Stale/corrupt PID file — clean up
		_ = os.Remove(pidPath)
		return false
	}

	if !CheckProcAlive(pid) {
		// Process dead — clean up stale PID file
		_ = os.Remove(pidPath)
		return false
	}

	return true
}

// CloseModal closes an open modal by sending SIGTERM to its process.
func CloseModal(session, name string) error {
	pidPath := ModalPidPath(session, name)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return nil // not open, nothing to do
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	if CheckProcAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	_ = os.Remove(pidPath)
	return nil
}

// OpenOrSpawn opens a modal if a tmux client is attached, otherwise falls back
// to headless spawn via StartSpawn. Unlike OpenModal, this does NOT toggle —
// if the modal is already open, it returns nil (the agent is already running).
func OpenOrSpawn(session, name, sizeFlag string) error {
	cfg, ok := GetModal(name)
	if !ok {
		return fmt.Errorf("unknown modal: %s", name)
	}

	// Already open — nothing to do (no toggle behavior here)
	if IsModalOpen(session, name) {
		return nil
	}

	// Check for attached clients
	out, err := TmuxOutput("list-clients", "-t", session, "-F", "#{client_name}")
	if err == nil && strings.TrimSpace(out) != "" {
		// Client attached — open modal
		return OpenModal(session, name, sizeFlag)
	}

	// No client — headless spawn fallback
	if cfg.Role != "" {
		_, spawnErr := StartSpawn(session, cfg.Role, "modal-fallback", "modal", false)
		return spawnErr
	}

	return fmt.Errorf("no tmux client attached and modal %q has no role for headless fallback", name)
}

// ModalStatus returns the status of a modal: "open" or "closed".
func ModalStatus(session, name string) string {
	if IsModalOpen(session, name) {
		return "open"
	}
	return "closed"
}

// FormatModalList formats the list of registered modals for display.
func FormatModalList(configs []ModalConfig) string {
	var b strings.Builder

	if len(configs) == 0 {
		b.WriteString("No modals registered.\n")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-12s %-20s %-8s %-8s %-6s %s\n",
		"NAME", "TITLE", "WIDTH", "HEIGHT", "SPLIT", "ROLE"))
	b.WriteString(strings.Repeat("-", 70) + "\n")

	for _, cfg := range configs {
		split := "no"
		if cfg.Split != nil {
			split = cfg.Split.Direction
		}
		role := cfg.Role
		if role == "" {
			role = "-"
		}
		title := strings.TrimSpace(cfg.Title)
		b.WriteString(fmt.Sprintf("%-12s %-20s %-8s %-8s %-6s %s\n",
			cfg.Name, title, cfg.Width, cfg.Height, split, role))
	}

	return b.String()
}

// FormatModalStatus formats the status of a specific modal for display.
func FormatModalStatus(session string, cfg ModalConfig) string {
	var b strings.Builder

	status := ModalStatus(session, cfg.Name)

	b.WriteString(fmt.Sprintf("Modal: %s\n", cfg.Name))
	b.WriteString(fmt.Sprintf("  Title:   %s\n", cfg.Title))
	b.WriteString(fmt.Sprintf("  Status:  %s\n", status))
	b.WriteString(fmt.Sprintf("  Size:    %s x %s\n", cfg.Width, cfg.Height))
	b.WriteString(fmt.Sprintf("  Command: %s\n", cfg.Command))

	if cfg.Split != nil {
		b.WriteString(fmt.Sprintf("  Split:   %s (%s)\n", cfg.Split.Direction, cfg.Split.Size))
		b.WriteString(fmt.Sprintf("  Secondary: %s\n", cfg.Split.Command))
	}

	if cfg.Role != "" {
		b.WriteString(fmt.Sprintf("  Role:    %s\n", cfg.Role))
	}

	if len(cfg.Sizes) > 0 {
		b.WriteString("  Presets:\n")
		names := make([]string, 0, len(cfg.Sizes))
		for name := range cfg.Sizes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			dims := cfg.Sizes[name]
			b.WriteString(fmt.Sprintf("    %-10s %s x %s\n", name, dims[0], dims[1]))
		}
	}

	// PID info if open
	pidPath := ModalPidPath(session, cfg.Name)
	if data, err := os.ReadFile(pidPath); err == nil {
		pidStr := strings.TrimSpace(string(data))
		b.WriteString(fmt.Sprintf("  PID:     %s\n", pidStr))
		b.WriteString(fmt.Sprintf("  PID file: %s\n", pidPath))
	}

	return b.String()
}

// cleanupModalPids removes all modal PID files during session cleanup.
func cleanupModalPids(session string) {
	dir := ModalDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pid") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
