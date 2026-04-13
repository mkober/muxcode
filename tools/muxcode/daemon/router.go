package daemon

// File routing is handled entirely by routeTrigger() in daemon.go,
// which sends aggregate file-edit events to the analyze agent only.
// Build, test, and deploy agents are never notified of file changes —
// they run only when explicitly requested via the message bus.
