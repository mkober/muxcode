package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// ErrResponseSuppressed reports a response not delivered because it repeats the
// last response delivered on the same (from, to, action) within the window
// (MUX-175). It is a decision, not a failure: retrying sends the same repeat.
var ErrResponseSuppressed = errors.New("response repeats the last one delivered")

// responseSuppressWindowEnv overrides the brake's window in seconds; 0 turns
// the brake off.
const responseSuppressWindowEnv = "MUXCODE_RESPONSE_SUPPRESS_WINDOW"

const defaultResponseSuppressWindow = 300 * time.Second

func responseSuppressWindow() time.Duration {
	v := strings.TrimSpace(os.Getenv(responseSuppressWindowEnv))
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return defaultResponseSuppressWindow
}

// normalizeResponse folds case, whitespace and punctuation so `Acknowledged.`
// and `acknowledged` match. Punctuation becomes a space rather than vanishing,
// so a number or id keeps its separators' weight: `10/23` and `102/3` differ,
// as do `10/23` and `11/23`.
func normalizeResponse(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

// lastResponse is the comparison point for one (from, to, action): the
// normalized text of the last response delivered on it, and when.
type lastResponse struct {
	Tuple string `json:"tuple"` // from, to, action — for a reader; the filename is its hash
	Text  string `json:"text"`
	TSMs  int64  `json:"ts_ms"` // milliseconds: whole seconds would age a copy up to 1 s early
}

func responseTuple(m Message) string {
	return m.From + "\x00" + m.To + "\x00" + m.Action
}

// lastResponsePath names a tuple's state by a hash of the NUL-joined tuple:
// actions are free-form, so joining the fields with any printable separator
// let `a/b` and `a_b` share one comparison point (MUX-175 review).
func lastResponsePath(session string, m Message) string {
	sum := sha256.Sum256([]byte(responseTuple(m)))
	return filepath.Join(BusDir(session), "response-last", hex.EncodeToString(sum[:16])+".json")
}

// withResponseBrake runs deliver for m, braked: for a response it holds the
// session's response-brake lock across the check, the delivery and the
// comparison-point update, so two concurrent sends on one tuple cannot both
// pass the check, nor record in a different order from delivery. It is the
// only road to the brake, so callers cannot split that sequence. Lock order:
// SendIfNotDuplicate's dedup lock, then this one, then acceptReply's
// reply-claim lock inside deliver. A lock that cannot be taken fails open:
// the brake still checks, but unserialized, so a concurrent repeat may slip
// through rather than a send being lost.
func withResponseBrake(session string, m Message, deliver func() error) error {
	if m.Type != "response" || session == "" {
		return deliver()
	}
	if unlock, err := lockResponseBrake(session); err == nil {
		defer unlock()
	}
	if err := brakeRepeatedResponse(session, m); err != nil {
		return err
	}
	if err := deliver(); err != nil {
		return err
	}
	recordDeliveredResponse(session, m)
	return nil
}

func lockResponseBrake(session string) (func(), error) {
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(BusDir(session), "response-brake.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// brakeRepeatedResponse refuses a response whose normalized text equals the
// last response delivered on the same (from, to, action) within the window —
// the user's decisions of 2026-11-15: exact match after normalization, no
// count, suppressed on the first repeat. Two agents acknowledging each other
// every few seconds stopped only when a human told them to (2026-09-10), while
// a substantive exchange carries new text each round and is never braked.
// Requests are never braked, nor a response answering a request still awaiting
// its answer (answersOpenRequest). A suppressed attempt neither
// becomes the comparison point nor restarts the window — recordDeliveredResponse
// moves it only on delivery — so the brake clears even for a pair that keeps
// retrying.
func brakeRepeatedResponse(session string, m Message) error {
	if m.Type != "response" || session == "" {
		return nil
	}
	window := responseSuppressWindow()
	if window == 0 {
		return nil
	}
	data, err := os.ReadFile(lastResponsePath(session, m))
	if err != nil {
		return nil
	}
	var last lastResponse
	if json.Unmarshal(data, &last) != nil {
		return nil
	}
	age := time.Since(time.UnixMilli(last.TSMs))
	if age >= window || last.Text != normalizeResponse(m.Payload) || answersOpenRequest(session, m.ReplyTo) {
		return nil
	}
	LogLifecycle(session, "warn", "bus", "response-suppressed", fmt.Sprintf(
		"%s→%s:%s repeats the response delivered %ds ago — not delivered", m.From, m.To, m.Action, int(age.Seconds())))
	fmt.Fprintf(os.Stderr, "  [send] suppressing repeated response %s→%s:%s\n", m.From, m.To, m.Action)
	return fmt.Errorf("%s→%s:%s repeats the response delivered %ds ago: %w",
		m.From, m.To, m.Action, int(age.Seconds()), ErrResponseSuppressed)
}

// answersOpenRequest reports whether replyTo names a request still awaiting its
// answer: an in-flight tracked task, or a request with no response recorded —
// a graph spawn worker's seed is one, untracked by any task, and a terse worker
// answers each loop iteration's seed with the same text. An ack ping-pong never
// does: its replies name nothing, a response, or a request already answered.
func answersOpenRequest(session, replyTo string) bool {
	if replyTo == "" {
		return false
	}
	if t, err := ReadTask(session, replyTo); err == nil {
		return t.Status == TaskInFlight
	}
	req, ok := FindMessageByID(session, replyTo)
	return ok && req.Type == "request" && !IsResponded(session, replyTo)
}

// recordDeliveredResponse makes a delivered response the comparison point for
// its (from, to, action).
func recordDeliveredResponse(session string, m Message) {
	if m.Type != "response" || session == "" {
		return
	}
	path := lastResponsePath(session, m)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	_ = atomicWriteJSON(path, lastResponse{
		Tuple: strings.ReplaceAll(responseTuple(m), "\x00", " → "),
		Text:  normalizeResponse(m.Payload),
		TSMs:  time.Now().UnixMilli(),
	})
}
