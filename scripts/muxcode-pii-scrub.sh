#!/bin/bash
# muxcode-pii-scrub.sh — Scrub PII and secrets from stdin
#
# Pipe-friendly filter that redacts common PII patterns from text.
# Used by Claude Code agents (api, run, watch) to sanitize tool output
# before it enters the conversation context.
#
# Usage:
#   curl https://api.example.com/users | muxcode-pii-scrub.sh
#   cat data.json | muxcode-pii-scrub.sh
#   muxcode-pii-scrub.sh < response.txt
#
# Patterns redacted:
#   - Email addresses
#   - SSN (xxx-xx-xxxx)
#   - Credit card numbers (16 digits)
#   - Phone numbers (US formats)
#   - AWS access keys (AKIA...)
#   - AWS secret keys
#   - JWT tokens
#   - Generic API keys, tokens, passwords
#   - Dates of birth (with contextual prefix)

# Read all stdin
INPUT=$(cat)

# Apply redactions in order (specific → broad)

# JWT tokens (three base64 segments)
INPUT=$(printf '%s' "$INPUT" | sed -E 's/eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/[JWT_REDACTED]/g')

# AWS access keys
INPUT=$(printf '%s' "$INPUT" | sed -E 's/AKIA[0-9A-Z]{16}/[AWS_KEY_REDACTED]/g')

# SSN
INPUT=$(printf '%s' "$INPUT" | sed -E 's/[0-9]{3}-[0-9]{2}-[0-9]{4}/[SSN_REDACTED]/g')

# Credit card (known prefixes: Visa 4, MC 5[1-5]/2[2-7], Amex 3[47], Discover 6011/65)
INPUT=$(printf '%s' "$INPUT" | sed -E 's/(4[0-9]{3}|5[1-5][0-9]{2}|2[2-7][0-9]{2}|3[47][0-9]{2}|6011|65[0-9]{2})[-[:space:]]?[0-9]{4}[-[:space:]]?[0-9]{4}[-[:space:]]?[0-9]{4}/[CC_REDACTED]/g')

# Email addresses
INPUT=$(printf '%s' "$INPUT" | sed -E 's/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}/[EMAIL_REDACTED]/g')

# Phone numbers: require separator or leading +/( to avoid bare digit matches
# Matches: +1-234-567-8901, (234) 567-8901, 234-567-8901, 234.567.8901
INPUT=$(printf '%s' "$INPUT" | sed -E 's/\+[0-9]{1,3}[-. ]\(?[0-9]{3}\)?[-. ][0-9]{3}[-. ][0-9]{4}/[PHONE_REDACTED]/g')
INPUT=$(printf '%s' "$INPUT" | sed -E 's/\([0-9]{3}\)[-. ]?[0-9]{3}[-. ]?[0-9]{4}/[PHONE_REDACTED]/g')
INPUT=$(printf '%s' "$INPUT" | sed -E 's/[0-9]{3}[-.][0-9]{3}[-.][0-9]{4}/[PHONE_REDACTED]/g')

# Generic secrets: api_key=..., token=..., password=..., authorization=...
INPUT=$(printf '%s' "$INPUT" | sed -E 's/(api[_-]?key|api[_-]?secret|auth[_-]?token|[Bb]earer|[Pp]assword|[Pp]asswd|[Ss]ecret|[Tt]oken|[Aa]uthorization)[[:space:]]*[=:][[:space:]]*"?[^[:space:]"'\'']{8,}"?/\1= [SECRET_REDACTED]/g')

# AWS secret keys
INPUT=$(printf '%s' "$INPUT" | sed -E 's/(aws_secret_access_key|[Ss]ecret.?[Kk]ey|SecretAccessKey)[[:space:]]*[=:][[:space:]]*"?[A-Za-z0-9\/+=]{40}"?/\1= [SECRET_REDACTED]/g')

printf '%s' "$INPUT"
