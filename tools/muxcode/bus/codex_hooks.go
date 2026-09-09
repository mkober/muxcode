package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Codex hooks (MUX-159): the deterministic chain road for Codex CLI agents.
//
// Codex ships PreToolUse/PostToolUse/Stop/UserPromptSubmit hooks of the same
// shape as Claude Code's. muxcode writes `<repo>/.codex/hooks.json` before a
// codex agent launches, pointing every event at a `muxcode hook` subcommand, so
// chains, console history, guards and inbox delivery run from hooks instead of
// the daemon scraping the pane — the road that produced MUX-154, MUX-009 and
// the fabricated build verdicts of 2026-09-08.
//
// Trust: Codex requires a persisted, hash-based trust decision for a hooks file
// and otherwise skips it with a warning. muxcode passes
// `--dangerously-bypass-hook-trust` instead — the flag exists for "automation
// that already vets hook sources" — but ONLY when the file on disk hashes to
// what muxcode itself wrote. The hash lives under the bus directory
// (agent-reachable only via the bus dir, and a mismatch refuses the launch),
// so an agent that edits the repo-local hooks.json to add its own handler gets
// a refused launch with a `codex-hooks-tampered` lifecycle row, never a trusted
// hook it authored.
//
// Activation is per role and recorded as that hash marker: a marker present
// means the role runs on the hook road, and every `muxcode hook` subprocess
// decides its provider capability from it. No marker, no hooks — the
// scrape road stays byte-for-byte what it was.

// WakeSentence is the fixed prompt injected to wake an idle agent. It carries
// no payload: a Claude agent answers it by reading its inbox, a hook-road codex
// agent has it expanded into context by the UserPromptSubmit hook. A payload
// is never injected as a prompt (MUX-009).
const WakeSentence = "You have new messages"

// CodexHooksMinVersion is the oldest codex CLI verified to run hooks
// (0.153.4, 2026-09-08). No published floor exists, so the gate is empirical.
const CodexHooksMinVersion = "0.153.0"

// ErrCodexHooksTampered reports that .codex/hooks.json no longer hashes to
// what muxcode wrote for this role. The launcher refuses to start the agent.
var ErrCodexHooksTampered = errors.New("codex hooks.json does not match what muxcode wrote")

// codexHooksDefault is the rollout default when no MUXCODE_*_CODEX_HOOKS
// variable is set. On since 2026-09-09, after scripts/test-codex-hooks.sh's
// live section ran green on a real codex (7/7); an ineligible codex still
// falls back to the scrape road, and MUXCODE_CODEX_HOOKS=0 (or the per-role
// variable) opts out.
const codexHooksDefault = true

// CodexHooksPath is the project-scope hooks file, beside .codex/AGENTS.md.
// Project scope because user scope (~/.codex/hooks.json) would fire muxcode's
// hooks in every repo and every codex the developer runs by hand.
func CodexHooksPath() string { return filepath.Join(".codex", "hooks.json") }

type codexHookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type codexHookGroup struct {
	Matcher string             `json:"matcher,omitempty"`
	Hooks   []codexHookHandler `json:"hooks"`
}

// CodexHooksTemplate renders the hooks.json muxcode installs. Every handler is
// a `muxcode hook` subcommand, each of which no-ops unless launched inside a
// muxcode session (BUS_SESSION set) for a role on the hook road, so a
// developer's own codex in the same repo is unaffected by a stale file.
//
// Matchers are regexes over the tool name. Codex reports shell and unified
// exec as `Bash` and file edits as `apply_patch` (verified 0.153.4).
// PreToolUse matches every tool: the guard self-selects by tool name, and an
// MCP call (an Atlassian write) must reach CheckAtlassianMCPGuard — a
// `Bash|apply_patch` matcher let a restricted codex role write Jira unchecked.
func CodexHooksTemplate() []byte {
	cmd := func(sub string, timeout int) []codexHookHandler {
		return []codexHookHandler{{Type: "command", Command: "muxcode hook " + sub, Timeout: timeout}}
	}
	doc := map[string]any{
		"hooks": map[string][]codexHookGroup{
			"PreToolUse":       {{Matcher: ".*", Hooks: cmd("guard", 30)}},
			"PostToolUse":      {{Matcher: "Bash", Hooks: cmd("bash", 60)}, {Matcher: "apply_patch", Hooks: cmd("analyze", 30)}},
			"Stop":             {{Hooks: cmd("stop", 30)}},
			"UserPromptSubmit": {{Hooks: cmd("prompt-submit", 30)}},
		},
	}
	data, _ := json.MarshalIndent(doc, "", "  ")
	return append(data, '\n')
}

// RoleCodexHooksEnvVar names the per-role opt-in variable, e.g.
// MUXCODE_BUILD_CODEX_HOOKS. Hosted aliases share their host's name.
func RoleCodexHooksEnvVar(role string) string {
	r := strings.ToUpper(strings.ReplaceAll(NormalizeBusRole(role), "-", "_"))
	return "MUXCODE_" + r + "_CODEX_HOOKS"
}

// CodexHooksOptIn reports whether the hook road is requested for a role:
// the per-role variable wins, then MUXCODE_CODEX_HOOKS, then the rollout
// default. Values 1/true/on/yes enable, anything else set disables.
func CodexHooksOptIn(role string) bool {
	for _, key := range []string{RoleCodexHooksEnvVar(role), "MUXCODE_CODEX_HOOKS"} {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			return parseBoolish(v)
		}
	}
	return codexHooksDefault
}

func parseBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// codexVersionOutput runs `codex --version`; tests replace it.
var codexVersionOutput = func() (string, error) {
	out, err := exec.Command("codex", "--version").Output()
	return string(out), err
}

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseCodexVersion extracts the first x.y.z triple from `codex --version`
// output ("codex-cli 0.153.4").
func ParseCodexVersion(s string) (major, minor, patch int, ok bool) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])
	return major, minor, patch, true
}

// CodexVersionAtLeast reports whether the version in have is >= min. An
// unparseable have is never at least anything — an unknown codex stays on the
// scrape road rather than being granted a trust bypass on a guess.
func CodexVersionAtLeast(have, min string) bool {
	hM, hm, hp, ok := ParseCodexVersion(have)
	if !ok {
		return false
	}
	mM, mm, mp, ok := ParseCodexVersion(min)
	if !ok {
		return false
	}
	if hM != mM {
		return hM > mM
	}
	if hm != mm {
		return hm > mm
	}
	return hp >= mp
}

// codexHome is $CODEX_HOME or ~/.codex.
func codexHome() string {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// CodexHooksFeatureDisabled reports whether a config.toml layer (user, then
// project) turns the hooks feature off (`[features] hooks = false`, or its
// deprecated alias codex_hooks). Codex would then load no hooks at all, and
// muxcode must not claim the hook road for an agent that has none.
func CodexHooksFeatureDisabled() (bool, string) {
	paths := []string{filepath.Join(".codex", "config.toml")}
	if home := codexHome(); home != "" {
		paths = append(paths, filepath.Join(home, "config.toml"))
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if tomlHooksFeatureDisabled(string(data)) {
			return true, p
		}
	}
	return false, ""
}

// tomlHooksFeatureDisabled is a minimal TOML scan: it tracks the current table
// header and looks for `hooks = false` under [features], or a dotted
// `features.hooks = false` at top level. Enough for the one key that matters;
// not a TOML parser.
func tomlHooksFeatureDisabled(s string) bool {
	table := ""
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			table = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(line, "]"), "[")), "\"")
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(strings.SplitN(val, "#", 2)[0])
		inFeatures := table == "features" && (key == "hooks" || key == "codex_hooks")
		dotted := table == "" && (key == "features.hooks" || key == "features.codex_hooks")
		if (inFeatures || dotted) && val == "false" {
			return true
		}
	}
	return false
}

// CodexHooksEligible reports whether the installed codex can run muxcode's
// hooks, and if not, why.
func CodexHooksEligible() (bool, string) {
	out, err := codexVersionOutput()
	if err != nil {
		return false, "codex --version failed: " + err.Error()
	}
	if !CodexVersionAtLeast(out, CodexHooksMinVersion) {
		return false, fmt.Sprintf("codex %q is older than %s", strings.TrimSpace(out), CodexHooksMinVersion)
	}
	if disabled, path := CodexHooksFeatureDisabled(); disabled {
		return false, "[features] hooks = false in " + path
	}
	return true, ""
}

// --- Activation markers and trust ---

func codexHooksMarkerDir(session string) string {
	return filepath.Join(BusDir(session), "codex-hooks")
}

// CodexHooksMarkerPath is the role's hook-road activation marker. It holds the
// sha256 of the hooks.json muxcode wrote, and its presence alone is what
// CodexHooksActive — hence ResolveProvider's choice of road — checks.
func CodexHooksMarkerPath(session, role string) string {
	return filepath.Join(codexHooksMarkerDir(session), NormalizeBusRole(role)+".sha256")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func readCodexHooksMarker(session, role string) (string, bool) {
	data, err := os.ReadFile(CodexHooksMarkerPath(session, role))
	if err != nil {
		return "", false
	}
	h := strings.TrimSpace(string(data))
	return h, h != ""
}

func codexHooksFileHash() (string, bool) {
	data, err := os.ReadFile(CodexHooksPath())
	if err != nil {
		return "", false
	}
	return sha256Hex(data), true
}

// knownCodexHooksHashes returns every hash muxcode recorded in this session,
// across roles — a hooks.json matching any of them is muxcode's own, even when
// the template has since changed under an upgrade.
func knownCodexHooksHashes(session string) map[string]bool {
	known := map[string]bool{}
	entries, err := os.ReadDir(codexHooksMarkerDir(session))
	if err != nil {
		return known
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(codexHooksMarkerDir(session), e.Name()))
		if err == nil {
			if h := strings.TrimSpace(string(data)); h != "" {
				known[h] = true
			}
		}
	}
	return known
}

// CodexHooksActive reports whether the role runs on the hook road: its
// activation marker exists. This is the single capability switch every
// `muxcode hook` subprocess and the daemon consult, so it must stay a cheap
// stat.
func CodexHooksActive(session, role string) bool {
	_, err := os.Stat(CodexHooksMarkerPath(session, role))
	return err == nil
}

// CodexHooksTrusted reports whether hooks.json on disk hashes to the marker —
// the only condition under which the trust bypass flag is passed.
func CodexHooksTrusted(session, role string) bool {
	want, ok := readCodexHooksMarker(session, role)
	if !ok {
		return false
	}
	got, ok := codexHooksFileHash()
	return ok && got == want
}

// CodexHooksTampered reports a marker whose file exists but no longer matches
// it. A missing file is not tampering (it is rewritten); a mismatching one is.
func CodexHooksTampered(session, role string) bool {
	want, ok := readCodexHooksMarker(session, role)
	if !ok {
		return false
	}
	got, ok := codexHooksFileHash()
	return ok && got != want
}

// PrepareCodexHooks decides the road for one role and materializes it: with
// the opt-in set and an eligible codex it writes hooks.json (atomically) and
// the role's hash marker; otherwise it clears the marker so the role stays on
// the scrape road. Returns whether the hook road is active.
//
// It is idempotent and runs from both ConfigureLaunch (so the shared prompt
// built moments later already sees the capability) and WriteAgentConfig (the
// reload and `agent config` paths).
//
// A hooks.json that is present but is neither the current template nor a hash
// muxcode recorded this session belongs to someone else: it is left alone and
// the role falls back, logged as codex-hooks-unavailable — never overwritten.
// One whose recorded marker no longer matches is tampering: nothing is touched
// so the evidence survives, and ErrCodexHooksTampered makes the launcher refuse.
func PrepareCodexHooks(session, role string) (bool, error) {
	role = NormalizeBusRole(role)
	if !CodexHooksOptIn(role) {
		disableCodexHooks(session, role)
		return false, nil
	}
	if ok, reason := CodexHooksEligible(); !ok {
		LogLifecycle(session, "warn", "codex-hooks", "codex-hooks-unavailable",
			role+": "+reason+" — staying on the scrape road")
		disableCodexHooks(session, role)
		return false, nil
	}

	template := CodexHooksTemplate()
	want := sha256Hex(template)
	onDisk, present := codexHooksFileHash()
	marker, hasMarker := readCodexHooksMarker(session, role)

	if hasMarker && present && onDisk != marker && onDisk != want {
		LogLifecycle(session, "error", "codex-hooks", "codex-hooks-tampered",
			fmt.Sprintf("%s: %s hashes %s, muxcode wrote %s — launch refused", role, CodexHooksPath(), shortHash(onDisk), shortHash(marker)))
		return false, ErrCodexHooksTampered
	}
	if present && onDisk != want && !hasMarker && !knownCodexHooksHashes(session)[onDisk] {
		LogLifecycle(session, "warn", "codex-hooks", "codex-hooks-unavailable",
			role+": foreign "+CodexHooksPath()+" present — not overwriting; staying on the scrape road")
		_ = os.Remove(CodexHooksMarkerPath(session, role))
		return false, nil
	}

	if !present || onDisk != want {
		if err := os.MkdirAll(filepath.Dir(CodexHooksPath()), 0o755); err != nil {
			return false, err
		}
		if err := atomicWriteFile(CodexHooksPath(), template); err != nil {
			return false, err
		}
	}
	if err := os.MkdirAll(codexHooksMarkerDir(session), 0o755); err != nil {
		return false, err
	}
	if err := atomicWriteFile(CodexHooksMarkerPath(session, role), []byte(want+"\n")); err != nil {
		return false, err
	}
	LogLifecycle(session, "info", "codex-hooks", "codex-hooks-enabled",
		fmt.Sprintf("%s: %s sha256 %s", role, CodexHooksPath(), shortHash(want)))
	return true, nil
}

// disableCodexHooks drops the role's marker and, once no role in the session
// holds one, removes a hooks.json that is muxcode's own current template. A
// file that is anything else is left in place — it is not ours to delete.
func disableCodexHooks(session, role string) {
	_ = os.Remove(CodexHooksMarkerPath(session, role))
	if len(knownCodexHooksHashes(session)) > 0 {
		return
	}
	if onDisk, present := codexHooksFileHash(); present && onDisk == sha256Hex(CodexHooksTemplate()) {
		_ = os.Remove(CodexHooksPath())
	}
}

// refuseTamperedCodexHooks is the launcher check behind ErrCodexHooksTampered:
// a codex role whose recorded hooks.json hash no longer matches the file is
// refused, the same way a definitionless Claude agent is (MUX-136), rather than
// launched either trusting a file nobody vetted or silently without hooks.
func refuseTamperedCodexHooks(session string, cfg *LaunchConfig) error {
	if cfg == nil || cfg.Provider == nil || cfg.Provider.Name() != "codex" {
		return nil
	}
	if !CodexHooksTampered(session, cfg.Role) {
		return nil
	}
	return fmt.Errorf("%s: %s no longer matches what muxcode wrote (lifecycle codex-hooks-tampered) — refusing to launch; delete or restore the file: %w",
		cfg.Role, CodexHooksPath(), ErrCodexHooksTampered)
}
