package harness

import (
	"regexp"
	"strings"
)

// PII patterns compiled once at package init.
var (
	// Email: user@domain.tld
	emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// SSN: 123-45-6789 or 123 45 6789
	ssnRe = regexp.MustCompile(`\b\d{3}[-\s]\d{2}[-\s]\d{4}\b`)

	// Credit card: known prefixes (Visa 4, MC 5[1-5]/2[2-7], Amex 3[47], Discover 6)
	// with required separators to avoid matching bare 16-digit numbers
	ccRe = regexp.MustCompile(`\b(?:4\d{3}|5[1-5]\d{2}|2[2-7]\d{2}|3[47]\d{2}|6(?:011|5\d{2}))[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`)

	// Phone: requires at least one separator or leading +/( to avoid matching bare digit runs
	// +1-234-567-8901, (234) 567-8901, 234-567-8901, 234.567.8901
	phoneRe = regexp.MustCompile(`(?:\+\d{1,3}[-.\s])\(?\d{3}\)?[-.\s]\d{3}[-.\s]\d{4}\b|\(\d{3}\)[-.\s]?\d{3}[-.\s]?\d{4}\b`)

	// AWS access key: AKIA followed by 16 alphanumeric chars
	awsKeyRe = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)

	// AWS secret key: 40-char base64-like string after common key labels
	awsSecretRe = regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret.?key|SecretAccessKey)\s*[=:]\s*["']?([A-Za-z0-9/+=]{40})["']?`)

	// Generic API key/token patterns (key=..., token=..., password=...)
	genericSecretRe = regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?secret|auth[_-]?token|bearer|password|passwd|secret|token|authorization)\s*[=:]\s*["']?([^\s"',;]{8,})["']?`)

	// JWT tokens: three base64 segments separated by dots
	jwtRe = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)

	// Date of birth patterns: MM/DD/YYYY, YYYY-MM-DD with contextual prefix
	dobRe = regexp.MustCompile(`(?i)(?:dob|date.?of.?birth|birth.?date)\s*[=:]\s*["']?\d{1,4}[-/]\d{1,2}[-/]\d{1,4}["']?`)

	// IP addresses (v4) — only scrub when in structured data contexts
	// Not scrubbed by default since IPs appear in non-PII contexts (localhost, DNS, etc.)
)

// Redaction placeholders
const (
	redactEmail     = "[EMAIL_REDACTED]"
	redactSSN       = "[SSN_REDACTED]"
	redactCC        = "[CC_REDACTED]"
	redactPhone     = "[PHONE_REDACTED]"
	redactAWSKey    = "[AWS_KEY_REDACTED]"
	redactSecret    = "[SECRET_REDACTED]"
	redactJWT       = "[JWT_REDACTED]"
	redactDOB       = "[DOB_REDACTED]"
)

// ScrubPII redacts common PII and secrets from text.
// Returns the scrubbed text and the count of redactions made.
func ScrubPII(text string) (string, int) {
	count := 0

	// Order matters: more specific patterns first to avoid partial matches

	// JWT tokens (before generic secret, since JWTs contain dots that confuse other patterns)
	if jwtRe.MatchString(text) {
		n := len(jwtRe.FindAllString(text, -1))
		text = jwtRe.ReplaceAllString(text, redactJWT)
		count += n
	}

	// AWS access keys
	if awsKeyRe.MatchString(text) {
		n := len(awsKeyRe.FindAllString(text, -1))
		text = awsKeyRe.ReplaceAllString(text, redactAWSKey)
		count += n
	}

	// AWS secret keys (capture group replacement)
	if awsSecretRe.MatchString(text) {
		n := len(awsSecretRe.FindAllString(text, -1))
		text = awsSecretRe.ReplaceAllStringFunc(text, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx >= 0 {
				return m[:idx+1] + " " + redactSecret
			}
			return redactSecret
		})
		count += n
	}

	// SSN
	if ssnRe.MatchString(text) {
		n := len(ssnRe.FindAllString(text, -1))
		text = ssnRe.ReplaceAllString(text, redactSSN)
		count += n
	}

	// Credit card numbers
	if ccRe.MatchString(text) {
		n := len(ccRe.FindAllString(text, -1))
		text = ccRe.ReplaceAllString(text, redactCC)
		count += n
	}

	// Email addresses
	if emailRe.MatchString(text) {
		n := len(emailRe.FindAllString(text, -1))
		text = emailRe.ReplaceAllString(text, redactEmail)
		count += n
	}

	// Phone numbers
	if phoneRe.MatchString(text) {
		n := len(phoneRe.FindAllString(text, -1))
		text = phoneRe.ReplaceAllString(text, redactPhone)
		count += n
	}

	// Date of birth
	if dobRe.MatchString(text) {
		n := len(dobRe.FindAllString(text, -1))
		text = dobRe.ReplaceAllString(text, redactDOB)
		count += n
	}

	// Generic secrets/tokens (last — broadest pattern)
	if genericSecretRe.MatchString(text) {
		n := len(genericSecretRe.FindAllString(text, -1))
		text = genericSecretRe.ReplaceAllStringFunc(text, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx >= 0 {
				return m[:idx+1] + " " + redactSecret
			}
			return redactSecret
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
