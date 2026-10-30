# Testing a frontend client

Run VLatte as a local API for a web or mobile app, without installing Go. This guide serves a mock from a YAML file, calls it from a browser with `fetch`, and verifies the request through the admin API. It uses the [CLI](/run/cli), [mock files](/http/mock-files), [CORS](/http/cors) and [request verification](/verify/admin-api).

## Prepare the server

These docs describe the current source, whose verification API may be newer than a published release. Build its [Docker image](/run/docker#building-the-image); Go runs inside the build container, so it isn't needed on the host:

```sh
git clone https://github.com/caffeine-projects/vlatte.git
cd vlatte
docker build -t vlatte:dev .
mkdir -p frontend-example/mocks
cd frontend-example
```

## Create the mock

Save this as `mocks/user_mock.yaml`:

```yaml
# yaml-language-server: $schema=https://vlatte.dev/schemas/http.schema.json
id: get-user
request:
  method: GET
  path: /users/42
  header:
    authorization: Bearer test-token
response:
  status: 200
  header:
    content-type: application/json
  body:
    id: 42
    name: ann
```

Start the server in one terminal:

```sh
docker run --rm -p 127.0.0.1:8080:8080 -p 127.0.0.1:8081:8081 \
  -v "$PWD/mocks:/vlatte/testdata/vlatte/_mocks:ro" \
  vlatte:dev --journal-dir="" \
  --cors-allowed-origin=http://localhost:5173
```

The mock API listens on port `8080`; the admin API listens on `8081`. `--journal-dir=""` keeps this run's requests in a temporary directory, so a previous run doesn't affect the count. The CORS origin is the address of the web app, including its scheme and port.

## Call it from a browser

Save this as `index.html` next to the `mocks` directory:

```html
<!doctype html>
<html lang="en">
  <meta charset="utf-8">
  <title>Users client</title>
  <p id="result">Loading…</p>
  <script type="module">
    const baseURL = 'http://127.0.0.1:8080';
    const result = document.querySelector('#result');

    try {
      const response = await fetch(`${baseURL}/users/42`, {
        headers: { Authorization: 'Bearer test-token' },
      });
      if (!response.ok) {
        throw new Error(`users: status ${response.status}`);
      }
      const user = await response.json();
      if (user.id !== 42 || user.name !== 'ann') {
        throw new Error('users: unexpected user');
      }
      result.textContent = user.name;
    } catch (error) {
      result.textContent = error.message;
    }
  </script>
</html>
```

Serve the page with your development server on `http://localhost:5173`. For this standalone example, Python's server works too:

```sh
python3 -m http.server 5173 --bind 127.0.0.1
```

Open `http://localhost:5173` in the browser. The page should show `ann`. In an existing app, make the API base URL a development or test setting and point it at VLatte instead of the real API.

The `Authorization` header makes the browser send an `OPTIONS` preflight before the `GET`. VLatte's CORS support answers that preflight and allows the requested header. It isn't a mock and isn't counted in the journal. A request made with `credentials: 'include'` also needs `--cors-allow-credentials` and an allowed origin; see [CORS](/http/cors).

## Verify what the app sent

From another terminal, check the method, path and token, and require one request:

```sh
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"method":"GET","path":"/users/42","header":{"authorization":"Bearer test-token"},"count":1,"within":"2s"}' \
  http://127.0.0.1:8081/requests/verify
```

The answer's `passed` must be `true` and `count` must be `1`. A check that ran returns HTTP `200` even when it failed, so an automated test must assert the `passed` field too. `within` waits for the request to be journaled; it doesn't add a fixed pause to the test.

Before running the page again, reset the server to clear the journal and reload the mock files:

```sh
curl -X POST http://127.0.0.1:8081/reset
```

For tests that share a server, use [marks](/verify/admin-api#marks) to select each test's own requests. To try an error screen, change the mock's response status to `503`, reload with `POST /mocks/reload`, and reload the page. The example displays `users: status 503`.

## Connect a mobile app

The same mock files work with native clients. Make the app's API base URL configurable for the test build, then choose an address the device can reach:

| Where the app runs | Address to use |
|---|---|
| Browser on the development machine | `http://127.0.0.1:8080` as above. |
| Android Emulator | `http://10.0.2.2:8080`, the emulator's alias for the host's loopback interface. See [Android Emulator network addresses](https://developer.android.com/studio/run/emulator-networking-address). |
| Physical device | The development machine's reachable network address, such as `http://192.168.1.20:8080`. The device's `localhost` refers to the device itself. |

For a physical device, bind the mock API to the network interface and keep the admin API local:

```sh
docker run --rm -p 8080:8080 -p 127.0.0.1:8081:8081 \
  -v "$PWD/mocks:/vlatte/testdata/vlatte/_mocks:ro" \
  vlatte:dev --journal-dir=""
```

Check that the device and host can reach each other and that the host firewall allows port `8080`. For other simulators or a WebView, check that runtime's host-routing and browser-origin rules. CORS applies to browsers and WebViews, rather than a native HTTP client.

Network access and certificate trust are separate: an app can reach VLatte and still reject plain HTTP or an untrusted certificate. Android's [Network Security Configuration](https://developer.android.com/privacy-and-security/security-config) covers cleartext policy and debug CA trust. Apple's [App Transport Security](https://developer.apple.com/documentation/security/preventing-insecure-network-connections) covers secure connections and exceptions. Use a test certificate trusted by the app and valid for the hostname or IP it calls; [HTTPS and mTLS mocks](/guides/https-mtls) shows how VLatte serves one. Keep test-only network settings out of release builds.

## What you used

- [Mock files](/http/mock-files) and their [schemas](/reference/schemas).
- The [CLI](/run/cli), [Docker](/run/docker), and [CORS](/http/cors).
- [Request verification through the admin API](/verify/admin-api) and [reset](/admin-api/).
