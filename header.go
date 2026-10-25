package vlatte

// Headers (HTTP) or metadata keys (gRPC) a server adds to its answers. gRPC sends them lowercased, as metadata keys are.
const (
	FailureHeader           = "VLatte-Error"
	MatchedMockIDHeader     = "VLatte-Matched-Mock-Id"     // ID of the mock that matched
	MatchedMockNameHeader   = "VLatte-Matched-Mock-Name"   // name of the mock that matched, when it has one
	MatchedMockHitsHeader   = "VLatte-Matched-Mock-Hits"   // hits of the mock that matched, this request included
	MatchedMockSourceHeader = "VLatte-Matched-Mock-Source" // file the mock that matched was loaded from (HTTP)
	RequestIDHeader         = "VLatte-Request-Id"          // ID of the request, as logs, templates and the journal see it (HTTP; gRPC with a journal)
	ProxiedHeader           = "VLatte-Proxied"             // "true" when a proxy answered instead of a mock (HTTP)
)
