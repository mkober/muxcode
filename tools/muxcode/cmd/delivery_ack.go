package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// DeliveryAck handles the "muxcode delivery-ack [on|off|status]" subcommand. It
// flips the receipt-based delivery cutover at runtime via a marker file in the
// bus dir. Because the daemon's ackDeliveryActive() re-reads the marker every
// poll, the flip (and rollback) takes effect immediately with NO daemon restart —
// unlike the startup-only MUXCODE_DELIVERY_ACK env var. This is the safe way to
// activate/test the cutover on a live session and the operational kill valve.
//
// Usage: muxcode delivery-ack [on|off|status] [--session <name>]
//
//	on       activate receipt-based delivery (create the marker)
//	off      revert to the old pane-scrape delivery path (remove the marker)
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
			fmt.Println("  on       activate receipt-based delivery (create the marker)")
			fmt.Println("  off      revert to the old pane-scrape delivery path (remove the marker)")
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
		if err := bus.SetAckDeliveryToggle(session, true); err != nil {
			fmt.Fprintf(os.Stderr, "delivery-ack: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("delivery-ack cutover: ON for session %q — receipt-based delivery active (no daemon restart needed)\n", session)
		if os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != "" {
			fmt.Println("  WARNING: MUXCODE_DELIVERY_ACK_DISABLE is set in this env — the daemon's env may hard-force the OLD path regardless of this marker.")
		}
	case "off":
		if err := bus.SetAckDeliveryToggle(session, false); err != nil {
			fmt.Fprintf(os.Stderr, "delivery-ack: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("delivery-ack cutover: OFF for session %q — reverted to the pane-scrape delivery path\n", session)
	default: // status
		on := bus.AckDeliveryToggleOn(session)
		state := "OFF (old pane-scrape delivery)"
		if on {
			state = "ON (receipt-based delivery)"
		}
		fmt.Printf("delivery-ack cutover [%s]: %s\n", session, state)
		fmt.Printf("  marker: %s (%s)\n", bus.AckDeliveryTogglePath(session), presentOrAbsent(on))
		if v := os.Getenv("MUXCODE_DELIVERY_ACK"); v != "" {
			fmt.Printf("  env MUXCODE_DELIVERY_ACK=%q (activates cutover at daemon startup)\n", v)
		}
		if os.Getenv("MUXCODE_DELIVERY_ACK_DISABLE") != "" {
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
