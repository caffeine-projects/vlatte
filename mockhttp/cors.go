package mockhttp

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// CORSConfig represents the possible options to configure CORS.
type CORSConfig struct {
	// AllowedOrigin lists the allowed origins, comma-separated. * allows any. Browsers refuse * for requests with
	// credentials, so with AllowCredentials the origin of the request is allowed instead.
	AllowedOrigin    string
	AllowCredentials bool

	// AllowedMethods lists the allowed methods, comma-separated. * allows the method a preflight request asks for.
	AllowedMethods string

	// AllowedHeaders lists the allowed headers, comma-separated. Empty, or *, allows the headers a preflight request
	// asks for.
	AllowedHeaders string

	// ExposeHeaders lists the headers browsers let scripts read, comma-separated. Browsers read * as a header name
	// for requests with credentials.
	ExposeHeaders string

	// MaxAge is how many seconds browsers may cache the answer to a preflight request. -1 sends none.
	MaxAge int

	// SuccessStatusCode is the status of the answer to a preflight request, 204 when 0. Browsers fail a preflight
	// answered with a status other than 2xx.
	SuccessStatusCode int
}

// CORSConfigurer lets users configure CORS.
type CORSConfigurer interface {
	Apply(opts *CORSConfig)
}

// Apply allows CORSConfig to be used as a CORSConfigurer
func (c *CORSConfig) Apply(opts *CORSConfig) {
	opts.AllowedOrigin = c.AllowedOrigin
	opts.AllowCredentials = c.AllowCredentials
	opts.AllowedMethods = c.AllowedMethods
	opts.AllowedHeaders = c.AllowedHeaders
	opts.ExposeHeaders = c.ExposeHeaders
	opts.MaxAge = c.MaxAge
	opts.SuccessStatusCode = c.SuccessStatusCode
}

var DefaultCORSConfig = CORSConfig{
	AllowedOrigin: "*",
	AllowedMethods: strings.Join([]string{
		http.MethodGet,
		http.MethodPost,
		http.MethodHead,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
	}, ","),
	AllowedHeaders:    "",
	AllowCredentials:  false,
	ExposeHeaders:     "",
	MaxAge:            0,
	SuccessStatusCode: http.StatusNoContent,
}

// CORSConfigBuilder facilitates building CORS options. It sets only the options it is given: the others keep the
// values they have, such as the ones of DefaultCORSConfig.
type CORSConfigBuilder struct {
	origins []string
	set     []func(opts *CORSConfig)
}

// CORS initializes a CORSConfig builder for a fluent configuration.
func CORS() *CORSConfigBuilder {
	return &CORSConfigBuilder{}
}

// SuccessStatusCode sets a custom status code returned on CORS Options request.
// If none is specified, the default status code is http.StatusNoContent.
func (b *CORSConfigBuilder) SuccessStatusCode(code int) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.SuccessStatusCode = code })
	return b
}

// MaxAge sets CORS max age.
func (b *CORSConfigBuilder) MaxAge(maxAge int) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.MaxAge = maxAge })
	return b
}

// AllowOrigin sets the allowed origins.
func (b *CORSConfigBuilder) AllowOrigin(origin ...string) *CORSConfigBuilder {
	b.origins = append(b.origins, origin...)
	return b
}

// AllowCredentials sets "Access-Control-Allow-Credentials" misc.Header
func (b *CORSConfigBuilder) AllowCredentials(allow bool) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.AllowCredentials = allow })
	return b
}

// ExposeHeaders sets the header "Access-Control-Expose-Headers".
func (b *CORSConfigBuilder) ExposeHeaders(headers ...string) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.ExposeHeaders = strings.Join(headers, ",") })
	return b
}

// AllowedHeaders sets the allowed headers.
// It will set the header "Access-Control-Allow-Headers".
func (b *CORSConfigBuilder) AllowedHeaders(headers ...string) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.AllowedHeaders = strings.Join(headers, ",") })
	return b
}

// AllowMethods sets the allowed HTTP methods.
// The header "Access-Control-Allow-Methods" will be used.
func (b *CORSConfigBuilder) AllowMethods(methods ...string) *CORSConfigBuilder {
	b.set = append(b.set, func(opts *CORSConfig) { opts.AllowedMethods = strings.Join(methods, ",") })
	return b
}

// Apply sets the options given to the builder on opts.
func (b *CORSConfigBuilder) Apply(opts *CORSConfig) {
	for _, set := range b.set {
		set(opts)
	}

	if len(b.origins) > 0 {
		opts.AllowedOrigin = strings.Join(b.origins, ",")
	}
}

func corsMiddleware(options *CORSConfig) func(http.Handler) http.Handler {
	if options == nil {
		options = &DefaultCORSConfig
	}

	// Read once, and never written: options can be DefaultCORSConfig itself.
	origins := allowedOrigins(options.AllowedOrigin)
	status := cmp.Or(options.SuccessStatusCode, http.StatusNoContent)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			configureOrigin(options, origins, r, w)
			configureCredentials(options, w)
			configureExposedHeaders(options, w)

			// A preflight request asks whether a browser may send a request, and is answered here. Other requests,
			// OPTIONS ones too, go to the mocks.
			if !isPreflight(r) {
				next.ServeHTTP(w, r)
				return
			}

			configureMethods(options, w, r)
			configureMaxAge(options, w)
			configureHeaders(options, w, r)

			w.Header().Add(HeaderVary, HeaderAccessControlRequestMethod)
			w.Header().Add(HeaderVary, HeaderAccessControlRequestHeaders)
			w.Header().Add(HeaderContentLength, "0")

			w.WriteHeader(status)
		})
	}
}

// isPreflight reports whether r is a CORS preflight request: an OPTIONS request with the origin of a browser and the
// method of the request it wants to send.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get(HeaderOrigin) != "" &&
		r.Header.Get(HeaderAccessControlRequestMethod) != ""
}

// configureHeaders allows the headers set, or else the headers the preflight request asks for. Browsers read * as any
// header only for requests without credentials, and never as Authorization, so * allows the headers asked for too.
func configureHeaders(options *CORSConfig, w http.ResponseWriter, r *http.Request) {
	headers := options.AllowedHeaders
	if h := strings.TrimSpace(headers); h == "" || h == "*" {
		headers = strings.TrimSpace(r.Header.Get(HeaderAccessControlRequestHeaders))
	}

	if headers != "" {
		w.Header().Add(HeaderAccessControlAllowHeaders, headers)
	}
}

func configureMaxAge(options *CORSConfig, w http.ResponseWriter) {
	if options.MaxAge > -1 {
		w.Header().Add(HeaderAccessControlMaxAge, strconv.Itoa(options.MaxAge))
	}
}

// configureMethods allows the methods set. Browsers read * as any method only for requests without credentials, so *
// allows the method the preflight request asks for.
func configureMethods(options *CORSConfig, w http.ResponseWriter, r *http.Request) {
	methods := options.AllowedMethods
	if strings.TrimSpace(methods) == "*" {
		methods = r.Header.Get(HeaderAccessControlRequestMethod)
	}

	if methods != "" {
		w.Header().Add(HeaderAccessControlAllowMethods, methods)
	}
}

func configureExposedHeaders(options *CORSConfig, w http.ResponseWriter) {
	if options.ExposeHeaders != "" {
		w.Header().Add(HeaderAccessControlExposeHeaders, options.ExposeHeaders)
	}
}

func configureCredentials(options *CORSConfig, w http.ResponseWriter) {
	if options.AllowCredentials {
		w.Header().Add(HeaderAccessControlAllowCredentials, "true")
	}
}

// configureOrigin allows the origin of r when it is one of origins. Browsers take one origin, or *, but refuse * for
// requests with credentials: the origin of r is allowed instead. The answer depends on the origin of r, so it varies by
// Origin even when the origin isn't allowed: a cache must not give the answer for one origin to another.
func configureOrigin(options *CORSConfig, origins []string, r *http.Request, w http.ResponseWriter) {
	if len(origins) == 0 {
		return
	}

	w.Header().Add(HeaderVary, HeaderOrigin)

	origin := r.Header.Get(HeaderOrigin)
	anyOrigin := slices.Contains(origins, "*")

	switch {
	case anyOrigin && !options.AllowCredentials:
		w.Header().Add(HeaderAccessControlAllowOrigin, "*")
	case origin != "" && (anyOrigin || slices.Contains(origins, origin)):
		w.Header().Add(HeaderAccessControlAllowOrigin, origin)
	}
}

// allowedOrigins lists the comma-separated origins of allowed, without blanks.
func allowedOrigins(allowed string) []string {
	var origins []string

	for origin := range strings.SplitSeq(allowed, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}

	return origins
}

// dropCORSHeaders removes the CORS headers set on w, such as by the CORS middleware, when h, the headers of the answer
// written to w, has one: an answer that sends a CORS header answers CORS by itself. Browsers refuse a CORS header sent
// twice, and an origin of * with credentials.
func dropCORSHeaders(w http.ResponseWriter, h http.Header) {
	for name := range h {
		if isCORSHeader(name, nil) {
			maps.DeleteFunc(w.Header(), isCORSHeader)

			// A recording leaves out the headers set before the answer, as the server's: these are the answer's.
			if c := captureWriterOf(w); c != nil {
				maps.DeleteFunc(c.before, isCORSHeader)
			}

			return
		}
	}
}

// isCORSHeader reports whether name is the name of a CORS response header. It fits maps.DeleteFunc.
func isCORSHeader(name string, _ []string) bool {
	return strings.HasPrefix(http.CanonicalHeaderKey(name), "Access-Control-")
}
