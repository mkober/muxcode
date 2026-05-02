package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Section indices for keyboard navigation.
const (
	sectionProvider = 0
	sectionModel    = 1
	sectionOptions  = 2
	sectionButtons  = 3
)

const numSections = 4

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

	// Options
	compact bool
	persist bool

	// Button focus (0=Reload, 1=Cancel)
	buttonFocus int

	// Custom model input
	customModel  string
	customActive bool // true when typing a custom model

	// Terminal
	keyCh chan byte
}

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

	return &ProviderSelectUI{
		session:     session,
		role:        role,
		window:      window,
		providers:   providers,
		selectedCLI: selectedCLI,
		selectedMdl: selectedMdl,
		section:     sectionProvider,
		cursorRow:   selectedCLI,
	}
}

// Run starts the interactive TUI loop. Returns the selected CLI, model,
// compact, and persist flags — or empty strings if cancelled.
func (ui *ProviderSelectUI) Run() (cli, model string, compact, persist bool, cancelled bool) {
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
		frame := ui.render()
		fmt.Print("\033[H")
		fmt.Print(frame)
		fmt.Print("\033[J")

		// Wait for key input
		select {
		case <-sigCh:
			return "", "", false, false, true
		case key := <-ui.keyCh:
			action := ui.handleKey(key)
			switch action {
			case "cancel":
				return "", "", false, false, true
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
				return cli, model, ui.compact, ui.persist, false
			}
		}
	}
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
	case sectionOptions:
		if ui.cursorRow > 0 {
			ui.cursorRow--
		} else {
			ui.section = sectionModel
			p := &ui.providers[ui.selectedCLI]
			ui.cursorRow = len(p.Models) // "custom..." row
		}
	case sectionButtons:
		ui.section = sectionOptions
		ui.cursorRow = 1 // last option
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
			ui.section = sectionOptions
			ui.cursorRow = 0
		}
	case sectionOptions:
		if ui.cursorRow < 1 { // 2 options: compact, persist
			ui.cursorRow++
		} else {
			ui.section = sectionButtons
			ui.buttonFocus = 0
		}
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
	case sectionOptions:
		if ui.cursorRow == 0 {
			ui.compact = !ui.compact
		} else {
			ui.persist = !ui.persist
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

	// Options section
	active = ui.section == sectionOptions
	b.WriteString(ui.sectionHeader("Options", active))
	b.WriteString("\n")

	options := []struct {
		label   string
		checked bool
	}{
		{"Compact before reload", ui.compact},
		{"Persist to config", ui.persist},
	}
	for i, opt := range options {
		cursor := "  "
		if active && ui.cursorRow == i {
			cursor = Purple + "> " + RST
		}
		check := "[ ]"
		if opt.checked {
			check = Green + "[x]" + RST
		}
		b.WriteString(fmt.Sprintf("  %s %s %s\n", cursor, check, opt.label))
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
	b.WriteString(fmt.Sprintf("  %s[ Reload ]%s  %s[ Cancel ]%s\n",
		reloadStyle, RST, cancelStyle, RST))
	b.WriteString("\n")

	// Help line
	b.WriteString(fmt.Sprintf("  %s↑↓ Navigate  ␣ Select  ⏎ Reload  Tab/S-Tab Section  q Quit%s\n", Comment, RST))

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

// ExecuteReload runs the agent reload directly as a subprocess so output
// is visible in the popup. Previously used a trigger file but that was
// fragile — the shell trigger check could silently fail.
func ExecuteReload(session, role, cli, model string, compact, persist bool) error {
	if persist {
		// Write to persistent config file
		cliKey := bus.RoleCLIEnvVar(role)
		modelKey := bus.RoleModelEnvVar(role)
		if cli != "" {
			_ = bus.SetShellConfigValue(cliKey, cli)
		}
		if model != "" {
			_ = bus.SetShellConfigValue(modelKey, model)
		}
	}

	// Build reload command args
	args := []string{"reload", role}
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
	fmt.Printf("\nReloading %s (cli=%s, model=%s)...\n", role, cli, model)
	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
