package harness

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Special key constants for terminal input handling.
const (
	keyNone = iota
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyUp
	keyDown
	keyCtrlC
	keyCtrlD
	keyCtrlU // clear line
	keyCtrlW // delete word backward
	keyEscape
	keyTab
)

// keyPress represents a single parsed key event from raw terminal input.
type keyPress struct {
	Rune rune // printable character (0 if special key)
	Key  int  // special key constant (keyNone if printable)
}

// tuiInput manages the text input area state: buffer, cursor, history,
// and channels for communicating with the key reader and harness loop.
type tuiInput struct {
	mu       sync.Mutex
	buf      []rune
	cursor   int
	submitCh chan string   // submitted messages → harness loop
	keyCh    chan keyPress // raw key events from reader goroutine
	history  []string     // input history (most recent last)
	histIdx  int          // -1 = current input, 0+ = history index
	savedBuf []rune       // saved current input while browsing history
	quitCh   chan struct{} // signals user wants to quit (Ctrl+C on empty)
}

func newTuiInput() *tuiInput {
	return &tuiInput{
		submitCh: make(chan string, 16),
		keyCh:    make(chan keyPress, 256),
		quitCh:   make(chan struct{}),
		histIdx:  -1,
	}
}

// readKeys reads from stdin in raw mode and sends parsed key events.
// Blocks until stdin is closed or an error occurs. Run in a goroutine.
func (inp *tuiInput) readKeys() {
	buf := make([]byte, 64)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}
		inp.parseBytes(buf[:n])
	}
}

// parseBytes interprets raw terminal bytes into key events.
func (inp *tuiInput) parseBytes(data []byte) {
	i := 0
	for i < len(data) {
		b := data[i]
		switch {
		case b == 0x0D || b == 0x0A: // CR or LF → Enter
			inp.keyCh <- keyPress{Key: keyEnter}
			i++
		case b == 0x7F || b == 0x08: // DEL or BS → Backspace
			inp.keyCh <- keyPress{Key: keyBackspace}
			i++
		case b == 0x03: // Ctrl+C
			inp.keyCh <- keyPress{Key: keyCtrlC}
			i++
		case b == 0x04: // Ctrl+D
			inp.keyCh <- keyPress{Key: keyCtrlD}
			i++
		case b == 0x15: // Ctrl+U → clear line
			inp.keyCh <- keyPress{Key: keyCtrlU}
			i++
		case b == 0x17: // Ctrl+W → delete word
			inp.keyCh <- keyPress{Key: keyCtrlW}
			i++
		case b == 0x01: // Ctrl+A → Home
			inp.keyCh <- keyPress{Key: keyHome}
			i++
		case b == 0x05: // Ctrl+E → End
			inp.keyCh <- keyPress{Key: keyEnd}
			i++
		case b == 0x09: // Tab (ignore for now)
			inp.keyCh <- keyPress{Key: keyTab}
			i++
		case b == 0x1B: // ESC
			if i+2 < len(data) && data[i+1] == '[' {
				// CSI escape sequence
				switch data[i+2] {
				case 'A':
					inp.keyCh <- keyPress{Key: keyUp}
					i += 3
				case 'B':
					inp.keyCh <- keyPress{Key: keyDown}
					i += 3
				case 'C':
					inp.keyCh <- keyPress{Key: keyRight}
					i += 3
				case 'D':
					inp.keyCh <- keyPress{Key: keyLeft}
					i += 3
				case 'H':
					inp.keyCh <- keyPress{Key: keyHome}
					i += 3
				case 'F':
					inp.keyCh <- keyPress{Key: keyEnd}
					i += 3
				case '3': // Delete: ESC [ 3 ~
					if i+3 < len(data) && data[i+3] == '~' {
						inp.keyCh <- keyPress{Key: keyDelete}
						i += 4
					} else {
						i += 3
					}
				default:
					// Skip unknown CSI sequence
					j := i + 2
					for j < len(data) && data[j] >= 0x20 && data[j] <= 0x3F {
						j++
					}
					if j < len(data) {
						j++ // skip final byte
					}
					i = j
				}
			} else {
				inp.keyCh <- keyPress{Key: keyEscape}
				i++
			}
		case b >= 0x20 && b < 0x7F: // Printable ASCII
			inp.keyCh <- keyPress{Rune: rune(b)}
			i++
		case b >= 0xC0: // UTF-8 multi-byte start
			width := 2
			if b >= 0xF0 {
				width = 4
			} else if b >= 0xE0 {
				width = 3
			}
			if i+width <= len(data) {
				r := decodeFirstRune(data[i : i+width])
				if r > 0 {
					inp.keyCh <- keyPress{Rune: r}
				}
				i += width
			} else {
				i++ // incomplete sequence, skip
			}
		default:
			i++ // skip unknown control characters
		}
	}
}

// decodeFirstRune decodes the first rune from a UTF-8 byte slice.
func decodeFirstRune(b []byte) rune {
	s := string(b)
	for _, r := range s {
		return r
	}
	return 0
}

// processKey handles a single key press, updating input state.
// Returns true if the TUI should re-render immediately.
func (inp *tuiInput) processKey(kp keyPress) bool {
	inp.mu.Lock()
	defer inp.mu.Unlock()

	switch {
	case kp.Key == keyEnter:
		text := strings.TrimSpace(string(inp.buf))
		if text != "" {
			inp.history = append(inp.history, text)
			select {
			case inp.submitCh <- text:
			default:
			}
		}
		inp.buf = inp.buf[:0]
		inp.cursor = 0
		inp.histIdx = -1
		inp.savedBuf = nil
		return true

	case kp.Key == keyBackspace:
		if inp.cursor > 0 {
			inp.buf = append(inp.buf[:inp.cursor-1], inp.buf[inp.cursor:]...)
			inp.cursor--
			return true
		}

	case kp.Key == keyDelete:
		if inp.cursor < len(inp.buf) {
			inp.buf = append(inp.buf[:inp.cursor], inp.buf[inp.cursor+1:]...)
			return true
		}

	case kp.Key == keyLeft:
		if inp.cursor > 0 {
			inp.cursor--
			return true
		}

	case kp.Key == keyRight:
		if inp.cursor < len(inp.buf) {
			inp.cursor++
			return true
		}

	case kp.Key == keyHome:
		if inp.cursor > 0 {
			inp.cursor = 0
			return true
		}

	case kp.Key == keyEnd:
		if inp.cursor < len(inp.buf) {
			inp.cursor = len(inp.buf)
			return true
		}

	case kp.Key == keyCtrlC:
		if len(inp.buf) > 0 {
			inp.buf = inp.buf[:0]
			inp.cursor = 0
			inp.histIdx = -1
			inp.savedBuf = nil
			return true
		}
		// Empty buffer + Ctrl+C → signal quit
		select {
		case <-inp.quitCh:
		default:
			close(inp.quitCh)
		}
		return false

	case kp.Key == keyCtrlD:
		// Ctrl+D on empty → signal quit
		if len(inp.buf) == 0 {
			select {
			case <-inp.quitCh:
			default:
				close(inp.quitCh)
			}
		}
		return false

	case kp.Key == keyCtrlU:
		if len(inp.buf) > 0 {
			inp.buf = inp.buf[:0]
			inp.cursor = 0
			return true
		}

	case kp.Key == keyCtrlW:
		if inp.cursor > 0 {
			end := inp.cursor
			// Skip trailing spaces
			for inp.cursor > 0 && inp.buf[inp.cursor-1] == ' ' {
				inp.cursor--
			}
			// Skip word characters
			for inp.cursor > 0 && inp.buf[inp.cursor-1] != ' ' {
				inp.cursor--
			}
			inp.buf = append(inp.buf[:inp.cursor], inp.buf[end:]...)
			return true
		}

	case kp.Key == keyUp:
		if len(inp.history) > 0 {
			if inp.histIdx == -1 {
				inp.savedBuf = make([]rune, len(inp.buf))
				copy(inp.savedBuf, inp.buf)
				inp.histIdx = len(inp.history) - 1
			} else if inp.histIdx > 0 {
				inp.histIdx--
			} else {
				return false
			}
			inp.buf = []rune(inp.history[inp.histIdx])
			inp.cursor = len(inp.buf)
			return true
		}

	case kp.Key == keyDown:
		if inp.histIdx >= 0 {
			if inp.histIdx < len(inp.history)-1 {
				inp.histIdx++
				inp.buf = []rune(inp.history[inp.histIdx])
			} else {
				inp.histIdx = -1
				if inp.savedBuf != nil {
					inp.buf = inp.savedBuf
				} else {
					inp.buf = nil
				}
				inp.savedBuf = nil
			}
			inp.cursor = len(inp.buf)
			return true
		}

	case kp.Key == keyEscape:
		if len(inp.buf) > 0 {
			inp.buf = inp.buf[:0]
			inp.cursor = 0
			inp.histIdx = -1
			inp.savedBuf = nil
			return true
		}

	case kp.Rune > 0:
		// Insert printable character at cursor
		newBuf := make([]rune, len(inp.buf)+1)
		copy(newBuf, inp.buf[:inp.cursor])
		newBuf[inp.cursor] = kp.Rune
		copy(newBuf[inp.cursor+1:], inp.buf[inp.cursor:])
		inp.buf = newBuf
		inp.cursor++
		return true
	}

	return false
}

// getInputState returns a snapshot of the current input for rendering.
func (inp *tuiInput) getInputState() (text []rune, cursor int) {
	inp.mu.Lock()
	defer inp.mu.Unlock()
	out := make([]rune, len(inp.buf))
	copy(out, inp.buf)
	return out, inp.cursor
}

// --- Raw terminal mode via stty ---

// sttyRawMode enters raw terminal mode for character-at-a-time input
// with no echo and no signal processing. Returns a restore function.
func sttyRawMode() (restore func(), err error) {
	// Save current terminal state
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	saved := strings.TrimSpace(string(out))

	// Enter raw mode: no canonical processing, no echo, no signal chars
	cmd = exec.Command("stty", "-icanon", "-echo", "-isig", "min", "1")
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return func() {
		cmd := exec.Command("stty", saved)
		cmd.Stdin = os.Stdin
		_ = cmd.Run()
	}, nil
}
