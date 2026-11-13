package bus

import "strconv"

// ReviewReplyOutcome derives the verdict of the review reply that tripped the
// reviewed transition from evidence the reply itself carries (MUX-172): its
// `EXIT=<n>` sentinel. It returns OutcomeSuccess or OutcomeFailure with the
// evidence read, or "" with the reason no verdict could be established — the
// caller withholds then, because a verification pass on an unknown verdict is
// the defect.
//
// The review history row is deliberately not read. A row carries no request
// or reply id, and review replies before it logs, so the newest row can be the
// previous review's: review A replying at T and logging success at T+1 would
// hand that success to a sentinel-less review B at T+2, and a late failure
// row from A would contradict B's own EXIT=0 forever. No time window proves
// which review a row belongs to, so only the reply's own text is evidence.
//
// A findings line reporting must-fix items beside an EXIT=0 contradicts
// itself and establishes nothing: a review's EXIT token has meant "the review
// ran" before (reviewFindingsOutcome, 2026-09-23), so it alone must not pass a
// review that lists must-fixes.
func ReviewReplyOutcome(reply Message) (outcome, evidence string) {
	claimed, ok := parseExitSentinel(reply.Payload)
	if !ok {
		return "", "unknown verdict: the reply carries no EXIT sentinel"
	}
	if claimed == OutcomeSuccess && reviewReportsMustFix(reply.Payload) {
		return "", "conflicting verdict: EXIT=0 beside a must-fix count"
	}
	return claimed, "EXIT sentinel"
}

// reviewReportsMustFix reports whether the reply's findings line — its first
// nonblank line, as reviewFindingsOutcome reads it — counts any must-fix.
func reviewReportsMustFix(payload string) bool {
	m := reviewCountsRe.FindStringSubmatch(firstNonblankLine(payload))
	if m == nil {
		return false
	}
	must, _ := strconv.Atoi(m[1])
	return must > 0
}
