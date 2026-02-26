package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Webhook handles the "muxcode-agent-bus webhook" subcommand.
func Webhook(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus webhook <start|stop|status|serve> [flags]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "start":
		webhookStart(subArgs)
	case "stop":
		webhookStop(subArgs)
	case "status":
		webhookStatus(subArgs)
	case "serve":
		webhookServe(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown webhook subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus webhook <start|stop|status|serve> [flags]\n")
		os.Exit(1)
	}
}

// webhookStart launches the webhook server as a detached background process.
func webhookStart(args []string) {
	port := "9090"
	host := "127.0.0.1"
	token := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --port requires a value\n")
				os.Exit(1)
			}
			i++
			port = args[i]
		case "--host":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --host requires a value\n")
				os.Exit(1)
			}
			i++
			host = args[i]
		case "--token":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --token requires a value\n")
				os.Exit(1)
			}
			i++
			token = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	// Check if already running
	if bus.IsWebhookRunning(session) {
		fmt.Fprintf(os.Stderr, "Error: webhook is already running\n")
		fmt.Fprintln(os.Stderr, bus.WebhookStatus(session))
		os.Exit(1)
	}

	// Find the current binary path for re-exec
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable: %v\n", err)
		os.Exit(1)
	}

	// Build serve args
	serveArgs := []string{"webhook", "serve", "--port", port, "--host", host}
	if token != "" {
		serveArgs = append(serveArgs, "--token", token)
	}

	// Launch detached process
	cmd := exec.Command(exe, serveArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting webhook: %v\n", err)
		os.Exit(1)
	}

	// Detach from child
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	// Wait for server to become healthy (up to 3 seconds)
	healthURL := fmt.Sprintf("http://%s:%s/health", host, port)
	healthy := false
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
	}

	if !healthy {
		fmt.Fprintf(os.Stderr, "Warning: webhook started (PID %d) but health check not responding\n", pid)
	}

	fmt.Printf("Webhook server started on %s:%s (PID %d)\n", host, port, pid)
}

// webhookStop stops the running webhook server.
func webhookStop(args []string) {
	session := bus.BusSession()

	if err := bus.StopWebhookProcess(session); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Webhook server stopped")
}

// webhookStatus shows the current webhook server status.
func webhookStatus(args []string) {
	session := bus.BusSession()
	fmt.Println(bus.WebhookStatus(session))
}

// webhookServe runs the HTTP server in the foreground (used by start).
func webhookServe(args []string) {
	port := 9090
	host := "127.0.0.1"
	token := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --port requires a value\n")
				os.Exit(1)
			}
			i++
			p, err := strconv.Atoi(args[i])
			if err != nil || p < 1 || p > 65535 {
				fmt.Fprintf(os.Stderr, "Error: --port must be a number between 1 and 65535\n")
				os.Exit(1)
			}
			port = p
		case "--host":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --host requires a value\n")
				os.Exit(1)
			}
			i++
			host = args[i]
		case "--token":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --token requires a value\n")
				os.Exit(1)
			}
			i++
			token = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	cfg := bus.WebhookConfig{
		Host:    host,
		Port:    port,
		Token:   token,
		Session: session,
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := bus.ServeWebhook(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
