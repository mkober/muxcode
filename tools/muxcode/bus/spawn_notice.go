package bus

import (
	"fmt"
	"os"
	"slices"
)

// SpawnNotice is one completion a worker's owner is owed word of. Key is the
// seed id of the agent task that completed, or reapNoticeKey of a worker the
// reaper completed.
type SpawnNotice struct {
	Entry SpawnEntry
	Key   string
}

// DueSpawnNotices lists the completions owners are owed and have not been
// told of (NoticesOwed): an agent's task once its worker answers that task's
// own seed — each queued task in turn, while the worker works on — or once
// the worker ends unanswered; and a worker the reaper completed. It reads
// persisted state alone (MUX-195 review must-fix): decided from what one
// refresh pass returned, a notice was lost whenever another process ran the
// pass — the graph executor's harvest discards it — or the send failed, and
// a queued task's completion was never noticed at all. A due notice stays due
// until AckSpawnNotice.
func DueSpawnNotices(session string) []SpawnNotice {
	entries, _ := ReadSpawnEntries(session)
	var due []SpawnNotice
	for _, e := range entries {
		live := e.Status == "running" || e.Status == spawnStarting
		for _, key := range e.NoticesOwed {
			if !live || spawnHasResponded(session, SpawnEntry{SpawnRole: e.SpawnRole, SeedMsgID: key}) {
				due = append(due, SpawnNotice{Entry: e, Key: key})
			}
		}
	}
	return due
}

// AckSpawnNotice records that the owner of spawn id was told of the
// completion keyed key. Once nothing more is owed the entry reads Notified,
// so its later reap owes no second notice for work already announced.
func AckSpawnNotice(session, id, key string) error {
	return UpdateSpawnEntry(session, id, func(e *SpawnEntry) {
		e.NoticesOwed = slices.DeleteFunc(e.NoticesOwed, func(k string) bool { return k == key })
		if len(e.NoticesOwed) == 0 {
			e.Notified = true
		}
	})
}

// reapNoticeKey keys the notice of a worker the reaper completed: its last
// seed, or its entry id when it never had one.
func reapNoticeKey(e SpawnEntry) string {
	if e.SeedMsgID != "" {
		return e.SeedMsgID
	}
	return e.ID
}

// spawnNoticeSendFn delivers a spawn-complete event and wakes its recipient;
// a seam so a test can fail the send.
var spawnNoticeSendFn = func(session string, m Message) error {
	if err := Send(session, m); err != nil {
		return err
	}
	return Notify(session, m.To)
}

// NotifySpawnCompletions sends each due notice (DueSpawnNotices) to its owner
// as a spawn-complete event and acknowledges it only once sent, so a failed
// send is retried on the next call rather than lost. A wake that fails after
// the event is queued still counts as sent: the inbox has it. Failures are
// warned on stderr, not the lifecycle log, which a send failing every pass
// would flood. It returns the notices sent.
func NotifySpawnCompletions(session string) []SpawnNotice {
	var sent []SpawnNotice
	for _, n := range DueSpawnNotices(session) {
		m := spawnNoticeMessage(session, n)
		if err := spawnNoticeSendFn(session, m); err != nil && !sentToInbox(session, m.ID) {
			fmt.Fprintf(os.Stderr, "  [spawn] notice to %s for %s not sent (%v) — retried next pass\n", n.Entry.Owner, n.Key, err)
			continue
		}
		if err := AckSpawnNotice(session, n.Entry.ID, n.Key); err != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] notice for %s sent but not recorded (%v)\n", n.Key, err)
		}
		LogLifecycle(session, "info", "daemon", "spawn-complete",
			fmt.Sprintf("%s role=%s window=%s seed=%s", n.Entry.ID, n.Entry.Role, n.Entry.Window, n.Key))
		sent = append(sent, n)
	}
	return sent
}

// sentToInbox reports whether message id reached the session log, which Send
// appends to once the recipient's inbox has it.
func sentToInbox(session, id string) bool {
	_, found := FindMessageByID(session, id)
	return found
}

// spawnNoticeMessage builds the spawn-complete event for one notice: the
// task it is about — the seed's own text, so an earlier queued task is named
// rather than the worker's latest — and the worker's reply to that seed.
func spawnNoticeMessage(session string, n SpawnNotice) Message {
	e := n.Entry
	task := e.Task
	if n.Key != e.SeedMsgID {
		if m, ok := FindMessageByID(session, n.Key); ok && m.Payload != "" {
			task = m.Payload
		}
	}
	result := spawnReplyPayload(session, n.Key)
	if result == "" && n.Key == e.ID {
		if m, ok := GetSpawnResult(session, e.SpawnRole); ok {
			result = m.Payload
		}
	}
	if result == "" {
		result = "No result message found."
	}
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	payload := fmt.Sprintf("Spawned agent completed: %s\n  Role: %s  Spawn Role: %s\n  Task: %s\n  Result: %s",
		e.ID, e.Role, e.SpawnRole, task, result)
	return NewMessage("spawn", e.Owner, "event", "spawn-complete", payload, "")
}
