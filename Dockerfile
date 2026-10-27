# syntax=docker/dockerfile:1

# The build stage runs on the build machine's platform and cross-compiles for the target one, so a multi-platform
# build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie@sha256:c124b9ad3452f46061b5054607d26ea51730dae5372c5e469920063ba2bc6dcc AS build

WORKDIR /src

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=bind,source=go.mod,target=go.mod \
    --mount=type=bind,source=go.sum,target=go.sum \
    go mod download

ARG TARGETOS
ARG TARGETARCH

# The context keeps .git: the binary reports its VCS revision as its version.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/vlatte ./cmd/vlatte

# The working directory the server writes its own files to, such as its journal, owned by the image's user.
RUN mkdir -p /out/home/testdata/vlatte

# static: CA certificates, time zones and a non-root user, no shell or package manager.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

ARG VERSION=devel
ARG REVISION=""

LABEL org.opencontainers.image.title="vlatte" \
      org.opencontainers.image.description="Build Mock APIs in Go" \
      org.opencontainers.image.url="https://github.com/caffeine-projects/vlatte" \
      org.opencontainers.image.source="https://github.com/caffeine-projects/vlatte" \
      org.opencontainers.image.documentation="https://github.com/caffeine-projects/vlatte#docker" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"

COPY --from=build --chown=nonroot:nonroot /out/home /vlatte
COPY --from=build /out/vlatte /usr/local/bin/vlatte

WORKDIR /vlatte
USER nonroot:nonroot

# Listens on every interface: mocks on 8080, or 8443 with HTTPS, the admin API on 8081, gRPC mocks on 50051 and
# their admin API on 8082.
ENV VLATTE_DOCKER_HOST=0.0.0.0

EXPOSE 8080 8443 8081 50051 8082

ENTRYPOINT ["/usr/local/bin/vlatte"]
