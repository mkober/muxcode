package watcher

import (
	"strings"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// RouteFile routes file-edit events through the bus based on file extension and name.
// It sends events to relevant agents but does not call bus.Notify —
// the watcher's inbox poll loop handles notifications.
func RouteFile(session, filepath string) {
	lower := strings.ToLower(filepath)

	// Test/spec files → test agent
	if strings.Contains(lower, "test") || strings.Contains(lower, "spec") {
		msg := bus.NewMessage("watcher", "test", "event", "notify",
			"Test file changed: "+filepath, "")
		_ = bus.Send(session, msg)
		return
	}

	// CDK/infrastructure files → deploy agent
	if strings.Contains(lower, "cdk") || strings.Contains(lower, "stack") || strings.Contains(lower, "construct") {
		msg := bus.NewMessage("watcher", "deploy", "event", "notify",
			"Infrastructure file changed: "+filepath, "")
		_ = bus.Send(session, msg)
		return
	}

	// Source files → build agent
	if strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".py") {
		msg := bus.NewMessage("watcher", "build", "event", "notify",
			"Source file changed: "+filepath, "")
		_ = bus.Send(session, msg)
		return
	}
}
