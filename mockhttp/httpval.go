package mockhttp

import "github.com/caffeine-projects/vlatte"

const _charsetUTF8 = "charset=UTF-8"

// Header names, for building and matching mocks.
const (
	HeaderAccept                          = "Accept"
	HeaderAcceptEncoding                  = "Accept-Encoding"
	HeaderContentType                     = "Content-Type"
	HeaderContentEncoding                 = "Content-Encoding"
	HeaderAllow                           = "Allow"
	HeaderAuthorization                   = "Authorization"
	HeaderContentDisposition              = "Content-Disposition"
	HeaderVary                            = "Vary"
	HeaderOrigin                          = "Origin"
	HeaderContentLength                   = "Content-Length"
	HeaderConnection                      = "Connection"
	HeaderTrailer                         = "Trailer"
	HeaderLocation                        = "Location"
	HeaderCacheControl                    = "Cache-Control"
	HeaderCookie                          = "Cookie"
	HeaderSetCookie                       = "Set-Cookie"
	HeaderIfModifiedSince                 = "If-Modified-Since"
	HeaderLastModified                    = "Last-Modified"
	HeaderLastEventID                     = "Last-Event-ID"
	HeaderRetryAfter                      = "Retry-After"
	HeaderUpgrade                         = "Upgrade"
	HeaderWWWAuthenticate                 = "WWW-Authenticate"
	HeaderXForwardedFor                   = "X-Forwarded-For"
	HeaderXForwardedProto                 = "X-Forwarded-Proto"
	HeaderXForwardedProtocol              = "X-Forwarded-Protocol"
	HeaderXForwardedSsl                   = "X-Forwarded-Ssl"
	HeaderXUrlScheme                      = "X-Url-Scheme"
	HeaderXHTTPMethodOverride             = "X-HTTP-Method-Override"
	HeaderXRealIP                         = "X-Real-Ip"
	HeaderXRequestID                      = "X-Request-Id"
	HeaderXCorrelationID                  = "X-Correlation-Id"
	HeaderXRequestedWith                  = "X-Requested-With"
	HeaderServer                          = "Server"
	HeaderAccessControlRequestMethod      = "Access-Control-Request-Method"
	HeaderAccessControlAllowOrigin        = "Access-Control-Allow-Origin"
	HeaderAccessControlAllowMethods       = "Access-Control-Allow-Methods"
	HeaderAccessControlAllowHeaders       = "Access-Control-Allow-Headers"
	HeaderAccessControlExposeHeaders      = "Access-Control-Expose-Headers"
	HeaderAccessControlMaxAge             = "Access-Control-Max-Age"
	HeaderAccessControlAllowCredentials   = "Access-Control-Allow-Credentials"
	HeaderAccessControlRequestHeaders     = "Access-Control-Request-Headers"
	HeaderStrictTransportSecurity         = "Strict-Transport-Security"
	HeaderXContentTypeOptions             = "X-Content-Type-Options"
	HeaderXXSSProtection                  = "X-XSS-Protection"
	HeaderXFrameOptions                   = "X-Frame-Options"
	HeaderContentSecurityPolicy           = "Content-Security-Policy"
	HeaderContentSecurityPolicyReportOnly = "Content-Security-Policy-Report-Only"
	HeaderXCSRFToken                      = "X-CSRF-Token"
	HeaderReferrerPolicy                  = "Referrer-Policy"

	// HeaderVlatteError says why vlatte, and not a mock, answered a request: one of the vlatte.Failure* values.
	HeaderVlatteError = vlatte.HeaderFailure
)

// _debugHeaders are the headers a server adds to its answers unless Config.NoDebugHeaders is set.
var _debugHeaders = []string{
	vlatte.HeaderMatchedMockID,
	vlatte.HeaderMatchedMockName,
	vlatte.HeaderMatchedMockHits,
	vlatte.HeaderMatchedMockSource,
	vlatte.HeaderRequestID,
	vlatte.HeaderProxied,
}

// MIME types, for building and matching mocks.
const (
	MIMEApplicationJSON                  = "application/json"
	MIMEApplicationCharsetUTF8           = MIMEApplicationJSON + "; " + _charsetUTF8
	MIMEApplicationProblemJSON           = "application/problem+json"
	MIMETextPlain                        = "text/plain"
	MIMETextPlainCharsetUTF8             = MIMETextPlain + "; " + _charsetUTF8
	MIMETextHTML                         = "text/html"
	MIMETextHTMLCharsetUTF8              = MIMETextHTML + "; " + _charsetUTF8
	MIMETextXML                          = "text/xml"
	MIMETextXMLCharsetUTF8               = MIMETextXML + "; " + _charsetUTF8
	MIMEFormURLEncoded                   = "application/x-www-form-urlencoded"
	MIMEFormURLEncodedCharsetUTF8        = MIMEFormURLEncoded + "; " + _charsetUTF8
	MIMEApplicationJavaScript            = "application/javascript"
	MIMEApplicationJavaScriptCharsetUTF8 = MIMEApplicationJavaScript + "; " + _charsetUTF8
	MIMEApplicationXML                   = "application/xml"
	MIMEApplicationXMLCharsetUTF8        = MIMEApplicationXML + "; " + _charsetUTF8
	MIMEApplicationProtobuf              = "application/protobuf"
	MIMEMultipartForm                    = "multipart/form-data"
	MIMETextEventStream                  = "text/event-stream"
	MIMEOctetStream                      = "application/octet-stream"
)
