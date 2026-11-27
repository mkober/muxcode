package bus

import (
	"fmt"
	"strings"
)

// CondOutputToken is the graph sentinel condition: it passes when the output
// carries the token as a whole word in at least one occurrence that is not
// negated by the word before it.
//
// output_contains is a substring test, so a reply reporting failure in the
// token's own vocabulary passed it: on 2026-11-27 watch answered a red PR
// "CI RED (not CI-GREEN)", the 55 loop's ci-green condition read success and
// dispatched merge — only the commit agent declining stopped a red merge.
// Templates name sentinels with this condition; output_contains keeps its
// substring meaning for chain configs.
const CondOutputToken = "output_token"

// tokenNegations are the words that, immediately before an occurrence,
// turn it into a report of the token's absence.
var tokenNegations = map[string]bool{
	"not": true, "no": true, "never": true, "without": true, "nor": true,
	"isn't": true, "isnt": true, "wasn't": true, "wasnt": true,
	"aren't": true, "arent": true, "weren't": true, "werent": true,
}

func evalOutputToken(value any, ctx *ChainContext) ConditionResult {
	token, ok := value.(string)
	if !ok || token == "" {
		return ConditionResult{Type: CondOutputToken, Passed: false, Detail: "value must be a non-empty string"}
	}
	result := ConditionResult{Type: CondOutputToken, Pattern: token}
	found, negated := scanOutputToken(ctx.Output, token)
	switch {
	case found:
		result.Passed = true
		result.Detail = fmt.Sprintf("output carries token %q", token)
	case negated > 0:
		result.Detail = fmt.Sprintf("output names token %q only negated (%d occurrence(s))", token, negated)
	default:
		result.Detail = fmt.Sprintf("output does not carry token %q", token)
	}
	return result
}

// scanOutputToken reports whether output holds token as a whole word in an
// un-negated occurrence, and how many whole-word occurrences were negated.
// Word characters include '-' and '_', so NOT-CI-GREEN is no occurrence of
// CI-GREEN.
func scanOutputToken(output, token string) (found bool, negated int) {
	for from := 0; ; {
		i := strings.Index(output[from:], token)
		if i < 0 {
			return false, negated
		}
		start := from + i
		end := start + len(token)
		from = start + 1
		if (start > 0 && isTokenEdgeChar(output[start-1])) || (end < len(output) && isTokenEdgeChar(output[end])) {
			continue
		}
		if tokenNegations[precedingWord(output[:start])] {
			negated++
			continue
		}
		return true, negated
	}
}

// isTokenEdgeChar reports a character that joins a token to its neighbour;
// a quote does not, so 'CI-GREEN' is an occurrence.
func isTokenEdgeChar(c byte) bool {
	return c == '-' || c == '_' ||
		(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// isTokenChar adds the apostrophe, so a negation word such as isn't reads whole.
func isTokenChar(c byte) bool {
	return c == '\'' || isTokenEdgeChar(c)
}

// precedingWord returns the lower-cased word before the end of s, skipping
// whitespace and opening brackets or quotes only — never sentence
// punctuation, so "No. CI-GREEN" is not a negation.
func precedingWord(s string) string {
	end := len(s)
	for end > 0 && strings.IndexByte(" \t\r\n([{\"'`*", s[end-1]) >= 0 {
		end--
	}
	start := end
	for start > 0 && isTokenChar(s[start-1]) {
		start--
	}
	return strings.ToLower(s[start:end])
}
