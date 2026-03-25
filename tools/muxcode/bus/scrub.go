package bus

import (
	"regexp"
	"strings"
)

// PII patterns compiled once at package init.
// NOTE: This file is intentionally duplicated in harness/scrub.go (separate Go module).
// Changes here must be mirrored in tools/muxcode-llm-harness/harness/scrub.go.
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

	// Generic API key/token patterns (key=..., token=..., password=...)
	piiGenericSecretRe = regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?secret|auth[_-]?token|bearer|password|passwd|secret|token|authorization)\s*[=:]\s*["']?([^\s"',;]{8,})["']?`)

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

// ScrubPII redacts common PII and secrets from text.
// Returns the scrubbed text and the count of redactions made.
func ScrubPII(text string) (string, int) {
	count := 0

	// Order matters: more specific patterns first to avoid partial matches

	// JWT tokens (before generic secret, since JWTs contain dots that confuse other patterns)
	if piiJWTRe.MatchString(text) {
		n := len(piiJWTRe.FindAllString(text, -1))
		text = piiJWTRe.ReplaceAllString(text, piiRedactJWT)
		count += n
	}

	// AWS access keys
	if piiAWSKeyRe.MatchString(text) {
		n := len(piiAWSKeyRe.FindAllString(text, -1))
		text = piiAWSKeyRe.ReplaceAllString(text, piiRedactAWSKey)
		count += n
	}

	// AWS secret keys (capture group replacement)
	if piiAWSSecretRe.MatchString(text) {
		n := len(piiAWSSecretRe.FindAllString(text, -1))
		text = piiAWSSecretRe.ReplaceAllStringFunc(text, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx >= 0 {
				return m[:idx+1] + " " + piiRedactSecret
			}
			return piiRedactSecret
		})
		count += n
	}

	// SSN
	if piiSSNRe.MatchString(text) {
		n := len(piiSSNRe.FindAllString(text, -1))
		text = piiSSNRe.ReplaceAllString(text, piiRedactSSN)
		count += n
	}

	// Credit card numbers
	if piiCCRe.MatchString(text) {
		n := len(piiCCRe.FindAllString(text, -1))
		text = piiCCRe.ReplaceAllString(text, piiRedactCC)
		count += n
	}

	// Email addresses
	if piiEmailRe.MatchString(text) {
		n := len(piiEmailRe.FindAllString(text, -1))
		text = piiEmailRe.ReplaceAllString(text, piiRedactEmail)
		count += n
	}

	// Phone numbers
	if piiPhoneRe.MatchString(text) {
		n := len(piiPhoneRe.FindAllString(text, -1))
		text = piiPhoneRe.ReplaceAllString(text, piiRedactPhone)
		count += n
	}

	// Date of birth
	if piiDOBRe.MatchString(text) {
		n := len(piiDOBRe.FindAllString(text, -1))
		text = piiDOBRe.ReplaceAllString(text, piiRedactDOB)
		count += n
	}

	// Generic secrets/tokens (last — broadest pattern)
	if piiGenericSecretRe.MatchString(text) {
		n := len(piiGenericSecretRe.FindAllString(text, -1))
		text = piiGenericSecretRe.ReplaceAllStringFunc(text, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx >= 0 {
				return m[:idx+1] + " " + piiRedactSecret
			}
			return piiRedactSecret
		})
		count += n
	}

	return text, count
}

// piiSensitiveRoles lists roles whose tool output should be scrubbed.
var piiSensitiveRoles = map[string]bool{
	"api":    true,
	"runner": true,
	"run":    true,
	"watch":  true,
}

// IsPIISensitiveRole returns true if the role handles external data
// that may contain PII (API responses, logs, command output).
func IsPIISensitiveRole(role string) bool {
	return piiSensitiveRoles[role]
}
