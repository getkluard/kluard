# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems.
Use GitHub's private reporting: **Security → Report a vulnerability** on this repository
(<https://github.com/getkluard/kluard/security/advisories/new>).

Include what you found, how to reproduce it, and the version. This is an early-stage project maintained by a small team,
so these are goals and not guarantees: we aim to answer within 5 working days, and to release a fix for a serious problem
(someone getting past the login, reading another tenant's data, running code on the server) within a few days of
confirming it. The fix is announced in the release notes and in `CHANGELOG.md`, marked as a security fix, and you are
credited if you wish.

## Supported versions

Only the latest release and the latest commit on `main` receive fixes. Upgrade to the newest release to get them; see
[UPGRADING.md](UPGRADING.md).

## How the project is kept patched

| What | How often | How |
|---|---|---|
| Known vulnerabilities in the Go toolchain and standard library (Kluard has no other Go dependencies) | Every week, automatically | The *Security* workflow runs `govulncheck` on Monday and when `go.mod` or the Dockerfile changes. A red run means the Go version must be raised. |
| GitHub Actions, the Go image and the ClickHouse and Elasticsearch images | Every week, automatically | Dependabot proposes updates; CI shows whether they are safe to merge. |
| Go itself | At least once a month, and at once for a fix that reaches Kluard | Go releases security fixes about monthly, often in `net/http` and `crypto/tls`. The `go` line in `go.mod` and the `golang:` image in the Dockerfile follow a supported Go version (the two newest). |
| Releases | A patch release (`v0.x.y`) after any of the above that changed something | Release binaries are built with the newest patch of the Go version in `go.mod`. |
| A serious problem in Kluard itself | A few days (a goal, see above) | Fix, patch release, security note in `CHANGELOG.md`. |

**What you do:** upgrade to a new release at least once a month and at once after a security note
([UPGRADING.md](UPGRADING.md)), and rebuild Docker-based servers regularly so that the base images are refreshed. Update the
agents too: the Hosts page marks those that differ from the server.

## What the project already does

- Passwords: salted PBKDF2-HMAC-SHA256, 600,000 iterations. Sessions: signed, HttpOnly, `SameSite=Strict` cookies. Logins are throttled.
- API keys are stored only as SHA-256 hashes and shown once.
- Stored Nextcloud credentials are encrypted with AES-GCM (`KLUARD_SECRET_KEY`) and never returned by the API.
- Every query is filtered by the tenant of the authenticated caller; permissions are enforced by the server, not the UI.
- Log paths pushed to agents from the server are checked against an allow-list on the machine.
- The UI's content-security policy allows only same-origin scripts, styles and connections, and `data:` images (for the logo preview and the generated tab icon).
- The logo upload is verified by its content, size-limited, refuses SVG with scripts or event handlers, and is served with `Content-Security-Policy: sandbox` and `nosniff`.

## Hardening your deployment

- Serve Kluard over **HTTPS** (API keys and agent configuration travel over it).
- Keep `deploy/.env` private (`chmod 600`) and back it up: it holds `KLUARD_SECRET_KEY`.
- Do not use `KLUARD_DEV_MODE=true` on a network you do not control: it disables authentication.
- Giving an agent `--docker` gives it access to the Docker socket, which is equivalent to root on that machine.
- The backup folder contains all tenants' data and a configuration dump with password hashes. Protect it like the database.
