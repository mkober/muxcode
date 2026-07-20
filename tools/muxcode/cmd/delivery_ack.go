package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// DeliveryAck handles the "muxcode delivery-ack [on|off|status]" subcommand.
// Receipt-based delivery is the DEFAULT; this command is the instant, restart-free
// ROLLBACK valve. `off` writes a marker in the bus dir that reverts the session to
// the old pane-scrape delivery path; `on` removes it (restoring the default).
// Because the daemon's ackDeliveryActive() re-reads the marker every poll, the
// flip (and rollback) takes effect immediately with NO daemon restart — unlike the
// startup-only MUXCODE_DELIVERY_ACK env var. MUXCODE_DELIVERY_ACK_DISABLE is a
// stronger env-level kill switch.
//
// Usage: muxcode delivery-ack [on|off|status] [--session <name>]
//
//	on       restore the default receipt-based delivery (remove the rollback marker)
//	off      revert to the old pane-scrape delivery path (write the rollback marker)
//	status   report the current runtime + env state (default)
func DeliveryAck(args []string) {
	sub := "status"
	var session string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				session = args[i]
			}
		case "-h", "--help":
			fmt.Println("Usage: muxcode delivery-ack [on|off|status] [--session <name>]")
			fmt.Println("  on       restore the default receipt-based delivery (remove the rollback marker)")
			fmt.Println("  off      revert to the old pane-scrape delivery path (write the rollback marker)")
			fmt.Println("  status   report the current runtime + env state (default)")
			fmt.Println("  --session <name>  target a different muxcode session (default: current)")
			return
		default:
			if args[i] == "on" || args[i] == "off" || args[i] == "status" {
				sub = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "delivery-ack: unknown argument %q\n", args[i])
				os.Exit(1)
			}
		}
	}
	if session == "" {
		// Env-first (BUS_SESSION/SESSION) like `muxcode inbox`/`deliver`, so a
		// subsession target isn't silently overridden by the current pane's
		// session.
		session = bus.BusSession()
	}

	switch sub {
	case "on":
		if err := bus.SetAckDeliveryOff(session, false); err != nil {
			fmt.Fprintf(os.Stderr, "delivery-ack: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("delivery-ack cutover: ON for session %q — receipt-based delivery active (the default; rollback marker cleared, no daemon restart needed)\n", session)
		if os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != "" {
			fmt.Println("  WARNING: MUXCODE_DELIVERY_ACK_DISABLE is set in this env — the daemon's env may hard-force the OLD path regardless of this marker.")
		}
	case "off":
		if err := bus.SetAckDeliveryOff(session, true); err != nil {
			fmt.Fprintf(os.Stderr, "delivery-ack: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("delivery-ack cutover: OFF for session %q — rolled back to the pane-scrape delivery path (no daemon restart needed)\n", session)
	default: // status
		off := bus.AckDeliveryToggledOff(session)
		// Mirror ackDeliveryActive precedence for THIS process's env (the daemon
		// reads env from its own process, which may differ): DISABLE > explicit
		// env > OFF marker > default (ON).
		active := !off
		envDisable := os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != ""
		if envDisable {
			active = false
		} else {
			switch strings.ToLower(strings.TrimSpace(os.Getenv("MUXCODE_DELIVERY_ACK"))) {
			case "0", "false", "no", "off":
				active = false
			case "1", "true", "yes", "on":
				active = true
			}
		}
		state := "OFF (old pane-scrape delivery)"
		if active {
			state = "ON (receipt-based delivery)"
		}
		fmt.Printf("delivery-ack cutover [%s]: %s\n", session, state)
		fmt.Println("  default: ON (receipt-based delivery)")
		fmt.Printf("  rollback marker: %s (%s)\n", bus.AckDeliveryOffMarkerPath(session), presentOrAbsent(off))
		if v := os.Getenv("MUXCODE_DELIVERY_ACK"); v != "" {
			fmt.Printf("  env MUXCODE_DELIVERY_ACK=%q (pins the cutover at daemon startup)\n", v)
		}
		if envDisable {
			fmt.Println("  env MUXCODE_DELIVERY_ACK_DISABLE set — hard kill switch, forces OLD path")
		}
		fmt.Println("  note: the daemon reads env from its own process; a marker flip needs no restart, an env change does.")
	}
}

func presentOrAbsent(on bool) string {
	if on {
		return "present"
	}
	return "absent"
}
