package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Subscription represents an event subscription for fan-out notifications.
type Subscription struct {
	ID        string `json:"id"`
	Event     string `json:"event"`
	Outcome   string `json:"outcome"`
	Notify    string `json:"notify"`
	Action    string `json:"action"`
	Message   string `json:"message"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	FireCount int    `json:"fire_count"`
}

// ReadSubscriptions reads all subscriptions from the JSONL file.
func ReadSubscriptions(session string) ([]Subscription, error) {
	data, err := os.ReadFile(SubscriptionPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []Subscription
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Subscription
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// WriteSubscriptions overwrites the subscriptions JSONL file with the given entries.
func WriteSubscriptions(session string, entries []Subscription) error {
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(SubscriptionPath(session), buf.Bytes(), 0644)
}

// AddSubscription validates and appends a new subscription. Returns the entry
// with generated ID and CreatedAt fields populated.
func AddSubscription(session string, sub Subscription) (Subscription, error) {
	// Validate notify role
	if !IsKnownRole(sub.Notify) {
		return Subscription{}, fmt.Errorf("unknown notify role: %s", sub.Notify)
	}

	// Validate event
	validEvents := map[string]bool{"build": true, "test": true, "deploy": true, "*": true}
	if !validEvents[sub.Event] {
		return Subscription{}, fmt.Errorf("invalid event: %s (must be build, test, deploy, or *)", sub.Event)
	}

	// Validate outcome
	validOutcomes := map[string]bool{"success": true, "failure": true, "*": true}
	if !validOutcomes[sub.Outcome] {
		return Subscription{}, fmt.Errorf("invalid outcome: %s (must be success, failure, or *)", sub.Outcome)
	}

	sub.ID = NewMsgID("sub")
	sub.CreatedAt = time.Now().Unix()
	sub.Enabled = true
	if sub.Action == "" {
		sub.Action = "notify"
	}
	if sub.Message == "" {
		sub.Message = "${event} ${outcome}: ${command}"
	}

	entries, err := ReadSubscriptions(session)
	if err != nil {
		return Subscription{}, err
	}

	entries = append(entries, sub)
	if err := WriteSubscriptions(session, entries); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// RemoveSubscription removes a subscription by ID.
func RemoveSubscription(session, id string) error {
	entries, err := ReadSubscriptions(session)
	if err != nil {
		return err
	}

	found := false
	var kept []Subscription
	for _, e := range entries {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}

	if !found {
		return fmt.Errorf("subscription not found: %s", id)
	}

	return WriteSubscriptions(session, kept)
}

// SetSubscriptionEnabled enables or disables a subscription by ID.
func SetSubscriptionEnabled(session, id string, enabled bool) error {
	entries, err := ReadSubscriptions(session)
	if err != nil {
		return err
	}

	found := false
	for i, e := range entries {
		if e.ID == id {
			entries[i].Enabled = enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("subscription not found: %s", id)
	}

	return WriteSubscriptions(session, entries)
}

// MatchSubscriptions filters subscriptions that are enabled and match the event+outcome.
func MatchSubscriptions(subs []Subscription, event, outcome string) []Subscription {
	var matched []Subscription
	for _, s := range subs {
		if !s.Enabled {
			continue
		}
		if s.Event != "*" && s.Event != event {
			continue
		}
		if s.Outcome != "*" && s.Outcome != outcome {
			continue
		}
		matched = append(matched, s)
	}
	return matched
}

// FireSubscriptions reads subscriptions, matches against the event/outcome,
// expands message templates, and sends notifications. Returns the count of
// fired subscriptions.
func FireSubscriptions(session, from, event, outcome, exitCode, command string) (int, error) {
	subs, err := ReadSubscriptions(session)
	if err != nil {
		return 0, err
	}

	matched := MatchSubscriptions(subs, event, outcome)
	if len(matched) == 0 {
		return 0, nil
	}

	fired := 0
	notified := make(map[string]bool) // dedupe tmux notifications per role
	for _, s := range matched {
		payload := ExpandSubscriptionMessage(s.Message, event, outcome, exitCode, command)
		msg := NewMessage(from, s.Notify, "event", s.Action, payload, "")
		if err := SendNoCC(session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: subscription %s notify failed: %v\n", s.ID, err)
			continue
		}
		// Send tmux notification so agent wakes up to read the message
		if !notified[s.Notify] {
			_ = Notify(session, s.Notify)
			notified[s.Notify] = true
		}
		fired++
	}

	// Update fire counts
	if fired > 0 {
		// Re-read to get latest state, then increment matched entries
		all, err := ReadSubscriptions(session)
		if err == nil {
			matchedIDs := make(map[string]bool, len(matched))
			for _, s := range matched {
				matchedIDs[s.ID] = true
			}
			for i, e := range all {
				if matchedIDs[e.ID] {
					all[i].FireCount++
				}
			}
			_ = WriteSubscriptions(session, all)
		}
	}

	return fired, nil
}

// ExpandSubscriptionMessage substitutes template variables in a subscription message.
// Supported: ${event}, ${outcome}, ${exit_code}, ${command}
func ExpandSubscriptionMessage(template, event, outcome, exitCode, command string) string {
	s := strings.ReplaceAll(template, "${event}", event)
	s = strings.ReplaceAll(s, "${outcome}", outcome)
	s = strings.ReplaceAll(s, "${exit_code}", exitCode)
	s = strings.ReplaceAll(s, "${command}", command)
	return s
}

// FormatSubscriptionList formats subscriptions as a human-readable table.
// When showAll is false, only enabled entries are shown.
func FormatSubscriptionList(entries []Subscription, showAll bool) string {
	var b strings.Builder

	var filtered []Subscription
	for _, e := range entries {
		if showAll || e.Enabled {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		if showAll {
			b.WriteString("No subscriptions.\n")
		} else {
			b.WriteString("No enabled subscriptions. Use --all to see disabled entries.\n")
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-40s %-8s %-10s %-10s %-8s %-8s %s\n",
		"ID", "Event", "Outcome", "Notify", "Action", "Status", "Fires"))
	b.WriteString(strings.Repeat("-", 100) + "\n")

	for _, e := range filtered {
		status := "enabled"
		if !e.Enabled {
			status = "disabled"
		}
		b.WriteString(fmt.Sprintf("%-40s %-8s %-10s %-10s %-8s %-8s %d\n",
			e.ID, e.Event, e.Outcome, e.Notify, e.Action, status, e.FireCount))
	}

	return b.String()
}
