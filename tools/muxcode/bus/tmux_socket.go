package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tmuxSocketFile names the bus-dir file recording where a session runs: line
// one the socket of its tmux server, line two its tmux session id. The reaper
// judges a session dead only from that server — absence from any other, a
// test's private TMUX_TMPDIR or a `tmux -L` server, says nothing about it —
// and only when neither its name nor its id is listed there: the id survives
// `tmux rename-session`, under which the name alone reads as gone (MUX-184).
const tmuxSocketFile = "tmux.socket"

// tmuxSocketOf asks tmux for the socket and session id of the server holding
// session; a var so tests can answer for it. It reads list-sessions, not
// `display-message -t`, which with no attached client returns the socket and
// an empty #{session_id} (tmux 3.8, measured for MUX-184).
var tmuxSocketOf = func(session string) (socket, id string, err error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{socket_path}\t#{session_name}\t#{session_id}").Output()
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 && f[1] == session && f[2] != "" {
			return f[0], f[2], nil
		}
	}
	return "", "", fmt.Errorf("tmux lists no session %q", session)
}

// RecordTmuxSocket writes the server and session id holding session into its
// bus dir, so a later sweep can tell its server from another and a renamed
// session from a closed one, and reports whether it did. A session tmux
// cannot find keeps whatever was recorded before. The daemon retries each
// poll until it succeeds — at a launch it starts before the session exists —
// so every daemon start, upgrade-daemons rollouts included, refreshes it.
func RecordTmuxSocket(session string) bool {
	socket, id, err := tmuxSocketOf(session)
	if err != nil || socket == "" {
		return false
	}
	path := filepath.Join(BusDir(session), tmuxSocketFile)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(socket+"\n"+id+"\n"), 0644) != nil {
		return false
	}
	return os.Rename(tmp, path) == nil
}

// readTmuxRecord reads a bus dir's tmux.socket: the socket, and the session id
// when the record carries one.
func readTmuxRecord(dir string) (socket, id string, err error) {
	data, err := os.ReadFile(filepath.Join(dir, tmuxSocketFile))
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	socket = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		id = strings.TrimSpace(lines[1])
	}
	return socket, id, nil
}
