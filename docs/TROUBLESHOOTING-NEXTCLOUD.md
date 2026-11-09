# Troubleshooting Nextcloud monitoring

See also [Nextcloud metrics, panels and alerts](NEXTCLOUD-METRICS.md) for every metric and the alerts to set up.

Find the message you see in Kluard, read what it means, and run the check. Everything below has been seen on real installations.

## Before you start

**Which machine runs the check?** Under the instance, "checked by" names a machine. The agent on **that** machine reaches Nextcloud, not Kluard's server and not your laptop. Run every `curl` below **on the "checked by" machine**. A test from another machine can fail (`HTTP 000`) while the agent works, or the other way round.

**Where is the reason?** The page says "the reason is in the agent's log". Read it on the "checked by" machine:

```bash
systemctl list-units --type=service | grep -i -E "kluard|lumen"
journalctl -u NAME-OF-THE-SERVICE -n 30 --no-pager | cut -c1-350
```

Look for `nextcloud poll problem`. The text after `err` is the exact cause.

**Running `occ`.** How you run it depends on how Nextcloud is installed. Replace `NC` with the container name (`docker ps` shows it):

| Installation | Command |
|---|---|
| Docker | `docker exec -u www-data NC php occ ...` |
| Nextcloud AIO | `docker exec --user www-data -it nextcloud-aio-nextcloud php occ ...` |
| Package or manual | `sudo -u www-data php /var/www/nextcloud/occ ...` (the web user may be `apache` or `nginx`) |
| Snap | `sudo nextcloud.occ ...` |

**Test the token as the agent does:**

```bash
TOKEN='YOUR_TOKEN'
curl -s -o /tmp/si.txt -w 'HTTP %{http_code}\n' -H "Accept: application/json" -H "OCS-APIRequest: true" -H "NC-Token: $TOKEN" "http://ADDRESS/ocs/v2.php/apps/serverinfo/api/v1/info?format=json"
head -c 300 /tmp/si.txt; echo
```

`HTTP 200` and JSON starting with `{"ocs":{"meta":{"status":"ok"` means address and token are right.

## The instance is down, or "Waiting for the first check"

| What you see | Cause and fix |
|---|---|
| `invalid nextcloud url` | The address is not a full URL. Write it as `https://cloud.example.com` or `http://192.168.1.10:8080`, with the scheme. |
| `status.php returned HTTP 404 ... the URL must be the base address` | The address has a path on it (`/login`, `/index.php`, `/nextcloud` is fine only if Nextcloud really lives there). Use the base address. Test: `curl -s http://ADDRESS/status.php` must print JSON. |
| `status.php did not return JSON (is this a Nextcloud URL?)` | Something else answers: a login page, a proxy error page, a redirect. Check with `curl -sL -o /dev/null -w '%{http_code} %{url_effective}\n' http://ADDRESS/status.php`. A redirect from http to https means you must use the https address. |
| Timeout, `HTTP 000`, connection refused | The agent's machine cannot reach the address. Check the address, the port, a firewall, and that Nextcloud runs (`docker ps`). Remember the test must run on the "checked by" machine. |
| `x509: certificate ...` on an https address | The certificate is not trusted by the agent's machine (self-signed or an internal authority). Install the authority on that machine, or monitor through the internal http address. |
| `HTTP 400` or "Access through untrusted domain" | The address is not in `trusted_domains`. List them with `occ config:system:get trusted_domains`, then add one: `occ config:system:set trusted_domains 3 --value="192.168.1.10:8080"` (use the next free number). |
| `HTTP 502`, `503`, `504` | The proxy in front of Nextcloud cannot reach it, or Nextcloud is overloaded or restarting. Look at the proxy and `docker logs NC`. |
| Down only for short moments | Maintenance mode (`nextcloud_maintenance_mode` = 1), a restart, or a backup that stops the containers. The log shows when. |

## "Only availability is monitored"

The agent reaches Nextcloud but the serverinfo request fails, so users, files, shares, storage and PHP are missing. The agent's log gives the HTTP code:

| `serverinfo returned HTTP ...` | Cause and fix |
|---|---|
| `401` or `403` | Nextcloud does not accept the token. Set it again: `occ app:enable serverinfo`, `occ config:app:set serverinfo token --value "TOKEN"`, and read it back with `occ config:app:get serverinfo token`. Paste the same value into Kluard (**Edit**). Type nothing by hand: a line break in a pasted token makes it fail. |
| `404` | The serverinfo app is not enabled: `occ app:enable serverinfo`. |
| `400` | Untrusted domain, see above. |
| `429` ("Reached maximum delay") | Nextcloud's brute-force protection has blocked the agent's address, usually after refused logins (a wrong password in the login fields, or an old token). See "Blocked by brute-force protection" below. |
| `500` | An error inside Nextcloud. Read its log (see "A huge log line" if `occ log:tail` fails). If it follows a `429`, it is the same blocking. |
| `serverinfo did not return JSON` | A proxy or login page answered. Check the address and that the proxy forwards the `NC-Token` and `OCS-APIRequest` headers. |
| `serverinfo is paused until HH:MM` | The agent stopped asking after a refusal (to avoid making the blocking worse). Fix the cause, then open **Edit** on the instance and **Save** once. That ends the pause. |

Other causes:

- You pasted the token but did not save it, or saved it on another instance.
- Both a token and a login are filled in, and the login password is wrong. The token is used for serverinfo, but the login is used for the deeper checks and the wrong password blocks the address (see the next two sections). Remove the login, or give it a correct app password.
- The agent is older than Kluard. The page then says so; update the agent.

## "Background jobs and the login check cannot be read"

These checks need a Nextcloud **administrator** account. Give the instance a user name and an **app password** (not the normal password). The agent log shows which of these it is:

| Message | Fix |
|---|---|
| No login given | Open **Edit**, fill in "or login": the user name and the app password. Do not tick "remove". |
| `the user name or app password was refused (HTTP 401)` | Wrong user or password, or the app password was removed. Create a new one. |
| `the user must be a Nextcloud administrator ... (HTTP 403)` | The user is not in the `admin` group. Use an administrator. |
| `the Provisioning API is not available ... enable the Provisioning API app` | `occ app:enable provisioning_api`. |
| Two-factor authentication is on for the user | A normal password is refused. An app password works despite it. |
| `paused until HH:MM ... (brute-force protection)` | See "Blocked by brute-force protection". |

Create an app password from the command line (it is shown once):

```bash
occ user:auth-tokens:add admin
```

Or in Nextcloud: **Settings, Security, Devices and sessions, Create new app password**.

## "The login check fails"

The agent logs in over WebDAV and lists the user's files. That touches the web server, PHP, the database and the storage, so it finds failures that `status.php` does not.

| Message | Cause and fix |
|---|---|
| `the WebDAV login was refused (HTTP 401)` | Wrong user or app password. |
| `the WebDAV check returned HTTP 5xx` | Nextcloud fails internally: a full disk, a database that is down, a storage that is missing (an unmounted data directory, an S3 bucket that does not answer). Read Nextcloud's log and `docker logs NC`. |
| `HTTP 403` or `423` | The user is locked, or the instance is in maintenance mode. |
| `HTTP 404` | The user has no home folder yet. Log in as that user once, or use another user. |

## "Background jobs have not run for ..."

Nextcloud's own jobs (clean-up, previews, notifications, mail) are run by cron. The page shows the time since the last run.

- Cron is not set up. Run it every 5 minutes: `*/5 * * * * www-data php -f /var/www/nextcloud/cron.php`. For Docker, run it from the host: `*/5 * * * * docker exec -u www-data NC php -f /var/www/html/cron.php`.
- The mode says "Cron" but nothing runs it. Check under **Administration settings, Basic settings**, and the cron service or container.
- "AJAX" or "Webcron" mode only runs when someone uses the site. Switch to Cron: `occ background:cron`.
- A job is stuck. Look for a long-running `cron.php` process, and for a lock in Redis (a Redis that is down slows pages and jobs).
- "As long as anyone can tell" means cron has never run on this instance.

## Blocked by brute-force protection

Nextcloud delays or blocks an address after repeated refused logins. The agent's address then gets `HTTP 429` ("Reached maximum delay") or strange `500` answers, and all checks that need a login stop. Common causes: a wrong password in the login fields, an old token, a test script with the wrong password.

1. Remove the cause first (fix or remove the login in Kluard), or it will happen again.
2. Reset the address as Nextcloud sees it. For Docker that is often the Docker gateway (such as `172.17.0.1`), and for a local test the machine's own address:

   ```bash
   occ security:bruteforce:reset 192.168.1.10
   occ security:bruteforce:reset 172.17.0.1
   ```

3. Put your monitoring network on the allow list so it is never blocked:

   ```bash
   occ config:app:set bruteForce whitelist_0 --value="192.168.1.0/24"
   occ config:app:set bruteForce whitelist_1 --value="172.16.0.0/12"
   ```

4. Test with the `curl` above (it must give `HTTP 200`), then **Edit** the instance and **Save** once so the agent resumes.

## Certificates

| What you see | Meaning |
|---|---|
| "The certificate is not checked because the address is not https" | Normal for an internal http address. The certificate that visitors see is on the public address. Add the public address as another instance if you want it checked. |
| "The certificate expires in ..." | Fewer than 14 days left. If it renews automatically, the renewal is failing: check the ACME client or proxy log. |
| "The certificate expired ... ago" | Browsers refuse the site. Renew it now. |
| "The certificate is not trusted, or not for this name" | A self-signed certificate, a missing intermediate certificate, or a different name. Fine for an internal instance with its own authority; a real problem on a public one. |

## Missing data ("No data" tiles)

- Users, files, shares, storage, database size and the PHP tiles come from serverinfo and need the token. "Only availability is monitored" explains why they are empty.
- Federated shares sent and received count what serverinfo reports. They stay 0 when the federation apps are not in use.
- **PHP memory limit** shows an infinity sign when PHP has `memory_limit = -1`. **PHP max run time** shows it when the value is 0.
- **PHP upload limit** needs an agent of the same version as Kluard or newer.
- **OPcache** tiles are empty when OPcache is off (`opcache.enable=0`) or cannot be read.
- Last background job, login check, apps and updates need the administrator login (see above).
- "Free data space" is empty on storages that do not report free space (some S3 and network mounts).
- Check the time range at the top of the page. A new instance has no history before its first report.

## The agent

| What you see | Fix |
|---|---|
| "last report" is old, or the host is missing | The agent is stopped or cannot reach Kluard. On the machine: `systemctl status NAME`, then `journalctl -u NAME -n 30`. Check that the machine reaches the `url` in `/etc/kluard-agent/config.json` (port 4318 by default, or your HTTPS address): `curl -s -o /dev/null -w '%{http_code}\n' http://KLUARD:4318/healthz` must print `200`. |
| `kluard rejected /v1/metrics: 401 Unauthorized` | The server does not know the agent's API key. Kluard now lists such agents on the **Hosts** page ("An agent is being refused", with the first 8 characters of the key and where it came from) and writes `agent key refused` to its own log. Put a valid key in `/etc/kluard-agent/config.json` and restart the agent. Keys are created under **Add a machine**; the keys in `KLUARD_API_KEYS` (or the old `LUMEN_API_KEYS`) in `deploy/.env` always work. |
| "The agent that checks this instance is older than Kluard" | Update the agent to the version of Kluard. Newer features need it. |
| `This machine refused ... log paths` | For safety an agent only reads files under `/var/log`, `/var/lib/docker`, `/var/www`, `/srv`, `/mnt` and `/opt`. Change `allowed_log_dirs` in `/etc/kluard-agent/config.json`, or move the file. |
| The log file shows nothing | Check the path, that the agent can read it (`ls -l`), and that the agent was restarted after the change. |

## A huge log line

`occ log:tail` can stop with `Allowed memory size ... exhausted` when one log entry is huge (often a long exception from a failing HTTP call). Read the raw file with long lines cut off instead:

```bash
docker exec NC sh -c 'tail -n 20 /var/www/html/data/nextcloud.log' | cut -c1-300
```

If the file is not there, find it: `occ config:system:get logfile` and `occ config:system:get log_type`. A log entry that large is worth looking into, because the same call fails again and again.

## Mistakes that look like faults

- Running the test on the wrong machine (`HTTP 000`).
- Copying a command with a placeholder still in it (`NC`, `YOUR_TOKEN`, `ADDRESS`).
- Pasting a whole answer, including its explanations, into the shell.
- A trailing space or line break in the token.
- Using the normal password instead of an app password.
- Using the container name `NC` instead of the real one.
- Reading old results: hard-refresh the page (Ctrl+Shift+R) after saving, and wait a minute or two. Checks run every 15 seconds, the deeper ones less often.

## Still stuck?

Collect these three things and send them with your question:

1. The agent log lines with `nextcloud poll problem`.
2. The result of the `curl` test above (the HTTP code and the first 300 characters).
3. The Nextcloud version and how it is installed (Docker, AIO, package).
