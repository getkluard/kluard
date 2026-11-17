# Kluard security overview

*Svenska: [SECURITY-OVERVIEW.sv.md](SECURITY-OVERVIEW.sv.md). To report a vulnerability, see [SECURITY.md](../SECURITY.md).*

This page describes what Kluard does to protect your data, where the limits are, and what you must do yourself. It is written from the code of the community edition (Apache-2.0), version 0.1.x. Enterprise features (`ee/`: SAML, SCIM, enforced MFA and so on, see [EDITIONS.md](EDITIONS.md)) are only mentioned where they differ.

**What this page is not.** Kluard has not had an independent security audit or penetration test. Everything below is what the project's own code and tests do. Treat it as a description to check against your own requirements, not as a certification.

## 1. The parts and what they hold

| Part | What it holds | Who can reach it |
|---|---|---|
| **Kluard server** (one Go program, runs as a non-root user in a "distroless" container) | Serves the web UI and the API on port 4318 | Anyone who can reach port 4318: put HTTPS in front of it |
| **ClickHouse** | Telemetry: traces, logs and metrics, per tenant | Only the Kluard server, on the internal Docker network. Port 8123 is not published. Password-protected |
| **Elasticsearch** | Users, groups, API-key hashes, dashboards, hosts, instances (with encrypted credentials), the session secret, audit entries | Only the Kluard server, on the internal Docker network. Port 9200 is not published. Password-protected (`xpack.security`), plain HTTP inside that network |
| **Agents** (on the machines you monitor) | A config file with the API key and, if you use them, Nextcloud credentials | They connect **out** to the server. Nothing connects in to an agent unless you switch on its metrics port |
| **Backup folder** | Daily exports of telemetry, and a configuration dump | The server's disk. It is only readable by whoever has access to the server |

Release-file installs (way B in [INSTALL.md](../INSTALL.md)) run the same parts: the server under systemd and ClickHouse in one Docker container that listens only on `127.0.0.1`.

## 2. Signing in

- **People** sign in with a username and password. Passwords are stored as salted PBKDF2-HMAC-SHA256 with 600,000 iterations and compared in constant time.
- **Sessions** are signed cookies: `HttpOnly`, `SameSite=Strict`, `Secure` when the request came over HTTPS (also behind a proxy that sends `X-Forwarded-Proto: https`). A session lasts 12 hours. Changing a password ends that user's sessions.
- **Login throttling.** After 10 failed attempts within 10 minutes a username is blocked for a while. API-key sign-ins are throttled per client address. The counters live in the server's memory, so a restart resets them.
- **Cross-site requests.** The API only accepts JSON bodies (`Content-Type: application/json`), which a browser cannot send from another site without permission. Together with `SameSite=Strict` cookies this blocks cross-site request forgery. Bodies are size-limited.
- **Machines** use **API keys**. Keys are stored only as SHA-256 hashes and shown once. A browser session can never send telemetry. A person can also sign in with an API key; that session cannot manage keys, users or passwords and ends when the key is deleted.
- **Not included in the community edition:** built-in multi-factor authentication. Enforced MFA, SAML and SCIM are Enterprise features. If you need MFA for the community edition, put an authenticating proxy (for example one that does SSO) in front of Kluard.

## 3. Who can see what

- **Tenants.** Every query against telemetry is filtered by the tenant of the signed-in caller, in the server, with the tenant passed as a query parameter and not pasted into the SQL. A hidden or edited UI does not change what the API allows.
- **Permissions.** Users belong to groups; each group has permissions (for example Alerts, Notification channels, Keys, Users). The server checks them on every request. The built-in *User* group can read alerts but cannot see notification channels, because channels hold secrets.
- **Audit.** Changes are recorded (who, what, from which address, signed in how). In installations with several tenants, a tenant's administrators can read the entries about their own tenant, including support access by the operator.

## 4. Secrets and encryption

- **Stored credentials** (Nextcloud tokens, app passwords, channel secrets) are encrypted with AES-256-GCM before they are saved, with the key derived from `KLUARD_SECRET_KEY`. They are never returned by the API.
- **`KLUARD_SECRET_KEY` must be random and kept.** The installer creates a random one. The key is derived with a single SHA-256, which is fine for a random key and weak for a chosen word. Without the key the stored credentials cannot be read again. If it is not set, Kluard falls back to the session secret and logs a warning.
- **`deploy/.env`** holds the key, the database passwords and the first admin password. Keep it `chmod 600`, back it up outside the server, and do not commit it.
- **Passwords of users** are never stored in readable form (see section 2). API keys likewise.
- **Encryption on the wire.** Kluard itself speaks plain HTTP on port 4318. API keys and agent configuration (which contains Nextcloud credentials for the checks the agent runs) travel over that connection, so **use HTTPS**: Nginx or Caddy in front, and `KLUARD_BIND=127.0.0.1` so the plain port is not open to the network. The server does not set HSTS itself; set it in the proxy. Traffic between Kluard and the databases is plain HTTP on the internal Docker network.
- **Data at rest** is not encrypted by Kluard. Use an encrypted disk or volume if you need it.

## 5. The agent

- **Outbound only.** The agent connects to the server's address with its API key and sends data. It also connects to the services you asked it to check (Nextcloud, nginx, databases, S3). The server cannot make an agent run commands: it can only change the agent's settings and offer a new agent version (see *Updates are checked* below).
- **What it starts.** The only programs the agent runs itself are `systemctl` (to list services) and, when updating, the new agent file with `-version` to check that it starts.
- **Remote settings are limited.** The server can tell an agent which log files to read, but the agent refuses paths outside an allow-list (`/var/log`, Docker's data folders, `/var/www`, `/srv`, `/mnt`, `/opt`), relative paths and `..`. The list can only be widened in the agent's own, root-owned config file.
- **Installed as a service** the agent runs as an own user (`kluard-agent`) with `NoNewPrivileges`, `ProtectSystem=strict` and `PrivateTmp`, and one added capability, read-only access to files (`CAP_DAC_READ_SEARCH`), so it can read logs that belong to other users. It cannot write outside its own state folder. Its config file is `root:kluard-agent`, mode 640.
- **Docker.** `--docker` gives the agent access to the Docker socket. **That is equivalent to root on that machine.** Use it only on machines where that is acceptable. With rootless Docker (`--docker-rootless`) the agent runs as that user with that user's own socket, which is less than root.
- **Updates are checked.** The installer and the self-update compare the downloaded agent with the SHA-256 sums published by *your* Kluard server and refuse a mismatch. This protects against a corrupt download, and against a changed file as long as the server itself is not compromised: a compromised Kluard server can offer a malicious agent, so protect the server like a management system.
- **Windows agent:** not reviewed for this page.

## 6. What the server connects to

By design the server connects out only to ClickHouse and Elasticsearch, and to the addresses people configure: alert channels (webhooks and mail servers) and, in the Enterprise edition, an identity provider (OIDC). There is no usage reporting or "phone home", and licences are checked offline (signed with Ed25519, no request to us). No other outbound client was found in the server code.

Alert channels let a user make the server connect to an address they type. To stop that being used to reach your internal network, Kluard **refuses loopback, private and link-local addresses**, checked on the real connection (so also after name resolution and redirects). Link-local addresses (169.254.x.x, where cloud providers keep instance credentials) stay blocked even when an administrator allows private addresses with `KLUARD_ALERT_ALLOW_PRIVATE=true`.

## 7. The web UI

- The UI is served with `Content-Security-Policy: default-src 'self'; img-src 'self' data:; ... frame-ancestors 'none'` and `X-Content-Type-Options: nosniff`. `frame-ancestors 'none'` stops it being shown in a frame on another site.
- **A limit:** the UI is one HTML file with its script inline, so the policy has to allow inline scripts (`'unsafe-inline'`). It does limit where scripts, styles and connections can come from, but it is not the strongest form of the policy.
- Uploaded logos are checked by their real content, size-limited, refused if an SVG contains scripts, and served with `Content-Security-Policy: sandbox` and `nosniff`.
- The public status page (if you use it) is served with its own content-security policy and `no-referrer`.

## 8. Backups

Backups hold **all tenants' data** and a configuration dump with password hashes and encrypted credentials. They are for the operator only and are never served by the API. They live on the same server as the data by default: copy them elsewhere, and protect that copy like the database. Retention is set in *Settings, How long data is kept*.

## 9. Keeping Kluard patched

Kluard has no Go dependencies besides the Go standard library, so the supply chain is short. A weekly automatic check for known vulnerabilities runs on the Go toolchain, and update proposals for GitHub Actions and the base images are made weekly. See [SECURITY.md](../SECURITY.md) for the schedule and for how to report a problem. The databases are run on versions their makers still patch (Elasticsearch 8.19, ClickHouse 26.8; see [UPGRADING.md](../UPGRADING.md)), and updating them is a decision to take and test on a copy.

## 10. What you must do

1. Serve Kluard over **HTTPS** and keep port 4318 closed to the network (`KLUARD_BIND=127.0.0.1`) when a proxy sits in front. Docker-published ports usually bypass firewalld and ufw, so do not rely on the firewall alone.
2. Keep `deploy/.env` private and backed up. Never lose `KLUARD_SECRET_KEY`.
3. **Never** run `KLUARD_DEV_MODE=true` on a network you do not control: it switches authentication off.
4. Use strong, separate passwords, and delete API keys and users you no longer use.
5. Give `--docker` only to machines where Docker-socket access is acceptable.
6. Upgrade Kluard at least monthly and at once after a security note ([UPGRADING.md](../UPGRADING.md)); update the agents too (the Hosts page marks those that differ).
7. Protect the backup folder and the server's disk.
8. If you need MFA or SSO, use an identity-aware proxy, or the Enterprise edition.

## 11. Known limits

- No built-in MFA in the community edition.
- Login throttle counters are kept in memory and reset on restart. Behind a proxy that does not pass the client address on, all clients share the proxy's address for the API-key throttle.
- The web UI's content-security policy allows inline scripts.
- Data at rest is not encrypted by Kluard.
- A compromised Kluard server can hand out a malicious agent update.
- No independent audit has been done.
