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
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const resumeBanner = "This session was running agent 'planner', which is no longer available (no agent by that name in %s). " +
	"Continuing with the default tools and system prompt — the agent's tool restrictions no longer apply."

func main() {
	hasAgent, hasAgents, resume := false, false, false
	var prompt strings.Builder
	args := os.Args[1:]
	for i, a := range args {
		switch a {
		case "--agent":
			hasAgent = true
		case "--agents":
			hasAgents = true
		case "--resume", "-r", "--continue", "-c":
			resume = true
		case "--append-system-prompt":
			if i+1 < len(args) {
				prompt.WriteString(args[i+1])
			}
		}
	}

	fmt.Printf("claude-stub pid=%d agent=%v agents=%v resume=%v\n", os.Getpid(), hasAgent, hasAgents, resume)
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
		fmt.Println("Resume this session with: claude --resume 0f3a")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		teardown()
		os.Exit(0)
	}()

	fmt.Print("❯ \n")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimLeft(sc.Text(), "\x1b"))
		if line == "/exit" {
			teardown()
			return
		}
		fmt.Print("❯ \n")
	}
	teardown()
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
