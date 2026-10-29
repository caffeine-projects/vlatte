# Docker Compose

This guide runs vlatte next to an app under test with Docker Compose: the app calls a mocked payments API, a test changes the mocks over the admin API, and the admin API asks for a token. It uses [Docker](/run/docker), [mock files](/http/mock-files), the [admin API](/admin-api/) and [outbound rules](/http/proxying#outbound-rules).

## The files

```
.
├── compose.yaml
├── mocks/
│   └── payments_mock.yaml
└── secrets/
    └── admin_token
```

```yaml
# mocks/payments_mock.yaml
# yaml-language-server: $schema=https://caffeine-projects.github.io/vlatte/schemas/http.schema.json
id: create-payment
request:
  method: POST
  path: /payments
response:
  status: 201
  body: {id: pay_1, status: authorized}
```

Generate the token with `openssl rand -hex 32 > secrets/admin_token`.

## The compose file

```yaml
# compose.yaml
services:
  payments:
    image: ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1
    ports:
      - "8080:8080" # mocks
      - "8081:8081" # admin API
    volumes:
      - ./mocks:/vlatte/testdata/vlatte/_mocks:ro
    environment:
      VLATTE_ADMIN_AUTH_TOKEN_FILE: /run/secrets/admin_token
      VLATTE_OUTBOUND_DENY: 169.254.0.0/16
    secrets:
      - admin_token

  app:
    build: .
    environment:
      PAYMENTS_URL: http://payments:8080
    depends_on:
      payments:
        condition: service_healthy

secrets:
  admin_token:
    file: ./secrets/admin_token
```

- **The image** is the published one, pinned to a version. It listens on every interface, on fixed ports: mocks on `8080`, the admin API on `8081`, and gRPC mocks on `50051`, with their admin API on `8082`.
- **Mock files** mount over `/vlatte/testdata/vlatte/_mocks`, which the command serves.
- **The health check** of the image runs `vlatte health`, which asks the admin API's readiness probe. Probes need no credentials, so `service_healthy` works with the token set, and the app starts once the mocks are loaded.
- **The token** comes from a file, as a Docker secret: the token has no flag, so it never shows on the command line.
- **Outbound rules** keep the mocks from reaching cloud metadata at `169.254.169.254`, should a proxy reply or a webhook be pointed there. Add the ranges of your internal networks too, but not the range of the compose network if the mocks proxy to other services in it.

## Changing mocks from a test

The admin API takes a mock in the same format as a file. With the token:

```sh
TOKEN=$(cat secrets/admin_token)

curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"id": "declined", "priority": -1, "request": {"method": "POST", "path": "/payments"}, "response": {"status": 402, "body": {"status": "declined"}}}' \
  localhost:8081/mocks

# ... run the test of a declined payment ...

curl -X POST -H "Authorization: Bearer $TOKEN" localhost:8081/reset
```

`POST /reset` removes the mocks the admin API added, loads the mock files again, starts every scenario over and clears the journal, so the next test starts clean. `QUERY /requests` checks what the app sent: see [Request journal](/more/journal).

The admin pages, such as `http://localhost:8081/`, ask for the token in the browser's sign-in prompt, as the password, with any username.

## Proxies in containers

Docker can set `HTTP_PROXY` and `HTTPS_PROXY` in containers from `~/.docker/config.json`, and so send the hosts of the compose network, such as `http://app:8080`, to that proxy: add them to `NO_PROXY` in the `environment` of the service.

## What you used

- The image, its ports and its health check: [Docker](/run/docker).
- `admin.auth.token_file`, `POST /mocks`, `POST /reset`: [Admin API](/admin-api/).
- `outbound.deny`: [Outbound rules](/http/proxying#outbound-rules).
