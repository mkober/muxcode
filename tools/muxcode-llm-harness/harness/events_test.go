package harness

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLogSink_Emit(t *testing.T) {
	var buf bytes.Buffer
	sink := &LogSink{W: &buf, Tag: "gemma4"}

	sink.Emit(Event{
		Kind:    EventStartup,
		Time:    time.Now(),
		Message: "Connected to Ollama",
	})

	output := buf.String()
	if !strings.Contains(output, "[gemma4]") {
		t.Errorf("expected [gemma4] prefix, got: %s", output)
	}
	if !strings.Contains(output, "Connected to Ollama") {
		t.Errorf("expected message in output, got: %s", output)
	}
}

func TestLogSink_MessageReceived_NoPrefixTag(t *testing.T) {
	var buf bytes.Buffer
	sink := &LogSink{W: &buf, Tag: "gemma4"}

	sink.Emit(Event{
		Kind:    EventMessageReceived,
		Time:    time.Now(),
		Message: "[edit → build] Run build",
	})

	output := buf.String()
	// MessageReceived should not have the [tag] prefix
	if strings.Contains(output, "[gemma4]") {
		t.Errorf("MessageReceived should not have tag prefix, got: %s", output)
	}
	if !strings.Contains(output, "[edit → build]") {
		t.Errorf("expected message content, got: %s", output)
	}
}

func TestLogSink_Close(t *testing.T) {
	sink := NewLogSink("test")
	// Should not panic
	sink.Close()
}
