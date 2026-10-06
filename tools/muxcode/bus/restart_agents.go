package bus

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Seams standing in for tmux, the process probe, the relaunch roads and the
// daemon's verdict in tests. restartReload takes the full ReloadAgent
// signature so a test can pin that the operator restart never passes a CLI or
// model override.
var (
	restartWindowNames = TmuxListWindowNames
	restartAgentAlive  = IsAgentAlive
	restartStopAgent   = func(session, role string) error { return GracefulStop(session, role, false) }
	restartRelaunch    = func(session, role string, mode relaunchMode) (string, error) {
		return scrapeAndRelaunch(session, role, ReloadTarget(session, role), "restart", "", LaunchReasonRestart, mode)
	}
	restartReload      = ReloadAgent
	restartAwaitVerify = awaitRestartVerification
	restartGap         = 3 * time.Second
	restartVerifyPoll  = 500 * time.Millisecond
	restartVerifyWait  = (RestartVerifyTimeoutSecs + 20) * time.Second
)

// RestartProviderAll is the provider filter that selects every provider.
const RestartProviderAll = "all"

// RestartTargets selects the agents an operator restart acts on: every role
// with a window in the session whose current provider matches filter (any,
// for "" or RestartProviderAll) — dead agents and edit included.
//
// It deliberately differs from ReloadAll's selection (reloadAllTargets), which
// skips dead agents and the edit/auto orchestrators. Skipping the dead is right
// for picking up new config and exactly wrong here: after a mass exit every
// target is dead, and a control that reported "0 agents restarted" then would
// do nothing in the situation it exists for (MUX-139 Decision 2). Including edit
// is the explicit request ReloadAll's skip asks for; RestartAgent keeps it
// resume-only. The window list is required — an unreadable list is an error,
// never a guess.
func RestartTargets(session, filter string) ([]string, error) {
	windows, err := restartWindowNames(session)
	if err != nil {
		return nil, fmt.Errorf("listing windows in session %s: %w", session, err)
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("session %s has no windows", session)
	}
	var roles []string
	for _, role := range ReloadableRoles() {
		if role == promptAgentRole || !RoleWindowPresent(windows, role) {
			continue
		}
		if filter != "" && filter != RestartProviderAll && ResolveProviderCLI(role) != filter {
			continue
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// RestartProviderCount is one provider's share of the restartable agents.
type RestartProviderCount struct {
	CLI   string
	Total int
	Down  int
}

// RestartProviderCounts groups the session's restartable agents by current
// provider, counting the dead, in first-seen order — the modal's provider list.
func RestartProviderCounts(session string) ([]RestartProviderCount, error) {
	roles, err := RestartTargets(session, "")
	if err != nil {
		return nil, err
	}
	var counts []RestartProviderCount
	index := map[string]int{}
	for _, role := range roles {
		cli := ResolveProviderCLI(role)
		i, ok := index[cli]
		if !ok {
			i = len(counts)
			index[cli] = i
			counts = append(counts, RestartProviderCount{CLI: cli})
		}
		counts[i].Total++
		if !restartAgentAlive(session, role) {
			counts[i].Down++
		}
	}
	return counts, nil
}

// RestartAgent brings one agent back on its current provider and model.
//
// A Claude agent goes through the MUX-139 resume road (scrapeAndRelaunch, the
// body the daemon restart and `muxcode resume` share): a live one is exited
// first so its pane draws the exit banner, then it is relaunched with its full
// flag set plus `--resume <id>` when the banner offers one. edit is
// resume-only — with no session to resume nothing is typed and
// ErrResumeUnavailable returned, since a fresh edit discards the user's
// conversation.
//
// The relaunch's definition check belongs to the daemon, not to this process:
// a RestartVerification record is written before anything is typed — a
// restart whose check cannot be handed off does not happen — and the daemon
// verifies the pane (AdvanceRestartVerification), stopping and retrying the
// stop until confirmed. This call only waits for the verdict, so closing the
// modal or killing the CLI mid-restart never leaves an agent unsupervised.
// A role whose previous restart's check is still active is refused.
//
// Any other provider gets a same-provider fresh ReloadAgent. Neither road
// writes a CLI or model override — the restart is a filter over the current
// assignment, never a switch. It returns the resumed session id, if any.
func RestartAgent(session, role string) (string, error) {
	if !IsClaudeTUI(ResolveProvider(role)) {
		return "", restartReload(session, role, "", "", false)
	}
	if RestartVerificationActive(session, role) {
		return "", fmt.Errorf("%s's previous restart is still being verified by the daemon", role)
	}

	release, err := acquireReloadMarker(session, role)
	if err != nil {
		return "", err
	}
	defer release()

	if restartAgentAlive(session, role) {
		if err := restartStopAgent(session, role); err != nil {
			return "", fmt.Errorf("exiting live %s before restart: %w", role, err)
		}
	}
	mode := relaunchResume
	if role == "edit" {
		mode = relaunchResumeOnly
	}
	record := RestartVerification{Role: role, Status: RestartVerifyPending, RelaunchedAt: time.Now().Unix()}
	if err := WriteRestartVerification(session, record); err != nil {
		return "", fmt.Errorf("handing %s's definition check to the daemon failed — not restarting: %w", role, err)
	}
	id, err := restartRelaunch(session, role, mode)
	if errors.Is(err, ErrResumeUnavailable) {
		ClearRestartVerification(session, role) // nothing was typed
		return "", err
	}
	if err != nil {
		return "", err // the record stays: a half-typed launch is still the daemon's to check
	}
	ClearNotifiedIDs(session, role)
	return id, restartAwaitVerify(session, role)
}

// RestartAgents restarts every RestartTargets agent for filter — the CLI's
// `reload --all --resume`.
func RestartAgents(session, filter string, progress ReloadProgress) ([]ReloadResult, error) {
	roles, err := RestartTargets(session, filter)
	if err != nil {
		return nil, err
	}
	return RestartRoles(session, roles, progress), nil
}

// RestartRoles restarts roles sequentially through RestartAgent, continuing
// past individual failures, and reports each through progress. Results carry
// Restarted, the unchanged provider and model, and ResumedID. The modal calls
// it with the targets it re-read at confirm time, so the rows it shows are
// exactly the agents acted on.
func RestartRoles(session string, roles []string, progress ReloadProgress) []ReloadResult {
	var results []ReloadResult
	for i, role := range roles {
		if i > 0 {
			time.Sleep(restartGap)
		}
		start := time.Now()
		cli, model := ResolveProviderCLI(role), EffectiveConfig(role).Model
		id, err := RestartAgent(session, role)
		r := ReloadResult{
			Role: role, Success: err == nil, Error: err,
			OldCLI: cli, OldModel: model,
			NewCLI: ResolveProviderCLI(role), NewModel: EffectiveConfig(role).Model,
			Duration:  time.Since(start),
			Restarted: true, ResumedID: id,
		}
		results = append(results, r)
		if progress != nil {
			progress(i, r)
		}
	}
	ok := 0
	for _, r := range results {
		if r.Success {
			ok++
		}
	}
	LogLifecycle(session, "info", "restart", "agents-restart",
		fmt.Sprintf("%d/%d restarted: %s", ok, len(results), strings.Join(roles, ", ")))
	return results
}
