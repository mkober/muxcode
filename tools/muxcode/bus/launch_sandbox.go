package bus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Launch-sandbox tracking. Codex fixes an agent's sandbox (codexSandboxArgs)
// when it launches, so an install that changes the sandbox reaches no running
// agent. On 2026-10-16 is-admissions-gateway's build agent, launched three days
// before build was granted network (PR #167), still failed `pip install` and
// `npx` on DNS while sessions launched after the install worked. Each launch
// stamps the fingerprint it ran with; the daemon's agent-defs watchdog compares
// it with what the installed binary would launch now and reloads the agent on a
// mismatch, as it does for a changed definition.

// LaunchSandboxFingerprint returns a hash of the sandbox flags role launches
// with, or "" when role's provider fixes no sandbox at launch (anything but
// Codex).
func LaunchSandboxFingerprint(role string) string {
	if ResolveProviderCLI(role) != "codex" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(codexSandboxArgs(role), "\x00")))
	return fmt.Sprintf("%x", sum)
}

func launchSandboxPath(session, role string) string {
	return filepath.Join(BusDir(session), fmt.Sprintf("launchsandbox-%s.hash", role))
}

// StampLaunchSandbox records the sandbox fingerprint role is launching with.
// Called from RunAgentLaunch beside StampAgentDefHash, and non-fatal like it: a
// failure is logged and never blocks the launch.
func StampLaunchSandbox(session, role string) {
	fp := LaunchSandboxFingerprint(role)
	if fp == "" {
		return
	}
	if err := os.WriteFile(launchSandboxPath(session, role), []byte(fp), 0o644); err != nil {
		LogLifecycle(session, "warn", "launch", "launch-sandbox-stamp-fail",
			fmt.Sprintf("%s: %v", role, err))
	}
}

// ReadLaunchSandbox returns the fingerprint role's running agent launched with,
// or "" when none was stamped — a launch without a sandbox, or one by a binary
// that predates the stamp.
func ReadLaunchSandbox(session, role string) string {
	data, err := os.ReadFile(launchSandboxPath(session, role))
	if err != nil {
		return ""
	}
	return string(data)
}

// SandboxFingerprinter returns a function reporting each role's sandbox
// fingerprint as computed by the muxcode binary on PATH — the binary the next
// launch runs, which can be newer than the caller's own code: an install made
// inside a Codex build sandbox cannot restart the daemons (MUX-161). Only a
// successful answer is cached, until the binary or a role's provider changes,
// so the subprocess runs once per install. A failure is retried once retry has
// passed and returned only when a probe actually ran, so a caller logging it
// logs once per attempt rather than once per check.
func SandboxFingerprinter(retry time.Duration) func(session string, roles []string) (map[string]string, error) {
	var key, failedKey string
	var cached map[string]string
	var failedAt time.Time
	return func(session string, roles []string) (map[string]string, error) {
		bin, k, err := installedBinaryKey(roles)
		if err == nil && k == key {
			return cached, nil
		}
		if k == failedKey && time.Since(failedAt) < retry {
			return nil, nil
		}
		var fps map[string]string
		if err == nil {
			fps, err = probeSandboxFingerprints(bin, session, roles)
		}
		if err != nil {
			failedKey, failedAt = k, time.Now()
			return nil, err
		}
		key, cached = k, fps
		return cached, nil
	}
}

// installedBinaryKey returns the muxcode binary on PATH and a key that changes
// with the binary or any role's provider; the key is "" when there is no binary.
func installedBinaryKey(roles []string) (string, string, error) {
	bin, err := exec.LookPath("muxcode")
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(bin)
	if err != nil {
		return "", "", err
	}
	k := fmt.Sprintf("%s|%d|%d", bin, info.Size(), info.ModTime().UnixNano())
	for _, role := range roles {
		k += "|" + role + "=" + ResolveProviderCLI(role)
	}
	return bin, k, nil
}

func probeSandboxFingerprints(bin, session string, roles []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"agent", "sandbox-fingerprint"}, roles...)...)
	cmd.Env = append(os.Environ(), "BUS_SESSION="+session)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s agent sandbox-fingerprint: %w", bin, err)
	}
	return ParseSandboxFingerprints(string(out)), nil
}

// ParseSandboxFingerprints reads `muxcode agent sandbox-fingerprint` output:
// one "<role>\t<fingerprint>" line per role, the fingerprint empty for a role
// without a sandbox. Lines without a tab are ignored.
func ParseSandboxFingerprints(out string) map[string]string {
	fps := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		role, fp, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if ok && role != "" {
			fps[role] = strings.TrimSpace(fp)
		}
	}
	return fps
}
