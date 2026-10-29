# HTTPS and mTLS mocks

This guide tests a client of an API that serves HTTPS only, first with a certificate the client trusts, then with mutual TLS, where the client must present a certificate of its own. Then the `vlatte` command serves the same mock with the same certificates. It uses [TLS](/tls/) and [mock files](/http/mock-files).

## The client

```go
// Client calls the inventory API, which serves HTTPS only.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) Stock(ctx context.Context, sku string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/stock/"+sku, nil)
	if err != nil {
		return 0, err
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("inventory: stock of %s: %s", sku, res.Status)
	}

	var body struct {
		Available int `json:"available"`
	}

	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return 0, err
	}

	return body.Available, nil
}
```

## Certificates

[mkcert](https://github.com/FiloSottile/mkcert) makes a CA and the certificates it signs. Put them in `testdata`, next to the tests:

```sh
mkcert -cert-file testdata/localhost.pem -key-file testdata/localhost-key.pem localhost 127.0.0.1
mkcert -client -cert-file testdata/client.pem -key-file testdata/client-key.pem client
cp "$(mkcert -CAROOT)/rootCA.pem" testdata/ca.pem
```

The server certificate names `127.0.0.1`, as `m.URL()` does. Keys of test CAs belong to tests only: never give one to a real service.

Without certificates of your own, `m.MustStartTLS()` serves a test certificate the client must skip verifying. That tests less: a client that verifies certificates, as it should, needs one it can trust.

## Helpers

`newInventory` starts an HTTPS mock of the API with the TLS options it's given. `clientTLS` makes an HTTP client that trusts the test CA, with the client certificate or without it.

```go
// newInventory starts an HTTPS mock of the inventory API with the setup given.
func newInventory(t *testing.T, setup *mockhttp.ConfigBuilder) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(setup.Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/stock/sku-1").Reply(mockhttp.OK().JSON(map[string]any{"available": 7})))

	return m
}

// clientTLS trusts the test CA, and presents the client certificate when asked to.
func clientTLS(t *testing.T, withCert bool) *http.Client {
	t.Helper()

	ca, err := os.ReadFile("testdata/ca.pem")
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	conf := &tls.Config{RootCAs: pool}

	if withCert {
		cert, err := tls.LoadX509KeyPair("testdata/client.pem", "testdata/client-key.pem")
		require.NoError(t, err)

		conf.Certificates = []tls.Certificate{cert}
	}

	return &http.Client{Transport: &http.Transport{TLSClientConfig: conf}}
}
```

## Over HTTPS

`TLSCertKeyPair` serves the server certificate, which the client verifies against the CA:

```go
func TestStock_OverHTTPS(t *testing.T) {
	m := newInventory(t, mockhttp.Setup().TLSCertKeyPair("testdata/localhost.pem", "testdata/localhost-key.pem"))
	client := &Client{BaseURL: m.URL(), HTTP: clientTLS(t, false)}

	n, err := client.Stock(t.Context(), "sku-1")

	require.NoError(t, err)
	require.Equal(t, 7, n)
}
```

## With mutual TLS

`TLSMutual` serves the same certificate, and asks the client for one the CA signed. The client with a certificate gets the stock; the one without is refused before any request is sent, so no mock sees it:

```go
func TestStock_WithMutualTLS(t *testing.T) {
	m := newInventory(t, mockhttp.Setup().
		TLSMutual("testdata/localhost.pem", "testdata/localhost-key.pem", "testdata/ca.pem"))

	t.Run("a client with a certificate gets the stock", func(t *testing.T) {
		client := &Client{BaseURL: m.URL(), HTTP: clientTLS(t, true)}

		n, err := client.Stock(t.Context(), "sku-1")

		require.NoError(t, err)
		require.Equal(t, 7, n)
	})

	t.Run("a client without one is refused", func(t *testing.T) {
		client := &Client{BaseURL: m.URL(), HTTP: clientTLS(t, false)}

		_, err := client.Stock(t.Context(), "sku-1")

		require.ErrorContains(t, err, "certificate required")
	})
}
```

## The same mock from the CLI

The `vlatte` command serves a mock file with the same certificates:

```yaml
# testdata/vlatte/_mocks/stock.mock.yaml
request:
  method: GET
  path: /stock/sku-1
response:
  status: 200
  header:
    content-type: application/json
  body: '{"available": 7}'
```

```sh
vlatte --https --addr=127.0.0.1:8443 \
  --tls-cert=testdata/localhost.pem --tls-key=testdata/localhost-key.pem --tls-ca=testdata/ca.pem

curl --cacert testdata/ca.pem --cert testdata/client.pem --key testdata/client-key.pem \
  https://localhost:8443/stock/sku-1
# {"available": 7}
```

In [Docker](/run/docker), mount `testdata` and set `VLATTE_HTTPS=true`, `VLATTE_TLS_CERT`, `VLATTE_TLS_KEY` and `VLATTE_TLS_CA`: the image serves HTTPS on port `8443`.
