package vlatte

// Values of FailureHeader.
const (
	FailureNoMatch = "no-match" // no mock matched the request
	FailureMatch   = "match"    // finding a mock failed: a matcher or the store returned an error
	FailureReply   = "reply"    // a mock matched, and its reply failed
	FailurePanic   = "panic"    // serving the request panicked
)
