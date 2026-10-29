# CORS

`Setup().CORS(...)` answers CORS requests, preflights included, with the headers of its configuration. `mockhttp.CORS()` builds one, and `mockhttp.DefaultCORSConfig` is the default: any origin, the methods `GET`, `POST`, `HEAD`, `PUT`, `DELETE` and `PATCH`, and `204` for a preflight.

```go
m := mockhttp.MustNew(mockhttp.Setup().CORS(mockhttp.CORS().
	AllowOrigin("https://app.example.com").
	AllowCredentials(true).
	ExposeHeaders("X-Request-Id")))
```

| Go | Setting | Sets |
|---|---|---|
| `AllowOrigin(origins...)` | `cors.allowed_origin` | `Access-Control-Allow-Origin`. |
| `AllowMethods(methods...)` | `cors.allowed_methods` | `Access-Control-Allow-Methods`. |
| `AllowedHeaders(headers...)` | `cors.allowed_headers` | `Access-Control-Allow-Headers`. |
| `ExposeHeaders(headers...)` | `cors.expose_headers` | `Access-Control-Expose-Headers`. |
| `AllowCredentials(v)` | `cors.allow_credentials` | `Access-Control-Allow-Credentials`. |
| `MaxAge(seconds)` | `cors.max_age` | `Access-Control-Max-Age`. |
| `SuccessStatusCode(code)` | `cors.success_status_code` | The status of a preflight. |

In settings, `cors: true` (`--cors`) turns CORS on with the defaults, and setting any `cors` key turns it on too.

An answer that sends a CORS header, such as a proxied one, answers CORS by itself: the server's CORS headers aren't sent with it. A `Handler` reply writes its answer on top of the server's CORS headers: it should `Set` its CORS headers, or `Del` the server's. Preflights the server answers aren't [journaled](/more/journal).
