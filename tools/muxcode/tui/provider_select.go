package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Section indices for keyboard navigation.
const (
	sectionProvider = 0
	sectionModel    = 1
	sectionAgents   = 2
	sectionOptions  = 3
	sectionButtons  = 4
)

const numSections = 5

// ProviderSelectUI is the interactive provider selector modal.
type ProviderSelectUI struct {
	session   string
	role      string
	window    string
	providers []bus.ProviderOption

	// Selection state
	selectedCLI int // index into providers
	selectedMdl int // index into current provider's models (+1 for "custom...")
	section     int // active section
	cursorRow   int // cursor row within the current section

	// Agent selection
	agents      []bus.AgentReloadStatus
	agentChecks []bool
	agentScroll int // scroll offset for agent list

	// Options
	compact bool

	// Button focus (0=Reload, 1=Cancel)
	buttonFocus int

	// Custom model input
	customModel  string
	customActive bool // true when typing a custom model

	// Progress view state
	inProgress      bool
	progressMu      sync.Mutex
	progressResults []bus.ReloadResult
	progressTotal   int
	progressDone    bool
	progressCh      chan struct{} // signals render loop to redraw on progress

	// Terminal
	keyCh chan byte
}

// maxVisibleAgents is the max agents shown before scrolling.
const maxVisibleAgents = 10

// NewProviderSelectUI creates a new provider selector for the given session and role.
func NewProviderSelectUI(session, role, window string) *ProviderSelectUI {
	providers := bus.AvailableProviders()

	// Find the current provider index
	currentCLI := bus.ResolveProviderCLI(role)
	selectedCLI := 0
	for i, p := range providers {
		if p.CLI == currentCLI {
			selectedCLI = i
			break
		}
	}

	// Find the current model index
	rc := bus.EffectiveConfig(role)
	selectedMdl := 0
	if p := bus.ProviderByIndex(providers, selectedCLI); p != nil {
		for j, m := range p.Models {
			if m == rc.Model {
				selectedMdl = j
				break
			}
		}
	}

	// Build agent list
	agents := bus.ActiveAgentStatuses(session)
	agentChecks := make([]bool, len(agents))

	// The current window's agent is not pre-selected — it's shown greyed
	// out and non-selectable (the user opens the modal to reload *other*
	// agents, not the one they're already on).

	return &ProviderSelectUI{
		session:     session,
		role:        role,
		window:      window,
		providers:   providers,
		selectedCLI: selectedCLI,
		selectedMdl: selectedMdl,
		section:     sectionProvider,
		cursorRow:   selectedCLI,
		agents:      agents,
		agentChecks: agentChecks,
		progressCh:  make(chan struct{}, 16), // initialized for render loop select
	}
}

// selectedAgentCount returns the number of checked agents.
func (ui *ProviderSelectUI) selectedAgentCount() int {
	count := 0
	for _, c := range ui.agentChecks {
		if c {
			count++
		}
	}
	return count
}

// selectedAgentRoles returns the role names of checked agents.
func (ui *ProviderSelectUI) selectedAgentRoles() []string {
	var roles []string
	for i, c := range ui.agentChecks {
		if c {
			roles = append(roles, ui.agents[i].Role)
		}
	}
	return roles
}

// isSelectable returns true if the agent at index i can be checked/unchecked.
// Dead agents and the current active window's agent are not selectable.
func (ui *ProviderSelectUI) isSelectable(i int) bool {
	if i < 0 || i >= len(ui.agents) {
		return false
	}
	a := &ui.agents[i]
	if !a.Alive {
		return false
	}
	// The agent for the window that opened the modal is not selectable
	if a.Role == ui.role {
		return false
	}
	return true
}

// Run starts the interactive TUI loop. Returns the selected CLI, model,
// compact flag, and selected agent roles — or empty if cancelled.
func (ui *ProviderSelectUI) Run() (cli, model string, compact bool, roles []string, cancelled bool) {
	rawCmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	rawCmd.Stdin = os.Stdin
	rawErr := rawCmd.Run()

	// Clear screen and hide cursor
	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25l")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ui.keyCh = make(chan byte, 16)
	go ui.readKeys()

	defer ui.cleanup(rawErr == nil)

	for {
		var frame string
		if ui.inProgress {
			frame = ui.renderProgress()
		} else {
			frame = ui.render()
		}
		fmt.Print("\033[H")
		fmt.Print(ClearFrame(frame))
		fmt.Print("\033[J")

		// Wait for key input or progress update
		select {
		case <-sigCh:
			return "", "", false, nil, true
		case <-ui.progressCh:
			// Progress update — just redraw (loop continues to top)
			continue
		case key := <-ui.keyCh:
			if ui.inProgress {
				action := ui.handleProgressKey(key)
				if action == "close" {
					roles := ui.selectedAgentRoles()
					p := &ui.providers[ui.selectedCLI]
					cli = p.CLI
					if ui.customActive && ui.customModel != "" {
						model = ui.customModel
					} else if ui.selectedMdl < len(p.Models) {
						model = p.Models[ui.selectedMdl]
					} else {
						model = p.Default
					}
					return cli, model, ui.compact, roles, false
				}
				continue
			}

			action := ui.handleKey(key)
			switch action {
			case "cancel":
				return "", "", false, nil, true
			case "confirm":
				p := &ui.providers[ui.selectedCLI]
				cli = p.CLI
				if ui.customActive && ui.customModel != "" {
					model = ui.customModel
				} else if ui.selectedMdl < len(p.Models) {
					model = p.Models[ui.selectedMdl]
				} else {
					model = p.Default
				}
				roles = ui.selectedAgentRoles()

				// Multi-agent: transition to progress view
				if len(roles) > 1 {
					ui.startBatchReload(cli, model)
					continue
				}

				return cli, model, ui.compact, roles, false
			}
		}
	}
}

// startBatchReload kicks off the background batch reload and switches to progress view.
func (ui *ProviderSelectUI) startBatchReload(cli, model string) {
	roles := ui.selectedAgentRoles()

	ui.progressTotal = len(roles)
	ui.progressResults = nil
	ui.progressDone = false
	ui.progressCh = make(chan struct{}, 16) // buffered to avoid blocking goroutine
	ui.inProgress = true

	go func() {
		bus.ReloadBatch(ui.session, roles, cli, model, ui.compact, func(i int, r bus.ReloadResult) {
			// Persist per agent, and only for the ones that actually
			// reloaded — same ordering rule as the single-agent path above.
			if r.Success {
				persistToConfig([]string{r.Role}, cli, model)
			}
			ui.progressMu.Lock()
			ui.progressResults = append(ui.progressResults, r)
			ui.progressMu.Unlock()
			// Signal render loop to redraw
			select {
			case ui.progressCh <- struct{}{}:
			default: // don't block if channel is full
			}
		})
		ui.progressMu.Lock()
		ui.progressDone = true
		ui.progressMu.Unlock()
		// Signal render loop for final "done" redraw
		select {
		case ui.progressCh <- struct{}{}:
		default:
		}
	}()
}

// handleProgressKey handles input during the progress view.
func (ui *ProviderSelectUI) handleProgressKey(key byte) string {
	switch key {
	case 'q', 27: // q or Escape
		return "close"
	case 10, 13: // Enter
		ui.progressMu.Lock()
		done := ui.progressDone
		ui.progressMu.Unlock()
		if done {
			return "close"
		}
	}
	return ""
}

// handleKey processes a single keypress and returns an action string.
func (ui *ProviderSelectUI) handleKey(key byte) string {
	// Custom model text input mode
	if ui.customActive {
		switch key {
		case 27: // Escape — cancel custom input
			ui.customActive = false
			ui.customModel = ""
			return ""
		case 10, 13: // Enter — accept custom input
			ui.customActive = false
			return ""
		case 127, 8: // Backspace
			if len(ui.customModel) > 0 {
				ui.customModel = ui.customModel[:len(ui.customModel)-1]
			}
			return ""
		default:
			if key >= 32 && key < 127 {
				ui.customModel += string(key)
			}
			return ""
		}
	}

	switch key {
	case 'q': // Quit
		return "cancel"

	case 10, 13: // Enter (LF or CR — icrnl may translate)
		if ui.section == sectionButtons {
			if ui.buttonFocus == 1 {
				return "cancel"
			}
			return "confirm"
		}
		return "confirm"

	case '\t': // Tab — switch section
		ui.section = (ui.section + 1) % numSections
		ui.syncCursorToSection()
		return ""

	case 'k': // Up
		ui.moveUp()
		return ""

	case 'j': // Down
		ui.moveDown()
		return ""

	case ' ': // Space — select
		ui.selectCurrent()
		return ""

	case 'a': // Select all agents (excludes orchestrators)
		if ui.section == sectionAgents {
			ui.selectAllAgents()
		}
		return ""

	case 'n': // Deselect all agents
		if ui.section == sectionAgents {
			ui.deselectAllAgents()
		}
		return ""

	case 'p': // Toggle agents by current provider
		if ui.section == sectionAgents {
			ui.toggleAgentsByProvider()
		}
		return ""

	case 'h': // Left (buttons)
		if ui.section == sectionButtons && ui.buttonFocus > 0 {
			ui.buttonFocus--
		}
		return ""

	case 'l': // Right (buttons)
		if ui.section == sectionButtons && ui.buttonFocus < 1 {
			ui.buttonFocus++
		}
		return ""

	case 27: // Escape or arrow key sequence
		// Read next bytes to distinguish bare Escape from CSI sequences
		select {
		case b1 := <-ui.keyCh:
			if b1 == '[' {
				select {
				case b2 := <-ui.keyCh:
					switch b2 {
					case 'A': // Up
						ui.moveUp()
					case 'B': // Down
						ui.moveDown()
					case 'C': // Right
						if ui.section == sectionButtons && ui.buttonFocus < 1 {
							ui.buttonFocus++
						}
					case 'D': // Left
						if ui.section == sectionButtons && ui.buttonFocus > 0 {
							ui.buttonFocus--
						}
					case 'Z': // Shift+Tab — reverse tab
						ui.section = (ui.section + numSections - 1) % numSections
						ui.syncCursorToSection()
					}
				case <-time.After(50 * time.Millisecond):
				}
			}
		case <-time.After(50 * time.Millisecond):
			// Bare Escape — cancel
			return "cancel"
		}
		return ""
	}

	return ""
}

// moveUp moves the cursor up, crossing into the previous section at boundaries.
func (ui *ProviderSelectUI) moveUp() {
	switch ui.section {
	case sectionProvider:
		if ui.cursorRow > 0 {
			ui.cursorRow--
		}
	case sectionModel:
		if ui.cursorRow > 0 {
			ui.cursorRow--
		} else {
			ui.section = sectionProvider
			ui.cursorRow = len(ui.providers) - 1
		}
	case sectionAgents:
		if ui.cursorRow > 0 {
			ui.cursorRow--
			// Scroll up if cursor is above visible area
			if ui.cursorRow < ui.agentScroll {
				ui.agentScroll = ui.cursorRow
			}
		} else {
			ui.section = sectionModel
			p := &ui.providers[ui.selectedCLI]
			ui.cursorRow = len(p.Models) // "custom..." row
		}
	case sectionOptions:
		if ui.cursorRow > 0 {
			ui.cursorRow--
		} else {
			ui.section = sectionAgents
			ui.cursorRow = len(ui.agents) - 1
			// Ensure visible
			if ui.cursorRow >= ui.agentScroll+maxVisibleAgents {
				ui.agentScroll = ui.cursorRow - maxVisibleAgents + 1
			}
		}
	case sectionButtons:
		ui.section = sectionOptions
		ui.cursorRow = 0 // single option
	}
}

// moveDown moves the cursor down, crossing into the next section at boundaries.
func (ui *ProviderSelectUI) moveDown() {
	switch ui.section {
	case sectionProvider:
		if ui.cursorRow < len(ui.providers)-1 {
			ui.cursorRow++
		} else {
			ui.section = sectionModel
			ui.cursorRow = 0
		}
	case sectionModel:
		p := &ui.providers[ui.selectedCLI]
		maxIdx := len(p.Models) // "custom..." row index
		if ui.cursorRow < maxIdx {
			ui.cursorRow++
		} else {
			ui.section = sectionAgents
			ui.cursorRow = 0
			ui.agentScroll = 0
		}
	case sectionAgents:
		if ui.cursorRow < len(ui.agents)-1 {
			ui.cursorRow++
			// Scroll down if cursor is below visible area
			if ui.cursorRow >= ui.agentScroll+maxVisibleAgents {
				ui.agentScroll = ui.cursorRow - maxVisibleAgents + 1
			}
		} else {
			ui.section = sectionOptions
			ui.cursorRow = 0
		}
	case sectionOptions:
		// Single option (compact) — move straight to buttons
		ui.section = sectionButtons
		ui.buttonFocus = 0
	case sectionButtons:
		if ui.buttonFocus < 1 {
			ui.buttonFocus++
		}
	}
}

// selectCurrent selects the item at the cursor in the current section.
func (ui *ProviderSelectUI) selectCurrent() {
	switch ui.section {
	case sectionProvider:
		if ui.cursorRow != ui.selectedCLI {
			ui.selectedCLI = ui.cursorRow
			ui.selectedMdl = 0 // reset model selection
			ui.customModel = ""
			ui.customActive = false
		}
	case sectionModel:
		p := &ui.providers[ui.selectedCLI]
		if ui.cursorRow < len(p.Models) {
			ui.selectedMdl = ui.cursorRow
			ui.customActive = false
		} else {
			// "custom..." option
			ui.selectedMdl = ui.cursorRow
			ui.customActive = true
			ui.customModel = ""
		}
	case sectionAgents:
		if ui.isSelectable(ui.cursorRow) {
			ui.agentChecks[ui.cursorRow] = !ui.agentChecks[ui.cursorRow]
		}
	case sectionOptions:
		ui.compact = !ui.compact
	}
}

// selectAllAgents selects all selectable agents except orchestrators (edit/auto).
func (ui *ProviderSelectUI) selectAllAgents() {
	for i, a := range ui.agents {
		if ui.isSelectable(i) && !a.Orchestrator {
			ui.agentChecks[i] = true
		}
	}
}

// deselectAllAgents deselects all agents.
func (ui *ProviderSelectUI) deselectAllAgents() {
	for i := range ui.agentChecks {
		ui.agentChecks[i] = false
	}
}

// toggleAgentsByProvider toggles all agents whose current CLI matches the
// *currently selected* target provider. This enables the "select all agents
// currently on the failing provider" workflow.
func (ui *ProviderSelectUI) toggleAgentsByProvider() {
	targetCLI := ui.providers[ui.selectedCLI].CLI

	// Check if any matching selectable agents are currently unchecked
	anyUnchecked := false
	for i, a := range ui.agents {
		if ui.isSelectable(i) && a.CLI == targetCLI && !ui.agentChecks[i] {
			anyUnchecked = true
			break
		}
	}

	// Toggle: if any are unchecked, check all matching; otherwise uncheck all matching
	for i, a := range ui.agents {
		if ui.isSelectable(i) && a.CLI == targetCLI {
			ui.agentChecks[i] = anyUnchecked
		}
	}
}

// syncCursorToSection resets cursor position when switching sections.
func (ui *ProviderSelectUI) syncCursorToSection() {
	switch ui.section {
	case sectionProvider:
		ui.cursorRow = ui.selectedCLI
	case sectionModel:
		ui.cursorRow = ui.selectedMdl
	case sectionAgents:
		// Find first checked agent, or default to 0
		ui.cursorRow = 0
		for i, c := range ui.agentChecks {
			if c {
				ui.cursorRow = i
				break
			}
		}
		// Ensure visible
		if ui.cursorRow < ui.agentScroll {
			ui.agentScroll = ui.cursorRow
		} else if ui.cursorRow >= ui.agentScroll+maxVisibleAgents {
			ui.agentScroll = ui.cursorRow - maxVisibleAgents + 1
		}
	case sectionOptions:
		ui.cursorRow = 0
	case sectionButtons:
		ui.buttonFocus = 0
	}
}

// render draws the full TUI frame.
func (ui *ProviderSelectUI) render() string {
	var b strings.Builder
	p := &ui.providers[ui.selectedCLI]

	// Current config
	rc := bus.EffectiveConfig(ui.role)
	fkey := bus.WindowFKey(ui.session, ui.window)

	// Header
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s%sAgent:%s %s", Bold, Purple, RST, ui.role))
	if fkey != "" {
		b.WriteString(fmt.Sprintf(" %s(%s)%s", Comment, fkey, RST))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %sCurrent:%s %s / %s\n", Dim, RST, rc.CLI, rc.Model))
	b.WriteString("\n")

	// Provider section
	active := ui.section == sectionProvider
	b.WriteString(ui.sectionHeader("Provider", active))
	b.WriteString("\n")

	for i, prov := range ui.providers {
		cursor := "  "
		if active && ui.cursorRow == i {
			cursor = Purple + "> " + RST
		}

		radio := "○"
		if i == ui.selectedCLI {
			radio = Green + "●" + RST
		}

		name := prov.Name
		suffix := ""
		if !prov.Installed {
			name = Comment + prov.Name + RST
			suffix = Comment + " (not installed)" + RST
		}

		b.WriteString(fmt.Sprintf("  %s %s %s%s\n", cursor, radio, name, suffix))
	}
	b.WriteString("\n")

	// Model section
	active = ui.section == sectionModel
	b.WriteString(ui.sectionHeader("Model", active))
	b.WriteString("\n")

	for j, model := range p.Models {
		cursor := "  "
		if active && ui.cursorRow == j {
			cursor = Purple + "> " + RST
		}

		radio := "○"
		if j == ui.selectedMdl && !ui.customActive {
			radio = Green + "●" + RST
		}

		b.WriteString(fmt.Sprintf("  %s %s %s\n", cursor, radio, model))
	}
	// Custom option
	{
		j := len(p.Models)
		cursor := "  "
		if active && ui.cursorRow == j {
			cursor = Purple + "> " + RST
		}
		radio := "○"
		if ui.customActive {
			radio = Green + "●" + RST
		}
		label := "custom..."
		if ui.customActive {
			label = fmt.Sprintf("custom: %s%s%s%s", Cyan, ui.customModel, Yellow+"█"+RST, RST)
		}
		b.WriteString(fmt.Sprintf("  %s %s %s\n", cursor, radio, label))
	}
	b.WriteString("\n")

	// Agents section
	active = ui.section == sectionAgents
	agentCount := ui.selectedAgentCount()
	agentLabel := "Agents"
	if agentCount > 0 {
		agentLabel = fmt.Sprintf("Agents (%d selected)", agentCount)
	}
	b.WriteString(ui.sectionHeader(agentLabel, active))
	b.WriteString("\n")

	// Determine visible window
	visEnd := ui.agentScroll + maxVisibleAgents
	if visEnd > len(ui.agents) {
		visEnd = len(ui.agents)
	}

	// Scroll indicator (top)
	if ui.agentScroll > 0 {
		b.WriteString(fmt.Sprintf("      %s↑ %d more%s\n", Comment, ui.agentScroll, RST))
	}

	for i := ui.agentScroll; i < visEnd; i++ {
		a := ui.agents[i]
		cursor := "  "
		if active && ui.cursorRow == i {
			cursor = Purple + "> " + RST
		}

		check := "[ ]"
		if ui.agentChecks[i] {
			check = Green + "[x]" + RST
		}

		// Role name (padded to 10 chars)
		roleName := Pad(a.Role, 10)

		// CLI / abbreviated model
		cliModel := fmt.Sprintf("%s / %s", a.CLI, bus.AbbreviateModel(a.Model))

		// Suffix: warning for orchestrators, (dead) for dead, (active) for current window
		suffix := ""
		if !a.Alive {
			roleName = Comment + Pad(a.Role, 10) + RST
			cliModel = Comment + cliModel + RST
			check = Comment + "[ ]" + RST
			suffix = Comment + " (dead)" + RST
		} else if a.Role == ui.role {
			roleName = Comment + Pad(a.Role, 10) + RST
			cliModel = Comment + cliModel + RST
			check = Comment + "[ ]" + RST
			suffix = Comment + " (active)" + RST
		} else if a.Orchestrator {
			suffix = Yellow + "⚠" + RST
		}

		// F-key label
		fkeyLabel := ""
		if a.FKey != "" {
			fkeyLabel = Comment + " " + a.FKey + RST
		}

		b.WriteString(fmt.Sprintf("  %s %s %s %s%s%s\n",
			cursor, check, roleName, cliModel, suffix, fkeyLabel))
	}

	// Scroll indicator (bottom)
	if visEnd < len(ui.agents) {
		b.WriteString(fmt.Sprintf("      %s↓ %d more%s\n", Comment, len(ui.agents)-visEnd, RST))
	}

	// Agent shortcuts help
	if active {
		b.WriteString(fmt.Sprintf("      %s(a) All  (p) By provider  (n) None%s\n", Comment, RST))
	}
	b.WriteString("\n")

	// Options section
	active = ui.section == sectionOptions
	b.WriteString(ui.sectionHeader("Options", active))
	b.WriteString("\n")

	{
		cursor := "  "
		if active && ui.cursorRow == 0 {
			cursor = Purple + "> " + RST
		}
		check := "[ ]"
		if ui.compact {
			check = Green + "[x]" + RST
		}
		b.WriteString(fmt.Sprintf("  %s %s Compact before reload\n", cursor, check))
	}
	b.WriteString("\n")

	// Buttons
	active = ui.section == sectionButtons
	reloadStyle := Dim
	cancelStyle := Dim
	if active {
		if ui.buttonFocus == 0 {
			reloadStyle = Bold + Green
		} else {
			cancelStyle = Bold + Red
		}
	}

	// Dynamic reload button text
	reloadText := "Reload"
	if agentCount > 1 {
		reloadText = fmt.Sprintf("Reload %d agents", agentCount)
	} else if agentCount == 1 {
		reloadText = "Reload 1 agent"
	}

	b.WriteString(fmt.Sprintf("  %s[ %s ]%s  %s[ Cancel ]%s\n",
		reloadStyle, reloadText, RST, cancelStyle, RST))
	b.WriteString("\n")

	// Help line
	b.WriteString(fmt.Sprintf("  %s↑↓ Navigate  ␣ Select  ⏎ Reload  Tab/S-Tab Section  q Quit%s\n", Comment, RST))

	return b.String()
}

// renderProgress draws the progress view during a batch reload.
func (ui *ProviderSelectUI) renderProgress() string {
	var b strings.Builder
	p := &ui.providers[ui.selectedCLI]

	// Determine target model
	model := p.Default
	if ui.customActive && ui.customModel != "" {
		model = ui.customModel
	} else if ui.selectedMdl < len(p.Models) {
		model = p.Models[ui.selectedMdl]
	}

	ui.progressMu.Lock()
	results := make([]bus.ReloadResult, len(ui.progressResults))
	copy(results, ui.progressResults)
	total := ui.progressTotal
	done := ui.progressDone
	ui.progressMu.Unlock()

	completed := len(results)
	roles := ui.selectedAgentRoles()

	// Header
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s%sReloading %d agents%s → %s\n",
		Bold, Purple, total, RST, p.CLI))
	b.WriteString(fmt.Sprintf("  %sModel:%s %s\n", Dim, RST, bus.AbbreviateModel(model)))
	b.WriteString("\n")

	// Section header
	b.WriteString(fmt.Sprintf("  %s%s── Progress ─────────────────────%s\n", Bold, Purple, RST))
	b.WriteString("\n")

	// Status for each agent
	resultMap := make(map[string]*bus.ReloadResult)
	for i := range results {
		resultMap[results[i].Role] = &results[i]
	}

	// Determine currently-reloading agent
	currentRole := ""
	if completed < total && completed < len(roles) {
		currentRole = roles[completed]
	}

	for _, role := range roles {
		if r, ok := resultMap[role]; ok {
			// Completed
			if r.Success {
				dur := r.Duration.Round(time.Second)
				if r.OldCLI == r.NewCLI && r.OldModel == r.NewModel {
					b.WriteString(fmt.Sprintf("    %s✓%s %-10s %s(no change)%s  %s\n",
						Green, RST, r.Role, Comment, RST, dur))
				} else {
					b.WriteString(fmt.Sprintf("    %s✓%s %-10s %s → %s  %s\n",
						Green, RST, r.Role, r.OldCLI, r.NewCLI, dur))
				}
			} else {
				b.WriteString(fmt.Sprintf("    %s✗%s %-10s %s%v%s\n",
					Red, RST, r.Role, Red, r.Error, RST))
			}
		} else if role == currentRole {
			// Currently reloading
			b.WriteString(fmt.Sprintf("    %s⟳%s %-10s %s...\n",
				Yellow, RST, role, Comment))
		} else {
			// Pending
			b.WriteString(fmt.Sprintf("    %s○%s %-10s\n",
				Comment, RST, role))
		}
	}
	b.WriteString("\n")

	// Progress bar
	barWidth := 30
	filled := 0
	if total > 0 {
		filled = (completed * barWidth) / total
	}
	bar := strings.Repeat("━", filled) + strings.Repeat("░", barWidth-filled)
	b.WriteString(fmt.Sprintf("  %s%s%s  %d/%d\n", Green, bar, RST, completed, total))
	b.WriteString("\n")

	// Footer
	if done {
		succeeded := 0
		for _, r := range results {
			if r.Success {
				succeeded++
			}
		}
		if succeeded == total {
			b.WriteString(fmt.Sprintf("  %s✓ All agents reloaded successfully%s\n", Green, RST))
		} else {
			b.WriteString(fmt.Sprintf("  %s%d/%d succeeded, %d failed%s\n",
				Yellow, succeeded, total, total-succeeded, RST))
		}
		b.WriteString(fmt.Sprintf("  %sPress Enter or q to close%s\n", Comment, RST))
	} else {
		b.WriteString(fmt.Sprintf("  %sPress q to close (reload continues in background)%s\n", Comment, RST))
	}

	return b.String()
}

// sectionHeader renders a section header with active highlighting.
func (ui *ProviderSelectUI) sectionHeader(title string, active bool) string {
	if active {
		return fmt.Sprintf("  %s%s── %s ─────────────────────%s", Bold, Purple, title, RST)
	}
	return fmt.Sprintf("  %s── %s ─────────────────────%s", Comment, title, RST)
}

// readKeys reads single bytes from stdin in a loop.
func (ui *ProviderSelectUI) readKeys() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ui.keyCh <- buf[0]
	}
}

// cleanup restores the terminal.
func (ui *ProviderSelectUI) cleanup(restoreStty bool) {
	if restoreStty {
		saneCmd := exec.Command("stty", "sane")
		saneCmd.Stdin = os.Stdin
		_ = saneCmd.Run()
	}
	fmt.Print("\033[?25h") // show cursor
	fmt.Print(RST)
	fmt.Print("\033[2J")
	fmt.Print("\033[H")
}

// ExecuteReload runs agent reload(s) directly as a subprocess so output
// is visible in the popup.
func ExecuteReload(session, role, cli, model string, compact bool, roles []string) error {
	targetRoles := roles
	if len(targetRoles) == 0 {
		targetRoles = []string{role}
	}

	// For multi-agent, roles were already handled by the TUI progress view
	// This path is only for single-agent reload
	targetRole := role
	if len(roles) == 1 {
		targetRole = roles[0]
	}

	// Build reload command args
	args := []string{"reload", targetRole}
	if cli != "" {
		args = append(args, "--cli", cli)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if compact {
		args = append(args, "--compact")
	}

	// Resolve our own binary path
	exe, err := os.Executable()
	if err != nil {
		exe = "muxcode"
	}

	// Run reload directly — stdout/stderr visible in the popup
	fmt.Printf("\nReloading %s (cli=%s, model=%s)...\n", targetRole, cli, model)
	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	// Persisted only after the reload succeeds. Writing it first made the
	// reload impossible: `muxcode reload` resolves the running agent's
	// provider to decide how to stop it, and that resolution reads this
	// config. Announcing the destination up front made a running OpenCode
	// agent look like a Claude one, so the stop sent /exit to a TUI that
	// ignores it and the reload timed out with "did not exit after 12
	// seconds". ReloadAgent takes the same care with its runtime override,
	// stopping the agent before writing it.
	//
	// Persisting after also keeps the config honest when a reload fails:
	// the file keeps describing the agent that is actually running.
	persistToConfig(targetRoles, cli, model)
	return nil
}

// persistToConfig writes provider/model choices to the muxcode config file
// for each role. This makes the selection permanent for this subsession.
func persistToConfig(roles []string, cli, model string) {
	for _, r := range roles {
		if cli != "" {
			_ = bus.SetShellConfigValue(bus.RoleCLIEnvVar(r), cli)
		}
		if model != "" {
			_ = bus.SetShellConfigValue(bus.RoleModelEnvVar(r), model)
		}
	}
}
