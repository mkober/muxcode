package bus

import (
	"strings"
	"testing"
)

func TestScrubPIIWithNotice_PrependsBannerOnRedaction(t *testing.T) {
	input := "user,email\nNCCU_suejones,suejones@nccu.edu"
	out, n := ScrubPIIWithNotice(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if !strings.HasPrefix(out, "[muxcode pii-scrub:") {
		t.Errorf("expected scrub notice banner prefix, got: %q", out[:min(60, len(out))])
	}
	if !strings.Contains(out, "PLACEHOLDERS") {
		t.Error("notice should warn that redacted tokens are placeholders")
	}
	// The real (non-PII) user_id must survive so length checks remain valid.
	if !strings.Contains(out, "NCCU_suejones") {
		t.Error("non-PII user_id should not be redacted")
	}
}

func TestScrubPIIWithNotice_NoBannerWhenClean(t *testing.T) {
	input := "no pii here, just counts: 1234"
	out, n := ScrubPIIWithNotice(input)
	if n != 0 {
		t.Fatalf("expected 0 redactions, got %d", n)
	}
	if strings.Contains(out, "pii-scrub:") {
		t.Error("clean input must not get a notice banner")
	}
	if out != input {
		t.Errorf("clean input should pass through unchanged, got %q", out)
	}
}

func TestScrubPII_Email(t *testing.T) {
	input := `{"name": "John", "email": "john.doe@example.com", "role": "admin"}`
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "john.doe@example.com") {
		t.Error("email not redacted")
	}
	if !strings.Contains(out, "[EMAIL_REDACTED]") {
		t.Error("missing redaction placeholder")
	}
}

func TestScrubPII_SSN(t *testing.T) {
	input := "SSN: 123-45-6789 for patient record"
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "123-45-6789") {
		t.Error("SSN not redacted")
	}
	if !strings.Contains(out, "[SSN_REDACTED]") {
		t.Error("missing SSN placeholder")
	}
}

func TestScrubPII_CreditCard(t *testing.T) {
	input := "Card: 4111-1111-1111-1111 exp 12/25"
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "4111-1111-1111-1111") {
		t.Error("credit card not redacted")
	}
}

func TestScrubPII_Phone(t *testing.T) {
	input := "Call me at (555) 123-4567 or +1-555-987-6543"
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "123-4567") {
		t.Error("phone not redacted")
	}
}

// ASIA is the STS temporary-credential prefix, issued by every assumed role.
func TestScrubPII_AWSKey(t *testing.T) {
	for _, key := range []string{"AKIAIOSFODNN7EXAMPLE", "ASIAIOSFODNN7EXAMPLE"} {
		out, n := ScrubPII("aws_access_key_id = " + key)
		if n != 1 || strings.Contains(out, key) {
			t.Errorf("AWS key %s not redacted once (%d): %q", key, n, out)
		}
	}
}

func TestScrubPII_JWT(t *testing.T) {
	input := "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi789"
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "eyJhbGci") {
		t.Error("JWT not redacted")
	}
}

func TestScrubPII_GenericSecret(t *testing.T) {
	input := `api_key = "sk-1234567890abcdef" and password: hunter2secret`
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "sk-1234567890abcdef") {
		t.Error("API key not redacted")
	}
}

func TestScrubPII_DOB(t *testing.T) {
	input := "dob: 1990-05-15 patient record"
	out, n := ScrubPII(input)
	if n == 0 {
		t.Fatal("expected redactions")
	}
	if strings.Contains(out, "1990-05-15") {
		t.Error("DOB not redacted")
	}
}

func TestScrubPII_NoMatch(t *testing.T) {
	input := "Build succeeded: compiled 42 packages in 3.2s"
	out, n := ScrubPII(input)
	if n != 0 {
		t.Errorf("expected 0 redactions, got %d", n)
	}
	if out != input {
		t.Errorf("output changed: %q", out)
	}
}

func TestScrubPII_CCFalsePositive(t *testing.T) {
	// Bare 16-digit numbers should NOT match (order IDs, timestamps, etc.)
	input := "Order ID: 1234567890123456 processed"
	out, n := ScrubPII(input)
	if n != 0 {
		t.Errorf("bare 16-digit number should not be redacted, got %d redactions", n)
	}
	if out != input {
		t.Errorf("output changed: %q", out)
	}
}

func TestScrubPII_PhoneFalsePositive(t *testing.T) {
	// Bare 10-digit numbers should NOT match
	input := "Record ID: 5551234567 in database"
	out, n := ScrubPII(input)
	if n != 0 {
		t.Errorf("bare 10-digit number should not be redacted, got %d redactions", n)
	}
	if out != input {
		t.Errorf("output changed: %q", out)
	}
}

func TestScrubPII_Multiple(t *testing.T) {
	input := `{
		"user": "john@test.com",
		"ssn": "123-45-6789",
		"phone": "(555) 123-4567",
		"token": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abc123def456ghi789"
	}`
	out, n := ScrubPII(input)
	if n < 4 {
		t.Errorf("expected at least 4 redactions, got %d", n)
	}
	if strings.Contains(out, "john@test.com") {
		t.Error("email not redacted")
	}
	if strings.Contains(out, "123-45-6789") {
		t.Error("SSN not redacted")
	}
}

// The four credential shapes MUX-179's coverage docs once listed as misses:
// an Authorization header's scheme hid its token from the label=value rule, a
// JSON key's closing quote broke the label from its colon, and a bare key=
// had no label at all. Each must go, counted once; the controls are the
// lookalikes that must stay, so a pattern widened to match everything fails.
func TestScrubSecrets_HeaderQuotedLabelAndBareKey(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi789"
	redacted := []struct {
		name, input, secret string
	}{
		{"bearer header", "Authorization: Bearer opaque0123456789abcdef", "opaque0123456789abcdef"},
		{"basic header", `-H 'authorization: Basic dXNlcjpwYXNzd29yZDEyMw=='`, "dXNlcjpwYXNzd29yZDEyMw=="},
		{"short basic header", "Authorization: Basic dTpw", "dTpw"},
		{"json bearer header", `{"Authorization": "Bearer opaque0123456789abcdef"}`, "opaque0123456789abcdef"},
		{"jwt in bearer header", "Authorization: Bearer " + jwt, "eyJhbGci"},
		{"digest header", `Authorization: Digest username="alice", realm="example", nonce="abcdef0123456789", response="0123456789abcdef0123456789abcdef"`, "0123456789abcdef0123456789abcdef"},
		{"jwt behind token label", "token=" + jwt, "eyJhbGci"},
		{"bracketed password", "password=[hunter2secret0]", "hunter2secret0"},
		{"json password", `{"user": "svc", "password": "hunter2secret"}`, "hunter2secret"},
		{"json token", `{"token": "tok_0123456789abcdef"}`, "tok_0123456789abcdef"},
		{"bare key flag", "openssl enc --key=sk0123456789abcdefXYZ", "sk0123456789abcdefXYZ"},
		{"json bare key", `{"key": "a1b2c3d4e5f6g7h8i9j0"}`, "a1b2c3d4e5f6g7h8i9j0"},
	}
	for _, tc := range redacted {
		out, n := ScrubSecrets(tc.input)
		if n != 1 {
			t.Errorf("%s: redactions = %d, want 1: %q", tc.name, n, out)
		}
		if strings.Contains(out, tc.secret) {
			t.Errorf("%s: credential survived: %q", tc.name, out)
		}
	}

	kept := []string{
		"key=timestamp",
		"sort key=user_profile_settings",
		"key=/etc/ssl/private/server.pem",
		"partition_key=user_0123456789abcdef",
		"monkey=a1b2c3d4e5f6g7h8i9j0",
		"256 key: SHA256:AbCdEf0123456789AbCdEf0123456789 (ED25519)",
		`{"token_type": "Bearer", "expires_in": 3600}`,
		"Bearer tokens are documented in RFC 6750",
		`{"Authorization": "Bearer "}`,
	}
	for _, input := range kept {
		if out, n := ScrubSecrets(input); n != 0 || out != input {
			t.Errorf("lookalike redacted (%d): %q -> %q", n, input, out)
		}
	}
}

// On a PII-sensitive role the email rule runs before the label=value rule and
// leaves a placeholder at the head of the value; the rest of the secret must
// still go. Exempting every '['-prefixed value leaked "!secret987".
func TestScrubPII_PlaceholderPrefixedSecretRedacted(t *testing.T) {
	out, n := ScrubPII("password=alice@example.com!secret987")
	if n != 2 || strings.Contains(out, "secret987") {
		t.Errorf("secret suffix survived (%d): %q", n, out)
	}
}

// An ANSI color code between a label and its '=' hid the pair from every
// label=value rule on the writers that scrub raw text (reply rows, muxcode
// log, muxcode agent). ScrubForRole strips escapes first, for every role.
func TestScrubForRole_ColoredLabelRedacted(t *testing.T) {
	input := "\x1b[33mpassword\x1b[0m=hunter2secret0"
	for _, role := range []string{"plan", "run"} {
		out, n := ScrubForRole(role, input)
		if n != 1 || strings.Contains(out, "hunter2secret0") {
			t.Errorf("%s: colored label not redacted (%d): %q", role, n, out)
		}
	}
	if out, n := ScrubForRole("plan", "\x1b[32mok\x1b[0m build passed"); n != 0 || out != "ok build passed" {
		t.Errorf("clean colored output: got (%d) %q, want escapes stripped and nothing redacted", n, out)
	}
}

// A spawn worker's bus role is spawn-<id>, so a worker spawned as run lost PII
// scrubbing unless the rule choice resolves its base role. The edit spawn is
// the negative control: its email must survive.
func TestScrubForRole_SpawnWorkerUsesBaseRole(t *testing.T) {
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	entries := []SpawnEntry{
		{ID: "1-spawn-a1b2c3d4", Role: "run", SpawnRole: "spawn-a1b2c3d4", Window: "spawn-a1b2c3d4", Status: "running"},
		{ID: "2-spawn-e5f6a7b8", Role: "edit", SpawnRole: "spawn-e5f6a7b8", Window: "spawn-e5f6a7b8", Status: "running"},
	}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	const email = "jane.doe@example.com"
	input := "Author: Jane Doe <" + email + ">"
	if out, n := ScrubForRole("spawn-a1b2c3d4", input); n != 1 || strings.Contains(out, email) {
		t.Errorf("run spawn: PII not scrubbed (%d): %q", n, out)
	}
	if out, n := ScrubForRole("spawn-e5f6a7b8", input); n != 0 || out != input {
		t.Errorf("edit spawn: PII scrubbed (%d): %q", n, out)
	}
}

// ScrubSecrets is the every-role scrub (MUX-179): credentials go, and
// PII-shaped text stays — the half that separates it from ScrubPII, without
// which every role's build, test and git output would be rewritten.
func TestScrubSecrets_CredentialsOnly(t *testing.T) {
	input := "MUXCODE_OPENCODE_API_KEY=sk-fake-0123456789abcdef AKIAIOSFODNN7EXAMPLE\n" +
		"Author: Jane Doe <jane.doe@example.com> call (555) 123-4567 id 123-45-6789"
	out, n := ScrubSecrets(input)
	if n != 2 {
		t.Errorf("redactions = %d, want 2: %q", n, out)
	}
	for _, secret := range []string{"sk-fake-0123456789abcdef", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(out, secret) {
			t.Errorf("credential %q survived: %q", secret, out)
		}
	}
	for _, pii := range []string{"jane.doe@example.com", "(555) 123-4567", "123-45-6789"} {
		if !strings.Contains(out, pii) {
			t.Errorf("PII %q was scrubbed; ScrubSecrets must leave it: %q", pii, out)
		}
	}
}
