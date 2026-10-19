package mockhttp

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// CORSConfig represents the possible options to configure CORS.
type CORSConfig struct {
	AllowedOrigin     string
	AllowCredentials  bool
	AllowedMethods    string
	AllowedHeaders    string
	ExposeHeaders     string
	MaxAge            int
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

// CORSConfigBuilder facilitates building CORS options.
type CORSConfigBuilder struct {
	options *CORSConfig
	origins []string
}

// CORS initializes a CORSConfig builder for a fluent configuration.
func CORS() *CORSConfigBuilder {
	return &CORSConfigBuilder{
		origins: make([]string, 0),
		options: &CORSConfig{SuccessStatusCode: http.StatusNoContent},
	}
}

// SuccessStatusCode sets a custom status code returned on CORS Options request.
// If none is specified, the default status code is http.StatusNoContent.
func (b *CORSConfigBuilder) SuccessStatusCode(code int) *CORSConfigBuilder {
	b.options.SuccessStatusCode = code
	return b
}

// MaxAge sets CORS max age.
func (b *CORSConfigBuilder) MaxAge(maxAge int) *CORSConfigBuilder {
	b.options.MaxAge = maxAge
	return b
}

// AllowOrigin sets the allowed origins.
func (b *CORSConfigBuilder) AllowOrigin(origin ...string) *CORSConfigBuilder {
	b.origins = append(b.origins, origin...)
	return b
}

// AllowCredentials sets "Access-Control-Allow-Credentials" misc.Header
func (b *CORSConfigBuilder) AllowCredentials(allow bool) *CORSConfigBuilder {
	b.options.AllowCredentials = allow
	return b
}

// ExposeHeaders sets the header "Access-Control-Expose-Headers".
func (b *CORSConfigBuilder) ExposeHeaders(headers ...string) *CORSConfigBuilder {
	b.options.ExposeHeaders = strings.Join(headers, ",")
	return b
}

// AllowedHeaders sets the allowed headers.
// It will set the header "Access-Control-Allow-Headers".
func (b *CORSConfigBuilder) AllowedHeaders(headers ...string) *CORSConfigBuilder {
	b.options.AllowedHeaders = strings.Join(headers, ",")
	return b
}

// AllowMethods sets the allowed HTTP methods.
// The header "Access-Control-Allow-Methods" will be used.
func (b *CORSConfigBuilder) AllowMethods(methods ...string) *CORSConfigBuilder {
	b.options.AllowedMethods = strings.Join(methods, ",")
	return b
}

// Apply builds CORS configurations based on previous settings set via the builder.
func (b *CORSConfigBuilder) Apply(opts *CORSConfig) {
	b.build().Apply(opts)
}

func (b *CORSConfigBuilder) build() *CORSConfig {
	if len(b.origins) > 0 {
		if len(b.origins) == 1 {
			b.options.AllowedOrigin = b.origins[0]
		} else {
			b.options.AllowedOrigin = strings.Join(b.origins, ",")
		}
	}

	return b.options
}

func corsMiddleware(options *CORSConfig) func(http.Handler) http.Handler {
	if options == nil {
		options = &DefaultCORSConfig
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// preflight request
			if r.Method == http.MethodOptions {
				configureOrigin(options, r, w)
				configureCredentials(options, w)
				configureExposedHeaders(options, w)
				configureMethods(options, w)
				configureMaxAge(options, w)
				configureHeaders(options, w, r)

				w.Header().Add(HeaderVary, HeaderAccessControlRequestHeaders)
				w.Header().Add(HeaderContentLength, "0")

				w.WriteHeader(options.SuccessStatusCode)
				return
			}

			configureOrigin(options, r, w)
			configureCredentials(options, w)
			configureExposedHeaders(options, w)

			next.ServeHTTP(w, r)
		})
	}
}

func configureHeaders(options *CORSConfig, w http.ResponseWriter, r *http.Request) {
	// when allowed headers aren't specified, use values from header access-control-request-headers
	if options.AllowedHeaders != "" {
		w.Header().Add(HeaderAccessControlAllowHeaders, options.AllowedHeaders)
	} else {
		hs := r.Header.Get(HeaderAccessControlRequestHeaders)
		if strings.TrimSpace(hs) != "" {
			w.Header().Add(HeaderAccessControlAllowHeaders, hs)
		}
	}
}

func configureMaxAge(options *CORSConfig, w http.ResponseWriter) {
	if options.MaxAge > -1 {
		w.Header().Add(HeaderAccessControlMaxAge, strconv.Itoa(options.MaxAge))
	}
}

func configureMethods(options *CORSConfig, w http.ResponseWriter) {
	if len(options.AllowedMethods) > 0 {
		w.Header().Add(HeaderAccessControlAllowMethods, options.AllowedMethods)
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

func configureOrigin(options *CORSConfig, r *http.Request, w http.ResponseWriter) {
	if options.AllowedOrigin == "" {
		return
	}

	origins := strings.Split(options.AllowedOrigin, ",")
	size := len(origins)

	if size == 1 {
		w.Header().Add(HeaderAccessControlAllowOrigin, options.AllowedOrigin)
		w.Header().Add(HeaderVary, HeaderOrigin)
		return
	}

	// received a list of origins
	// will check if the request origin is within the provided array and use it as the allowed origin
	origin := r.Header.Get(HeaderOrigin)
	allowed := slices.Contains(origins, origin)

	if allowed {
		w.Header().Add(HeaderAccessControlAllowOrigin, origin)
		w.Header().Add(HeaderVary, HeaderOrigin)
	}
}
