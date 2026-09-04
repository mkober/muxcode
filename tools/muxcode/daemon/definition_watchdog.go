package daemon

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Definition watchdog (MUX-136 Phase 2). REFUSES a definition-less Claude agent
// rather than quarantining it: a role's whole safety model is its definition
// (plan: docs-only writes, sole Atlassian write authority), so an instance
// running default tools under that role's name is torn down and relaunched
// through `muxcode agent launch`, which always carries the definition
// (ClaudeCodeProvider.BuildExecArgs binds --agent to --agents; RunAgentLaunch
// refuses when nothing resolves). A same-provider reload is recovery, not a
// provider change, so it needs no approval.
//
// A bare `claude --resume` typed into the pane loses its resumed context this
// way — accepted: MUX-126 is where a resume learns to carry the definition, and
// until then an unconstrained privileged agent is the worse outcome. The alert
// names the cause so the human knows why the pane relaunched.
//
// Detection is the positive argv probe first (bus.ProbeAgentDefinition); the
// startup banner is only a fallback for when no claude process can be
// attributed to the pane, and never overrides a positive probe — an agent
// merely discussing this bug prints the banner text without being downgraded.
// Two consecutive sightings before acting; reloads capped per role with a
// cooldown; alert-only once the cap is hit. Roles the daemon never restarts
// (bus.IsAgentHealthExcluded — edit, the pane the user is typing into) are
// alerted, never reloaded: a bare resume there is the user's own hand, and
// MUX-126 documents it as current practice. Opt out with
// MUXCODE_DEFINITION_WATCHDOG_DISABLE=1.

// definitionCheckSecs is the sweep interval; the env override exists for
// hermetic tests that compress supervisor time.
func definitionCheckSecs() int64 {
	if v := os.Getenv("MUXCODE_DEFINITION_CHECK_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 30
}

const definitionDebounce = 2
const definitionReloadCap = 3
const definitionReloadCooldownSecs int64 = 180

// definitionBannerLines is how much scrollback the banner fallback scans. The
// banner prints once at session start, so the scan must reach past the live
// tail; 200 lines outlasts a few turns of ordinary output without reading the
// whole history.
const definitionBannerLines = 200

func (d *Daemon) checkDefinitionless() {
	if os.Getenv("MUXCODE_DEFINITION_WATCHDOG_DISABLE") == "1" {
		return
	}
	now := time.Now().Unix()
	if now-d.lastDefinitionCheck < definitionCheckSecs() {
		return
	}
	d.lastDefinitionCheck = now

	windows := d.sessionWindows()
	for _, role := range bus.KnownRoles {
		if bus.WindowForRole(role) != role || bus.IsSpawnRole(role) || !roleHasWindow(windows, role) {
			continue
		}
		if bus.IsReloading(d.session, role) || bus.IsHarnessActive(d.session, role) {
			continue
		}
		if !bus.ResolveProvider(role).SupportsHooks() {
			continue // the argv contract is Claude Code's
		}
		if !d.agentAlive(d.session, role) || !d.definitionMissing(role) {
			d.clearDefinitionless(role)
			continue
		}

		d.definitionSeen[role]++
		if d.definitionSeen[role] < definitionDebounce {
			continue
		}

		ts := time.Now().Format("15:04:05")
		reloadable := !bus.IsAgentHealthExcluded(d.session, role)
		if !d.definitionless[role] {
			d.definitionless[role] = true
			fmt.Printf("  %s  Definition watchdog: %s is running without its agent definition — refusing it\n", ts, role)
			bus.LogLifecycle(d.session, "warn", "daemon", "agent-definitionless", role)
			if d.shouldSendEvent("agent-definitionless", role) && d.shouldNotifyEdit("event") {
				remedy := fmt.Sprintf("Refusing it: the daemon will reload %s with its definition (capped at %d reloads).", role, definitionReloadCap)
				if !reloadable {
					remedy = fmt.Sprintf("The daemon never restarts %s, so it stays up unconstrained until you relaunch it: muxcode reload %s.", role, role)
				}
				msg := bus.NewMessage("daemon", "edit", "event", "agent-definitionless",
					fmt.Sprintf("%s is running WITHOUT its agent definition — default tools, no role restrictions (the shape a bare `claude --resume` in its pane produces; the launcher never emits it). %s Do not resume agents bare.",
						role, remedy), "")
				if err := bus.Send(d.session, msg); err == nil {
					_ = bus.Notify(d.session, "edit")
				}
			}
		}
		if !reloadable {
			continue
		}

		if d.definitionReloads[role] >= definitionReloadCap {
			if !d.definitionGaveUp[role] {
				d.definitionGaveUp[role] = true
				fmt.Printf("  %s  Definition watchdog: %s still definition-less after %d reloads — giving up\n", ts, role, definitionReloadCap)
				bus.LogLifecycle(d.session, "error", "daemon", "definition-reload-giveup",
					fmt.Sprintf("%s definition-less after %d reloads", role, definitionReloadCap))
				if d.shouldSendEvent("agent-definitionless", role+":giveup") && d.shouldNotifyEdit("event") {
					msg := bus.NewMessage("daemon", "edit", "event", "agent-definitionless",
						fmt.Sprintf("%s came back without its agent definition after %d reloads — its definition file is missing or unreadable at every tier. It is running unconstrained; stop it (muxcode agent-health --stop %s) or restore the file (make install) and reload.",
							role, definitionReloadCap, role), "")
					if err := bus.Send(d.session, msg); err == nil {
						_ = bus.Notify(d.session, "edit")
					}
				}
			}
			continue
		}
		if last, ok := d.lastDefinitionReload[role]; ok && now-last < definitionReloadCooldownSecs {
			continue
		}

		d.definitionReloads[role]++
		d.lastDefinitionReload[role] = now
		delete(d.definitionSeen, role)
		fmt.Printf("  %s  Definition watchdog: reloading %s with its definition (attempt %d/%d)\n",
			ts, role, d.definitionReloads[role], definitionReloadCap)
		bus.LogLifecycle(d.session, "warn", "daemon", "definition-reload",
			fmt.Sprintf("%s reload %d/%d", role, d.definitionReloads[role], definitionReloadCap))
		go func(r string) {
			if err := d.reloadAgent(d.session, r); err != nil {
				fmt.Fprintf(os.Stderr, "  [watchdog] definition reload of %s failed: %v\n", r, err)
			}
		}(role)
	}
}

// definitionMissing is the per-role verdict: a positive probe result is final
// in either direction; only an unattributable process falls back to the banner.
func (d *Daemon) definitionMissing(role string) bool {
	switch d.probeDefinition(d.session, role) {
	case bus.DefinitionPresent:
		return false
	case bus.DefinitionMissing:
		return true
	}
	content, err := d.capturePane(bus.PaneTarget(d.session, role), definitionBannerLines)
	return err == nil && bus.PaneShowsDefinitionlessAgent(content)
}

// clearDefinitionless re-arms the watchdog for a role: it resets the sighting
// counter and, if the role was flagged, lifts the flag with a lifecycle event
// so the restore is as visible as the loss. Called when the definition is
// found on the process, the agent dies, or a reload takes the pane.
func (d *Daemon) clearDefinitionless(role string) {
	delete(d.definitionSeen, role)
	if d.definitionless[role] {
		d.definitionless[role] = false
		d.definitionGaveUp[role] = false
		fmt.Printf("  %s  Definition watchdog: %s is running with its definition again\n",
			time.Now().Format("15:04:05"), role)
		bus.LogLifecycle(d.session, "info", "daemon", "definition-restored", role)
	}
}

// definitionApplied gates checkAgentHealth's recovery announcement: a Claude
// agent counts as recovered only with POSITIVE evidence that its definition is
// on the process. DefinitionUnknown (launcher still pre-exec, probe failure)
// defers the announcement to a later sweep rather than letting
// `agent-recovered` describe a possibly downgraded agent — the 2026-09-01
// recovery event was emitted for an agent that had come back unconstrained.
// Non-Claude providers have no argv contract and pass through.
func (d *Daemon) definitionApplied(role string) bool {
	if !bus.ResolveProvider(role).SupportsHooks() {
		return true
	}
	return d.probeDefinition(d.session, role) == bus.DefinitionPresent
}
