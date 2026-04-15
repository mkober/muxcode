package harness

import (
	"testing"
)

func TestTuiInput_InsertChars(t *testing.T) {
	inp := newTuiInput()

	inp.processKey(keyPress{Rune: 'h'})
	inp.processKey(keyPress{Rune: 'e'})
	inp.processKey(keyPress{Rune: 'l'})
	inp.processKey(keyPress{Rune: 'l'})
	inp.processKey(keyPress{Rune: 'o'})

	text, cursor := inp.getInputState()
	if string(text) != "hello" {
		t.Errorf("text = %q, want hello", string(text))
	}
	if cursor != 5 {
		t.Errorf("cursor = %d, want 5", cursor)
	}
}

func TestTuiInput_Backspace(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'a'})
	inp.processKey(keyPress{Rune: 'b'})
	inp.processKey(keyPress{Rune: 'c'})
	inp.processKey(keyPress{Key: keyBackspace})

	text, cursor := inp.getInputState()
	if string(text) != "ab" {
		t.Errorf("text = %q, want ab", string(text))
	}
	if cursor != 2 {
		t.Errorf("cursor = %d, want 2", cursor)
	}
}

func TestTuiInput_BackspaceAtStart(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Key: keyBackspace}) // should be no-op
	text, _ := inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty", string(text))
	}
}

func TestTuiInput_ArrowKeys(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'a'})
	inp.processKey(keyPress{Rune: 'b'})
	inp.processKey(keyPress{Rune: 'c'})

	// Move left twice
	inp.processKey(keyPress{Key: keyLeft})
	inp.processKey(keyPress{Key: keyLeft})

	_, cursor := inp.getInputState()
	if cursor != 1 {
		t.Errorf("cursor = %d, want 1", cursor)
	}

	// Insert at cursor
	inp.processKey(keyPress{Rune: 'X'})
	text, cursor := inp.getInputState()
	if string(text) != "aXbc" {
		t.Errorf("text = %q, want aXbc", string(text))
	}
	if cursor != 2 {
		t.Errorf("cursor = %d, want 2", cursor)
	}

	// Move right
	inp.processKey(keyPress{Key: keyRight})
	_, cursor = inp.getInputState()
	if cursor != 3 {
		t.Errorf("cursor = %d, want 3", cursor)
	}
}

func TestTuiInput_HomeEnd(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'a'})
	inp.processKey(keyPress{Rune: 'b'})
	inp.processKey(keyPress{Rune: 'c'})

	inp.processKey(keyPress{Key: keyHome})
	_, cursor := inp.getInputState()
	if cursor != 0 {
		t.Errorf("cursor = %d, want 0", cursor)
	}

	inp.processKey(keyPress{Key: keyEnd})
	_, cursor = inp.getInputState()
	if cursor != 3 {
		t.Errorf("cursor = %d, want 3", cursor)
	}
}

func TestTuiInput_Delete(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'a'})
	inp.processKey(keyPress{Rune: 'b'})
	inp.processKey(keyPress{Rune: 'c'})

	inp.processKey(keyPress{Key: keyHome})
	inp.processKey(keyPress{Key: keyDelete}) // delete 'a'

	text, cursor := inp.getInputState()
	if string(text) != "bc" {
		t.Errorf("text = %q, want bc", string(text))
	}
	if cursor != 0 {
		t.Errorf("cursor = %d, want 0", cursor)
	}
}

func TestTuiInput_CtrlU_ClearLine(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'h'})
	inp.processKey(keyPress{Rune: 'i'})

	inp.processKey(keyPress{Key: keyCtrlU})

	text, cursor := inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty", string(text))
	}
	if cursor != 0 {
		t.Errorf("cursor = %d, want 0", cursor)
	}
}

func TestTuiInput_CtrlW_DeleteWord(t *testing.T) {
	inp := newTuiInput()
	for _, r := range "hello world" {
		inp.processKey(keyPress{Rune: r})
	}

	inp.processKey(keyPress{Key: keyCtrlW})

	text, cursor := inp.getInputState()
	if string(text) != "hello " {
		t.Errorf("text = %q, want 'hello '", string(text))
	}
	if cursor != 6 {
		t.Errorf("cursor = %d, want 6", cursor)
	}

	inp.processKey(keyPress{Key: keyCtrlW})
	text, _ = inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty after second ctrl-w", string(text))
	}
}

func TestTuiInput_Submit(t *testing.T) {
	inp := newTuiInput()
	for _, r := range "build it" {
		inp.processKey(keyPress{Rune: r})
	}

	inp.processKey(keyPress{Key: keyEnter})

	// Should have submitted
	select {
	case msg := <-inp.submitCh:
		if msg != "build it" {
			t.Errorf("submitted = %q, want 'build it'", msg)
		}
	default:
		t.Error("no message submitted")
	}

	// Buffer should be cleared
	text, cursor := inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty after submit", string(text))
	}
	if cursor != 0 {
		t.Errorf("cursor = %d, want 0 after submit", cursor)
	}
}

func TestTuiInput_SubmitEmpty(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Key: keyEnter}) // empty submit → nothing

	select {
	case msg := <-inp.submitCh:
		t.Errorf("should not submit empty, got %q", msg)
	default:
		// expected: no message
	}
}

func TestTuiInput_SubmitWhitespace(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: ' '})
	inp.processKey(keyPress{Rune: ' '})
	inp.processKey(keyPress{Key: keyEnter}) // whitespace-only → nothing

	select {
	case msg := <-inp.submitCh:
		t.Errorf("should not submit whitespace, got %q", msg)
	default:
		// expected
	}
}

func TestTuiInput_History(t *testing.T) {
	inp := newTuiInput()

	// Submit two messages
	for _, r := range "first" {
		inp.processKey(keyPress{Rune: r})
	}
	inp.processKey(keyPress{Key: keyEnter})
	<-inp.submitCh

	for _, r := range "second" {
		inp.processKey(keyPress{Rune: r})
	}
	inp.processKey(keyPress{Key: keyEnter})
	<-inp.submitCh

	// Up arrow → "second"
	inp.processKey(keyPress{Key: keyUp})
	text, _ := inp.getInputState()
	if string(text) != "second" {
		t.Errorf("text = %q, want 'second'", string(text))
	}

	// Up arrow → "first"
	inp.processKey(keyPress{Key: keyUp})
	text, _ = inp.getInputState()
	if string(text) != "first" {
		t.Errorf("text = %q, want 'first'", string(text))
	}

	// Down arrow → "second"
	inp.processKey(keyPress{Key: keyDown})
	text, _ = inp.getInputState()
	if string(text) != "second" {
		t.Errorf("text = %q, want 'second'", string(text))
	}

	// Down arrow → back to empty (current)
	inp.processKey(keyPress{Key: keyDown})
	text, _ = inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty (back to current)", string(text))
	}
}

func TestTuiInput_CtrlC_ClearsInput(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'x'})
	inp.processKey(keyPress{Key: keyCtrlC})

	text, _ := inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty after Ctrl+C", string(text))
	}
}

func TestTuiInput_CtrlC_EmptyQuits(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Key: keyCtrlC}) // empty input → quit

	select {
	case <-inp.quitCh:
		// expected
	default:
		t.Error("expected quitCh to be closed")
	}
}

func TestTuiInput_Escape_ClearsInput(t *testing.T) {
	inp := newTuiInput()
	inp.processKey(keyPress{Rune: 'x'})
	inp.processKey(keyPress{Key: keyEscape})

	text, _ := inp.getInputState()
	if string(text) != "" {
		t.Errorf("text = %q, want empty after Escape", string(text))
	}
}

func TestParseBytes_PrintableASCII(t *testing.T) {
	inp := newTuiInput()
	inp.parseBytes([]byte("abc"))

	for _, want := range []rune{'a', 'b', 'c'} {
		select {
		case kp := <-inp.keyCh:
			if kp.Rune != want {
				t.Errorf("got rune %q, want %q", kp.Rune, want)
			}
		default:
			t.Errorf("missing key event for %q", want)
		}
	}
}

func TestParseBytes_SpecialKeys(t *testing.T) {
	inp := newTuiInput()

	tests := []struct {
		data []byte
		want int
	}{
		{[]byte{0x0D}, keyEnter},           // CR
		{[]byte{0x0A}, keyEnter},           // LF
		{[]byte{0x7F}, keyBackspace},       // DEL
		{[]byte{0x03}, keyCtrlC},           // Ctrl+C
		{[]byte{0x15}, keyCtrlU},           // Ctrl+U
		{[]byte{0x01}, keyHome},            // Ctrl+A
		{[]byte{0x05}, keyEnd},             // Ctrl+E
		{[]byte{0x1B, '[', 'A'}, keyUp},    // ESC[A
		{[]byte{0x1B, '[', 'B'}, keyDown},  // ESC[B
		{[]byte{0x1B, '[', 'C'}, keyRight}, // ESC[C
		{[]byte{0x1B, '[', 'D'}, keyLeft},  // ESC[D
	}

	for _, tt := range tests {
		inp.parseBytes(tt.data)
		select {
		case kp := <-inp.keyCh:
			if kp.Key != tt.want {
				t.Errorf("data %v: got key %d, want %d", tt.data, kp.Key, tt.want)
			}
		default:
			t.Errorf("data %v: no key event", tt.data)
		}
	}
}

func TestParseBytes_UTF8(t *testing.T) {
	inp := newTuiInput()
	// "é" is 0xC3 0xA9
	inp.parseBytes([]byte{0xC3, 0xA9})

	select {
	case kp := <-inp.keyCh:
		if kp.Rune != 'é' {
			t.Errorf("got rune %q, want 'é'", kp.Rune)
		}
	default:
		t.Error("missing key event for UTF-8 char")
	}
}
