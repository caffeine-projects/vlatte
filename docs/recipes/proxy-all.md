# Proxy all

`recipe/http/proxyall` prepares an HTTP server to send what no other mock matches to a real service. Mock the endpoints a test cares about, and let everything else reach the service as it is.

```go
import "github.com/caffeine-projects/vlatte/recipe/http/proxyall"

m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

`proxyall.New(reply)` adds the mock `ProxyAll(reply)` makes: it matches any method and path, at priority `math.MaxInt`, so every other mock answers first. It takes the options of the [proxy reply](/http/proxying) it is given, such as headers to add or a timeout.

Without Go, a mock file's `proxy_all` key, or the `proxy_all` setting (`vlatte --proxy-all-target=https://api.example.com`), does the same: see [Proxy all](/http/proxying#proxy-all).
