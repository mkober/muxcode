package harness

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
		t.Error("expected scrub notice banner prefix")
	}
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

// Mirrors bus TestScrubSecrets_HeaderQuotedLabelAndBareKey (MUX-179): the two
// copies of the patterns must redact and keep the same inputs.
func TestScrubPII_HeaderQuotedLabelAndBareKey(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi789"
	digest := `Authorization: Digest username="alice", nonce="abcdef0123456789", response="0123456789abcdef0123456789abcdef"`
	redacted := map[string]string{
		"Authorization: Bearer opaque0123456789abcdef":       "opaque0123456789abcdef",
		"Authorization: Basic dTpw":                          "dTpw",
		`{"Authorization": "Bearer opaque0123456789abcdef"}`: "opaque0123456789abcdef",
		`{"user": "svc", "password": "hunter2secret"}`:       "hunter2secret",
		"openssl enc --key=sk0123456789abcdefXYZ":            "sk0123456789abcdefXYZ",
		"Authorization: Bearer " + jwt:                       "eyJhbGci",
		"token=" + jwt:                                       "eyJhbGci",
		"password=[hunter2secret0]":                          "hunter2secret0",
		digest:                                               "0123456789abcdef0123456789abcdef",
	}
	for input, secret := range redacted {
		out, n := ScrubPII(input)
		if n != 1 || strings.Contains(out, secret) {
			t.Errorf("not redacted once (%d): %q -> %q", n, input, out)
		}
	}
	if out, n := ScrubPII("password=alice@example.com!secret987"); n != 2 || strings.Contains(out, "secret987") {
		t.Errorf("secret suffix after a placeholder survived (%d): %q", n, out)
	}
	for _, input := range []string{
		"key=timestamp",
		"key=/etc/ssl/private/server.pem",
		"partition_key=user_0123456789abcdef",
		`{"token_type": "Bearer", "expires_in": 3600}`,
		`{"Authorization": "Bearer "}`,
	} {
		if out, n := ScrubPII(input); n != 0 || out != input {
			t.Errorf("lookalike redacted (%d): %q -> %q", n, input, out)
		}
	}
}

func TestIsPIISensitiveRole(t *testing.T) {
	sensitive := []string{"api", "runner", "run", "watch"}
	for _, r := range sensitive {
		if !IsPIISensitiveRole(r) {
			t.Errorf("expected %q to be PII-sensitive", r)
		}
	}

	notSensitive := []string{"build", "test", "edit", "review", "commit"}
	for _, r := range notSensitive {
		if IsPIISensitiveRole(r) {
			t.Errorf("expected %q to NOT be PII-sensitive", r)
		}
	}
}
