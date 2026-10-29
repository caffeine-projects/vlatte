# Security policy

## Supported versions

Only the latest release gets security fixes.

## Reporting a vulnerability

Report it privately through [GitHub](https://github.com/caffeine-projects/vlatte/security/advisories/new), not in a public issue. Include the version, how to reproduce it, and what an attacker gains.

## What happens next

- We acknowledge the report within 7 days.
- We confirm the vulnerability, or explain why it is not one, and keep you updated while we work on a fix.
- We release the fix and publish a GitHub Security Advisory within 90 days of the report, and credit you unless you ask us not to.
- Please keep the vulnerability private until the advisory is published. If a fix takes longer than 90 days, we agree on a disclosure date with you.

## Scope

vlatte is a test tool: it does what its mocks and settings declare. These are not vulnerabilities:

- A mock replies exactly what it declares. Its body can echo the request, its cookies are sent as declared, without `Secure` unless they set it, and vlatte adds no security headers to it. A recorded or proxied reply is what the target sent.
- Proxy replies, recordings, the forward proxy and webhooks connect where they are told. `outbound.allow` and `outbound.deny` limit where.

Run mock servers and the admin API where only your tests and tools reach them. Where others can, as in Docker or a shared environment, protect the admin API with `admin.BasicAuth` or `admin.BearerToken`, serve it with `admin.TLS`, and set the outbound rules.

Report anything beyond that, such as a flaw in vlatte's own answers, like the one to a request no mock matched, in the admin API and its credentials check, or a mock or setting that does what it doesn't declare.
