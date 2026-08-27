package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The prompt-agent (MUX-109) runs headless: no tmux window, no harness
// TUI. That is the harness's default path — --tui is opt-in — so headless
// means simply launching `muxcode-llm-harness run prompt` with no flag.
// Because no pane exists, none of the pane-based supervision covers it;
// the daemon owns the lifecycle via checkPromptAgent, using the same PID
// marker the harness writes for itself at startup (loop.go), read through
// IsHarnessActive.
//
// The role's tool profile (bus/profile.go) is deliberately minimal, since
// the agent sits behind a free-text input — the least predictable input
// in the system. No include groups: "bus" would grant Bash(muxcode *),
// the entire CLI. No Write/Edit: IsToolAllowed gates write_file on the
// mere presence of a Write pattern (hasToolPattern does not path-match),
// so any grant would unlock writes everywhere. Graph definitions are
// instead written through the muxcode graph CLI, whose write path
// validates before writing (WriteGraphDefinition) — denying raw writes is
// what makes validate-before-write unbypassable by the model.
// TestPromptProfileDeniesRepoWriteAndGit pins this.

const promptAgentRole = "prompt"

// PromptAgentEnabled reports whether the daemon should supervise a
// headless prompt-agent for this session. Opt-out follows the watchdog
// convention: MUXCODE_PROMPT_AGENT_DISABLE=1.
func PromptAgentEnabled() bool {
	return os.Getenv("MUXCODE_PROMPT_AGENT_DISABLE") != "1"
}

// opencodeGatewayURL is OpenCode's hosted OpenAI-compatible endpoint —
// the Zen gateway serving the opencode-go/* catalog (one sk- key).
const opencodeGatewayURL = "https://opencode.ai/zen/v1"

// promptBackendDefaultModel is what the prompt role runs on the opencode
// backend when MUXCODE_PROMPT_MODEL does not say otherwise: measured
// 2026-08-27, qwen3:4b could not serve the intent windows on this
// hardware (39-82s per call, fabricated success summaries), so the user
// revised the spec's off-the-metered-providers decision.
// Bare id, no provider prefix: the gateway's own API namespace differs
// from the OpenCode TUI's catalog names — sending the TUI-side
// opencode-go/deepseek-v4-flash got `Model ... is not supported`
// (verified against GET /zen/v1/models, 2026-08-27; a
// deepseek-v4-flash-free tier also exists for pinning via
// MUXCODE_PROMPT_MODEL).
const promptBackendDefaultModel = "deepseek-v4-flash"

// PromptBackend returns the prompt-agent's inference backend:
// "opencode" (default — the hosted Zen gateway; local qwen3:4b measured
// 39-82s/call with fabricated success summaries, user-flipped
// 2026-08-27) or "ollama" (explicit opt-in for local, unmetered).
// Env first, then the shell config file — the daemon's env is frozen at
// its launch, and a backend switched in ~/.config/muxcode/config must
// take effect without relaunching the session.
func PromptBackend(session string) string {
	if promptSetting(session, "MUXCODE_PROMPT_BACKEND") == "ollama" {
		return "ollama"
	}
	return "opencode"
}

// promptSetting resolves one prompt-agent setting through the same chain
// every other role's config uses: session runtime override → env →
// resolved shell config → home config. The override step is the provider
// selector's write channel (ReloadPromptAgent); the home-config step
// survives project-config shadowing. Backend, model, and any future
// prompt setting all resolve here so none of them can drift env-only
// again (a config-file model pin used to be silently ignored).
func promptSetting(session, key string) string {
	if session != "" {
		if ov, _ := ReadRuntimeOverrides(session, promptAgentRole); ov[key] != "" {
			return strings.TrimSpace(ov[key])
		}
	}
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if v := strings.TrimSpace(GetShellConfig("")[key]); v != "" {
		return v
	}
	return homeConfigValue(key)
}

// PromptBackendInfo returns the backend and model the prompt-agent runs
// on, mirroring the launch-time resolution in promptAgentEnv — a display
// helper for the Prompt surface, so the footer names exactly what
// StartPromptAgent would launch.
func PromptBackendInfo(session string) (backend, model string) {
	backend = PromptBackend(session)
	if m := promptSetting(session, "MUXCODE_PROMPT_MODEL"); m != "" {
		return backend, m
	}
	if backend == "opencode" {
		return backend, promptBackendDefaultModel
	}
	return backend, RoleModel(promptAgentRole)
}

// ReloadPromptAgent switches the headless prompt-agent's backend/model
// and restarts it. The prompt role has no tmux pane, so the window-based
// ReloadAgent path can't reach it — the switch is a session runtime
// override plus a stop/start of the harness process, and the daemon's
// respawn keeps it (promptAgentEnv reads the same override).
//
// cli uses the provider-selector vocabulary: "opencode" (Zen gateway) or
// "local" (Ollama) — Claude and Codex have no headless harness endpoint.
// Catalog-form opencode ids are stripped to their bare form here, at the
// selector boundary only: the selector's model list is the TUI catalog
// namespace, but the Zen gateway's is bare (opencode-go/deepseek-v4-flash
// 401s there while deepseek-v4-flash works, verified 2026-08-27).
func ReloadPromptAgent(session, cli, model string) error {
	backend, model, err := promptReloadTarget(cli, model)
	if err != nil {
		return err
	}
	if err := WriteRuntimeOverride(session, promptAgentRole, "MUXCODE_PROMPT_BACKEND", backend); err != nil {
		return err
	}
	// Written unconditionally: an empty model CLEARS the override (an
	// empty value reads as absent in promptSetting), so a model-less
	// reload falls back to the new backend's default instead of keeping
	// a prior reload's pin across backends (review catch, 2026-08-27).
	if err := WriteRuntimeOverride(session, promptAgentRole, "MUXCODE_PROMPT_MODEL", model); err != nil {
		return err
	}
	_ = StopPromptAgent(session)
	_, err = StartPromptAgent(session)
	return err
}

// promptReloadTarget maps a selector (cli, model) pick to the prompt
// backend vocabulary — the pure seam under ReloadPromptAgent.
func promptReloadTarget(cli, model string) (backend, m string, err error) {
	switch cli {
	case "opencode":
		return "opencode", strings.TrimPrefix(model, "opencode-go/"), nil
	case "local", "ollama":
		return "ollama", model, nil
	default:
		return "", "", fmt.Errorf("prompt-agent runs on the muxcode harness — pick OpenCode (gateway) or Local (Ollama), not %q", cli)
	}
}

// OpencodeAPIKey resolves the gateway key: env, then the resolved shell
// config, then the HOME config explicitly. The last step exists because
// GetShellConfig stops at the first config it finds — a repo-local
// .muxcode/config without the key silently shadowed the user's
// ~/.config/muxcode/config that held it, and the relaunched harness got
// the gateway URL with no Authorization (found live 2026-08-27: two
// health-check 404 deaths in prompt-agent.log).
func OpencodeAPIKey() string {
	if v := os.Getenv("MUXCODE_OPENCODE_API_KEY"); v != "" {
		return v
	}
	if v := strings.TrimSpace(GetShellConfig("")["MUXCODE_OPENCODE_API_KEY"]); v != "" {
		return v
	}
	return homeConfigValue("MUXCODE_OPENCODE_API_KEY")
}

// homeConfigValue reads one variable from ~/.config/muxcode/config
// directly, bypassing the project-config shadowing in GetShellConfig's
// resolution. User-global settings (gateway keys, backend choice) live
// in the home config and must be findable from any repo.
func homeConfigValue(key string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "muxcode", "config"))
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(ln, prefix)), `"'`)
		}
	}
	return ""
}

// promptAgentEnv builds the child environment for the headless harness,
// per backend. Ollama: thinking off (qwen3's reasoning phase is pure
// latency for closed-set classification). Opencode: gateway URL + model
// + bearer key, and NO think-suppression — /no_think is a qwen-family
// switch that would just be noise in another model's system prompt.
func promptAgentEnv(session string) []string {
	env := append(os.Environ(),
		"MUXCODE_SESSION="+session,
		"AGENT_ROLE="+promptAgentRole,
	)
	// Duplicate keys are fine: exec.Cmd keeps the last value, so these
	// resolved settings win over anything inherited from the parent.
	backend, model := PromptBackendInfo(session)
	env = append(env, "MUXCODE_PROMPT_MODEL="+model)
	if backend == "opencode" {
		gatewayURL := os.Getenv("MUXCODE_OPENCODE_API_URL")
		if gatewayURL == "" {
			gatewayURL = opencodeGatewayURL
		}
		env = append(env, "MUXCODE_OLLAMA_URL="+gatewayURL)
		if key := OpencodeAPIKey(); key != "" {
			env = append(env, "MUXCODE_HARNESS_API_KEY="+key)
		}
		return env
	}
	return append(env, "MUXCODE_HARNESS_NOTHINK=1")
}

// PromptAgentLogPath returns the headless agent's log file — its only
// output surface, since it has no pane.
func PromptAgentLogPath(session string) string {
	return filepath.Join(BusDir(session), "prompt-agent.log")
}

// PromptAgentAlive reports whether the prompt-agent process is running,
// via the harness PID marker (stale markers are cleaned by the check).
func PromptAgentAlive(session string) bool {
	return IsHarnessActive(session, promptAgentRole)
}

// StartPromptAgent launches the headless harness process for the prompt
// role. The child is detached (own process group, output to the log file)
// so it survives nothing it shouldn't and never wedges the daemon. The
// harness writes its own PID marker once its loop starts; callers gate
// re-starts on a cooldown rather than the marker to cover the write gap.
func StartPromptAgent(session string) (int, error) {
	bin, err := exec.LookPath("muxcode-llm-harness")
	if err != nil {
		return 0, fmt.Errorf("muxcode-llm-harness not in PATH: %w", err)
	}
	logf, err := os.OpenFile(PromptAgentLogPath(session), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()

	cmd := exec.Command(bin, "run", promptAgentRole)
	// AGENT_ROLE pins the bus identity: outside a pane there is no tmux
	// window name to fall back on, and inheriting the daemon's TMUX_PANE
	// would resolve some other window's name instead. The rest of the
	// environment is backend-dependent — see promptAgentEnv.
	cmd.Env = promptAgentEnv(session)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap; the marker liveness check owns restart
	return pid, nil
}

// StopPromptAgent terminates the prompt-agent process recorded in the
// harness marker and removes the marker. A missing or stale marker is not
// an error — the goal state is "not running".
func StopPromptAgent(session string) error {
	path := HarnessMarkerPath(session, promptAgentRole)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	return os.Remove(path)
}
