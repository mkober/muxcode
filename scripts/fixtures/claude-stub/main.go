// claude-stub is a Claude-shaped process for MUX-136's integration test
// (scripts/test-restart-definition.sh). Built into a scratch bin as `claude`,
// it is what the real launcher execs with the real flag set, so the real
// ProbeAgentDefinition runs against a real process tree — argv[0] must be
// "claude", which no shell script can be (a script's process is its
// interpreter).
//
// It behaves like the parts of Claude Code the daemon observes: prints the
// idle prompt glyph so provider idle detection reads it as alive, starts
// `muxcode inbox --poll --loop` when its system prompt tells it to (as a real
// agent does from the shared prompt), prints Claude's resume banner on a bare
// --resume with no --agent, and exits on /exit (GracefulStop) or Ctrl-C
// (RestartLocalAgent), taking its listener with it.
//
// Its teardown mirrors Claude Code's exit: the TUI restores the screen — no
// prompt glyph left in the pane's last rows — and prints the resume hint, so
// the daemon's liveness heuristic reads the returning shell prompt as dead.
// A SIGKILL skips that and leaves the glyph behind; real Claude evades the
// health sweep the same way, which is why the integration test kills with
// SIGTERM. The teardown also scrolls the prompt onto the pane's bottom row:
// the heuristic reads only the last few ROWS, and a prompt left mid-screen
// with blank rows below it reads as "assume alive" — a real Claude that
// dies before its pane has filled is invisible to the sweep the same way.
//
// The exit banner's session id is read at teardown from the file named by
// CLAUDE_STUB_SESSION_FILE (MUX-126's scripts/test-edit-auto-resume.sh), so a
// test can switch between a resumable UUID and no scrapeable id without
// relaunching the pane. Absent or empty, it is "0f3a" — not a UUID, so a
// restart scrape misses and relaunches fresh, as MUX-136's test expects.
//
// A /exit waits the whole seconds named in CLAUDE_STUB_EXIT_DELAY_FILE, read
// when /exit arrives (MUX-217's scripts/test-reload-defers-dispatch.sh): a
// reload's GracefulStop then holds the reload marker that long, so a graph
// dispatch can be placed inside the stop-to-relaunch window deterministically.
// Absent, empty or 0, /exit is immediate.
//
// Every typed line is appended to the file named by CLAUDE_STUB_TYPED_LOG,
// when set (MUX-232's scripts/test-redrive-reply-route.sh). While the file
// named by CLAUDE_STUB_REPLY_FILE holds "1", a typed line carrying a re-drive's
// `To reply: <command>` runs that command as written, its summary placeholder
// filled — what an agent that did the work does. The stub never answers its
// listener's messages, so a request it consumed stays unanswered until a
// re-drive hands it a command to run.
//
// While the file named by CLAUDE_STUB_BUSY_FILE holds "1" the pane shows
// Claude's working spinner, refreshed every second, so AgentIsWorking reads a
// live turn (MUX-190's scripts/test-guard-denied-redrive.sh); when it stops
// holding "1" the stub prints enough prompt rows to push the spinner out of
// the pane's live tail (recoverableIdleTailLines), and reads idle again.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const resumeBanner = "This session was running agent 'planner', which is no longer available (no agent by that name in %s). " +
	"Continuing with the default tools and system prompt — the agent's tool restrictions no longer apply."

func main() {
	hasAgent, hasAgents, resume, skipPerms := false, false, false, false
	resumeID := ""
	var prompt strings.Builder
	args := os.Args[1:]
	for i, a := range args {
		switch a {
		case "--agent":
			hasAgent = true
		case "--agents":
			hasAgents = true
		case "--dangerously-skip-permissions":
			skipPerms = true
		case "--resume", "-r", "--continue", "-c":
			resume = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				resumeID = args[i+1]
			}
		case "--append-system-prompt":
			if i+1 < len(args) {
				prompt.WriteString(args[i+1])
			}
		}
	}

	fmt.Printf("claude-stub pid=%d agent=%v agents=%v resume=%v skip-perms=%v resume-id=%s\n",
		os.Getpid(), hasAgent, hasAgents, resume, skipPerms, resumeID)
	if resume && !hasAgent {
		cwd, _ := os.Getwd()
		fmt.Printf(resumeBanner+"\n", cwd)
	}

	var mu sync.Mutex
	var listener *exec.Cmd
	stopped := false
	if strings.Contains(prompt.String(), "muxcode inbox --poll --loop") {
		go superviseListener(&mu, &listener, &stopped)
	}
	teardown := func() { // screen restore + resume hint — see package doc
		mu.Lock()
		stopped = true
		if listener != nil && listener.Process != nil {
			_ = listener.Process.Kill()
		}
		mu.Unlock()
		fmt.Print(strings.Repeat("\n", 60)) // past any pane height: the prompt must land on the bottom row
		fmt.Println("Resume this session with: claude --resume " + bannerSessionID())
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		teardown()
		os.Exit(0)
	}()

	go showBusy()
	fmt.Print("❯ \n")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimLeft(sc.Text(), "\x1b"))
		if line == "/exit" {
			exitDelay()
			teardown()
			return
		}
		handleTyped(line)
		fmt.Print("❯ \n")
	}
	teardown()
}

// handleTyped logs a typed line and, when replies are on, runs the reply
// command a re-drive carries — see the package doc.
func handleTyped(line string) {
	if p := os.Getenv("CLAUDE_STUB_TYPED_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
	i := strings.LastIndex(line, "To reply: ")
	if i < 0 {
		return
	}
	if b, err := os.ReadFile(os.Getenv("CLAUDE_STUB_REPLY_FILE")); err != nil || strings.TrimSpace(string(b)) != "1" {
		return
	}
	cmd := line[i+len("To reply: "):]
	if j := strings.Index(cmd, " | "); j >= 0 {
		cmd = cmd[:j]
	}
	cmd = strings.ReplaceAll(cmd, "<your one-line summary>", "Re-drive answered. EXIT=0")
	_ = exec.Command("bash", "-c", cmd).Run()
}

// showBusy renders the working spinner while CLAUDE_STUB_BUSY_FILE holds "1"
// — see the package doc.
func showBusy() {
	busy := false
	for secs := 1; ; time.Sleep(time.Second) {
		b, _ := os.ReadFile(os.Getenv("CLAUDE_STUB_BUSY_FILE"))
		now := strings.TrimSpace(string(b)) == "1"
		switch {
		case now:
			fmt.Printf("✻ Working… (%ds · esc to interrupt)\n", secs)
			secs++
		case busy:
			fmt.Print(strings.Repeat("❯ \n", 12))
			secs = 1
		}
		busy = now
	}
}

// exitDelay holds a /exit — see the package doc.
func exitDelay() {
	b, err := os.ReadFile(os.Getenv("CLAUDE_STUB_EXIT_DELAY_FILE"))
	if err != nil {
		return
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
		time.Sleep(time.Duration(n) * time.Second)
	}
}

// bannerSessionID is the id the exit banner offers — see the package doc.
func bannerSessionID() string {
	if b, err := os.ReadFile(os.Getenv("CLAUDE_STUB_SESSION_FILE")); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	return "0f3a"
}

// superviseListener keeps one `muxcode inbox --poll --loop` running, the way
// a real agent's Stop hook does: the listener returns as soon as it delivers
// a message (the launcher's startup request, a bus send), the agent handles
// it, and a fresh listener is launched. Without the relaunch the polling
// marker is released moments after launch and the pane looks unreachable.
func superviseListener(mu *sync.Mutex, listener **exec.Cmd, stopped *bool) {
	for {
		cmd := exec.Command("muxcode", "inbox", "--poll", "--loop")
		if err := cmd.Start(); err != nil {
			fmt.Printf("claude-stub: listener failed: %v\n", err)
			return
		}
		mu.Lock()
		*listener = cmd
		mu.Unlock()
		fmt.Printf("claude-stub: listener pid=%d\n❯ \n", cmd.Process.Pid)
		_ = cmd.Wait()
		mu.Lock()
		done := *stopped
		mu.Unlock()
		if done {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
