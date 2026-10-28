# Installation

## The library

Requires Go 1.27 or later.

```sh
go get github.com/caffeine-projects/vlatte
```

Import the packages you use: `mockhttp` for HTTP, `mockgrpc` for gRPC, and `match` for matchers.

| Package | What it's for |
|---|---|
| `mockhttp` | HTTP mock servers |
| `mockgrpc` | gRPC mock servers |
| `match` | Request matchers, such as `match.Equal`, `match.JSONPath` and `match.All` |
| `match/matchbuild` | The matcher lists of mock files, and the registry of custom matchers |
| `match/matchscenario` | Scenario state for mock servers |
| `webhook` | Calling a URL after a mock is served |
| `fault` | Steps that make an HTTP mock answer wrong |
| `journal` | The request journal |
| `admin` | The admin API |
| `recipe/<protocol>/<recipe>` | Recipes that prepare a server, such as `recipe/http/proxyall` |
| `vlatte` | The types every mock server shares, and the Go template engine |

## The CLI

### Homebrew

On macOS and Linux:

```sh
brew install caffeine-projects/tap/vlatte
```

### An archive

[GitHub Releases](https://github.com/caffeine-projects/vlatte/releases) has an archive for Linux, macOS and Windows, on `amd64` and `arm64`. Each holds the `vlatte` binary, which needs nothing else:

```sh
VERSION=v0.1.0-rc.1
curl -fsSL "https://github.com/caffeine-projects/vlatte/releases/download/$VERSION/vlatte_${VERSION#v}_linux_amd64.tar.gz" | tar -xz vlatte
```

### Go

Requires Go 1.27 or later.

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

### Docker

The image is `ghcr.io/caffeine-projects/vlatte`, for `linux/amd64` and `linux/arm64`: see [Docker](/run/docker).

```sh
docker run --rm ghcr.io/caffeine-projects/vlatte:v0.1.0-rc.1 version
```

## Verifying a release

The release workflow of this repository builds every archive and image, and signs them with [cosign](https://docs.sigstore.dev/cosign/) without a key: the signature names the workflow and the tag it ran for. Each archive also has an SBOM, `<archive>.sbom.json`, and a provenance attestation.

An archive: check the signature of `checksums.txt`, then the archive against it.

```sh
VERSION=v0.1.0-rc.1
ARCHIVE="vlatte_${VERSION#v}_linux_amd64.tar.gz"
gh release download "$VERSION" -R caffeine-projects/vlatte -p checksums.txt -p checksums.txt.sigstore.json -p "$ARCHIVE"

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/caffeine-projects/vlatte/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt # shasum -a 256 on macOS
```

Or, with the GitHub CLI alone, check its provenance:

```sh
gh attestation verify "$ARCHIVE" -R caffeine-projects/vlatte
```

The image:

```sh
cosign verify "ghcr.io/caffeine-projects/vlatte:$VERSION" \
  --certificate-identity "https://github.com/caffeine-projects/vlatte/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify "oci://ghcr.io/caffeine-projects/vlatte:$VERSION" -R caffeine-projects/vlatte
```

## Versions

Versions follow [semantic versioning](https://semver.org). Until v1.0.0, a minor version may change the API. A version with a suffix, such as `v0.1.0-rc.1`, is a release candidate. The changes of each version are on [GitHub Releases](https://github.com/caffeine-projects/vlatte/releases).

## Editor support for mock files

Mock files have JSON Schemas at stable addresses. Point your editor at them to get completion and validation: see [Mock file schemas](/reference/schemas).
