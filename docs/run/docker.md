# Docker

`ghcr.io/caffeine-projects/vlatte` is an image of the [CLI](/run/cli) for `linux/amd64` and `linux/arm64`, run by a non-root user (`65532`). Each release tags it with its version, with and without the `v`, such as `v0.1.0-rc.1` and `0.1.0-rc.1`, and signs it: see [Verifying a release](/getting-started/installation#verifying-a-release).

```sh
docker run --rm -p 8080:8080 -p 8081:8081 -v "$PWD/testdata/vlatte/_mocks:/vlatte/testdata/vlatte/_mocks:ro" ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1
```

The image sets `VLATTE_DOCKER_HOST`, so it listens on every interface, on fixed ports: mocks on `8080`, or `8443` with `--https`, the admin API on `8081`, and gRPC mocks on `50051`, with their admin API on `8082`. Its working directory is `/vlatte`, so the CLI's defaults apply below it: mock files in `/vlatte/testdata/vlatte/_mocks`, and a `.vlatterc` file in `/vlatte` or `/vlatte/testdata`. Arguments go to `vlatte`, and `VLATTE_*` variables set the same [settings](/run/configuration):

```sh
docker run --rm -p 8080:8080 -e VLATTE_LOG_LEVEL=debug ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1 --proxy-all-target=https://api.example.com
docker run --rm ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1 version
```

The CLI writes its journal and the mocks the admin API adds to `/vlatte/testdata/vlatte`, which the image's user owns. To keep them, or recordings, on the host, mount a `testdata` directory over `/vlatte/testdata` and run as its owner: `--user "$(id -u):$(id -g)" -v "$PWD/testdata:/vlatte/testdata"`. With a read-only root file system, give both of its writable paths a `tmpfs`:

```sh
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp --tmpfs /vlatte/testdata/vlatte:uid=65532,gid=65532 \
  -v "$PWD/testdata/vlatte/_mocks:/vlatte/testdata/vlatte/_mocks:ro" \
  -p 8080:8080 -p 8081:8081 ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1
```

Its `HEALTHCHECK` runs `vlatte health`, which asks the admin API's readiness probe, `GET /readyz` on `8081`, and exits with 0 when it answers 200. With the admin API off or on another address, it checks that the mock server accepts connections on `8080` or `8443` instead, so it needs no settings of its own. Kubernetes can probe `GET /livez` and `GET /readyz` with `httpGet` too.

## Building the image

The `Dockerfile` builds the image from a checkout, for the platforms of its distroless base:

```sh
make docker   # docker build -t vlatte:dev .
docker run --rm vlatte:dev version
```
