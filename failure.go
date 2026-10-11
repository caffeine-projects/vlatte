package vlatte

// FailureHeader names the header (HTTP) or trailer (gRPC) a server adds to an answer it makes itself, instead of a
// mock. Its value says why: one of the Failure* constants. gRPC sends it lowercased, as metadata keys are.
const FailureHeader = "X-Vlatte-Error"

// Values of FailureHeader.
const (
	FailureNoMatch = "no-match" // no mock matched the request
	FailureMatch   = "match"    // finding a mock failed: a matcher or the store returned an error
	FailureReply   = "reply"    // a mock matched, and its reply failed
	FailurePanic   = "panic"    // serving the request panicked
)
