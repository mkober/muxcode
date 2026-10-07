package bus

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// PII patterns compiled once at package init.
// NOTE: The patterns, placeholders and ScrubPII's redaction order are
// duplicated in tools/muxcode-llm-harness/harness/scrub.go (separate Go
// module) and must stay in step with it. ScrubSecrets and ScrubForRole are
// bus-only: the harness scrubs by the role list alone (MUX-179).
var (
	// Email: user@domain.tld
	piiEmailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// SSN: 123-45-6789 or 123 45 6789
	piiSSNRe = regexp.MustCompile(`\b\d{3}[-\s]\d{2}[-\s]\d{4}\b`)

	// Credit card: known prefixes (Visa 4, MC 5[1-5]/2[2-7], Amex 3[47], Discover 6)
	// with required separators to avoid matching bare 16-digit numbers
	piiCCRe = regexp.MustCompile(`\b(?:4\d{3}|5[1-5]\d{2}|2[2-7]\d{2}|3[47]\d{2}|6(?:011|5\d{2}))[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`)

	// Phone: requires at least one separator or leading +/( to avoid matching bare digit runs
	piiPhoneRe = regexp.MustCompile(`(?:\+\d{1,3}[-.\s])\(?\d{3}\)?[-.\s]\d{3}[-.\s]\d{4}\b|\(\d{3}\)[-.\s]?\d{3}[-.\s]?\d{4}\b`)

	// AWS access key: AKIA followed by 16 alphanumeric chars
	piiAWSKeyRe = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// AWS secret key: 40-char base64-like string after common key labels
	piiAWSSecretRe = regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret.?key|SecretAccessKey)\s*[=:]\s*["']?([A-Za-z0-9/+=]{40})["']?`)

	// Authorization header: one Bearer/Basic/Token token, or Digest's field list to end of line
	piiAuthHeaderRe = regexp.MustCompile(`(?i)\bauthorization["']?\s*[=:]\s*["']?(?:(?:bearer|basic|token)\s+[^\s"',;]{8,}["']?|digest\s+[^\r\n]+)`)

	// Generic API key/token patterns (token=..., "password": "...")
	piiGenericSecretRe = regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?secret|auth[_-]?token|bearer|password|passwd|secret|token|authorization)["']?\s*[=:]\s*["']?([^\s"',;]{8,})["']?`)

	// Bare key label: --key=..., "key": "..." — kept only when secretShaped
	piiBareKeyRe = regexp.MustCompile(`(?i)\bkey["']?\s*[=:]\s*["']?[^\s"',;]{8,}["']?`)

	// A whole value that is an earlier rule's placeholder, closing JSON punctuation allowed
	piiPlaceholderRe = regexp.MustCompile(`^\[[A-Z_]+_REDACTED\][)\]}>]*$`)

	// JWT tokens: three base64 segments separated by dots
	piiJWTRe = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)

	// Date of birth patterns: MM/DD/YYYY, YYYY-MM-DD with contextual prefix
	piiDOBRe = regexp.MustCompile(`(?i)(?:dob|date.?of.?birth|birth.?date)\s*[=:]\s*["']?\d{1,4}[-/]\d{1,2}[-/]\d{1,4}["']?`)
)

// PII redaction placeholders
const (
	piiRedactEmail  = "[EMAIL_REDACTED]"
	piiRedactSSN    = "[SSN_REDACTED]"
	piiRedactCC     = "[CC_REDACTED]"
	piiRedactPhone  = "[PHONE_REDACTED]"
	piiRedactAWSKey = "[AWS_KEY_REDACTED]"
	piiRedactSecret = "[SECRET_REDACTED]"
	piiRedactJWT    = "[JWT_REDACTED]"
	piiRedactDOB    = "[DOB_REDACTED]"
)

// scrubRule is one redaction. A rule with no placeholder matches a
// label=value pair and keeps the label, redacting only what follows its = or :.
// A rule with accept redacts only the matches accept approves.
type scrubRule struct {
	re          *regexp.Regexp
	placeholder string
	credential  bool
	accept      func(match string) bool
}

// scrubRules run in order: specific patterns before broad ones — the
// Authorization header first, so a JWT inside it is one redaction rather than
// two, JWTs ahead of the generic secret, whose dots confuse it, and the
// label=value rules last. The generic rule skips a value that is exactly an
// earlier placeholder (token=[JWT_REDACTED]) so a redaction is counted once,
// but still takes one that merely contains a placeholder:
// password=[EMAIL_REDACTED]!secret987 would otherwise leak its suffix.
var scrubRules = []scrubRule{
	{piiAuthHeaderRe, "", true, nil},
	{piiJWTRe, piiRedactJWT, true, nil},
	{piiAWSKeyRe, piiRedactAWSKey, true, nil},
	{piiAWSSecretRe, "", true, nil},
	{piiSSNRe, piiRedactSSN, false, nil},
	{piiCCRe, piiRedactCC, false, nil},
	{piiEmailRe, piiRedactEmail, false, nil},
	{piiPhoneRe, piiRedactPhone, false, nil},
	{piiDOBRe, piiRedactDOB, false, nil},
	{piiGenericSecretRe, "", true, notPlaceholder},
	{piiBareKeyRe, "", true, secretShaped},
}

// labelValue returns a label=value match's value, unquoted.
func labelValue(match string) string {
	return strings.Trim(match[strings.IndexAny(match, "=:")+1:], ` "'`)
}

func notPlaceholder(match string) bool {
	return !piiPlaceholderRe.MatchString(labelValue(match))
}

// secretShaped reports whether a bare key= match carries a credential rather
// than an ordinary key: a value of 16+ characters mixing letters and digits,
// with no '.' or ':' and no leading '/' or '~', so key=timestamp,
// key=/etc/a.pem and an SSH "key: SHA256:…" fingerprint stay readable while
// key=sk0123456789abcdefXYZ goes.
func secretShaped(match string) bool {
	v := labelValue(match)
	if len(v) < 16 || strings.ContainsAny(v, ".:") || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") {
		return false
	}
	return strings.IndexFunc(v, unicode.IsLetter) >= 0 && strings.IndexFunc(v, unicode.IsDigit) >= 0
}

var credentialScrubRules = func() []scrubRule {
	var rules []scrubRule
	for _, r := range scrubRules {
		if r.credential {
			rules = append(rules, r)
		}
	}
	return rules
}()

func (r scrubRule) apply(text string) (string, int) {
	n := 0
	out := r.re.ReplaceAllStringFunc(text, func(m string) string {
		if r.accept != nil && !r.accept(m) {
			return m
		}
		n++
		if r.placeholder != "" {
			return r.placeholder
		}
		if idx := strings.IndexAny(m, "=:"); idx >= 0 {
			return m[:idx+1] + " " + piiRedactSecret
		}
		return piiRedactSecret
	})
	return out, n
}

func applyScrubRules(text string, rules []scrubRule) (string, int) {
	count := 0
	for _, r := range rules {
		var n int
		text, n = r.apply(text)
		count += n
	}
	return text, count
}

// ScrubPII redacts common PII and secrets from text.
// Returns the scrubbed text and the count of redactions made.
func ScrubPII(text string) (string, int) {
	return applyScrubRules(text, scrubRules)
}

// ScrubSecrets redacts credentials alone — JWTs, AWS keys, Authorization
// headers and labelled secrets such as api_key=, token= and "password": — and
// leaves PII-shaped text
// (emails, phone- and SSN-shaped numbers) untouched. Returns the scrubbed text
// and the count of redactions made.
func ScrubSecrets(text string) (string, int) {
	return applyScrubRules(text, credentialScrubRules)
}

// PIIScrubNotice returns an in-band banner to prepend to scrubbed output when
// redactions occurred. Agents reason over their own tool output and history,
// so without a visible notice an agent can mistake a redacted
// placeholder (e.g. [EMAIL_REDACTED]) for real data — and, worse, compute
// string lengths, byte sizes, or row counts over redacted text and report them
// as fact. The notice tells the agent the data was masked and that quantitative
// conclusions must come from a non-PII aggregation instead.
func PIIScrubNotice(count int) string {
	return fmt.Sprintf("[muxcode pii-scrub: %d value(s) redacted in the output below. "+
		"Redacted tokens (e.g. [EMAIL_REDACTED]) are PLACEHOLDERS, not real data. "+
		"Do NOT compute lengths, byte sizes, row counts, or draw conclusions from "+
		"redacted content — for exact values use an aggregation that emits only "+
		"non-PII numbers (e.g. LENGTH(col), COUNT(*)).]\n\n", count)
}

// ScrubPIIWithNotice scrubs PII and, if any redactions were made, prepends the
// PIIScrubNotice banner so the consuming agent sees the redaction in-band.
// Returns the (possibly annotated) text and the redaction count.
func ScrubPIIWithNotice(text string) (string, int) {
	out, n := ScrubPII(text)
	if n > 0 {
		out = PIIScrubNotice(n) + out
	}
	return out, n
}

// piiSensitiveRoles lists roles whose tool output should be scrubbed.
var piiSensitiveRoles = map[string]bool{
	"api":    true,
	"run":    true,
	"runner": true,
	"watch":  true,
}

// IsPIISensitiveRole returns true if the role handles external data
// that may contain PII (API responses, logs, command output).
func IsPIISensitiveRole(role string) bool {
	return piiSensitiveRoles[role]
}

// ScrubForRole is the bus road's coverage rule (MUX-179): every role's text is
// scrubbed of credentials (ScrubSecrets), and a PII-sensitive role's of PII as
// well (ScrubPII). The split follows the evidence: the leak that motivated it
// was a credential, from plan, outside the role list — and credentials match
// with high precision, so redacting them everywhere costs little. The PII
// patterns match ordinary output agents reason over — a commit's author email,
// an SSN-shaped id in a test log — so they stay gated on the roles that
// handle external data. ANSI escapes are stripped first on every writer: a
// color code between a label and its = defeats every label=value pattern.
func ScrubForRole(role, text string) (string, int) {
	text = StripANSI(text)
	if IsPIISensitiveRole(role) {
		return ScrubPII(text)
	}
	return ScrubSecrets(text)
}
