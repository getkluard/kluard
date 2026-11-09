# Nextcloud metrics, panels and alerts

What Kluard measures on a Nextcloud instance, where to see it, how to build panels that change colour and which alerts to set up first. For problems, see [TROUBLESHOOTING-NEXTCLOUD.md](TROUBLESHOOTING-NEXTCLOUD.md).

## Where the numbers come from

The agent on the machine that checks the instance collects three kinds of data, and the table below says which metric needs which:

- **status.php** is open to everyone and needs nothing. It gives availability, response time and maintenance mode.
- **serverinfo** needs the serverinfo token, or an administrator login. It gives users, files, shares, sizes, PHP settings and updates.
- **An administrator login with an app password** gives the background jobs, the WebDAV login check, stored data and accounts. Create the app password with `occ user:auth-tokens:add admin` and enter the user and the password under Edit instance.
- **The certificate** is read on https addresses only.

## Where to see the metrics

- **Metrics** in the menu lists every metric that has arrived. Picking a Nextcloud metric shows what it means and sets the right unit. Split by the label `instance` to see one line per instance.
- **Instances**: click an instance for its own page with tiles and charts for everything below. The tiles turn yellow and red by themselves.
- **Dashboards**: build your own panels, or start from the ready-made **Nextcloud health**.

## Build a panel that turns yellow and red

Stat panels (a big number) can change colour with the value:

- Open *Dashboards*, open a dashboard and click *Edit dashboard*, then *+ Add panel*.
- Set *Panel type* to *Stat* and choose the metric, for example `nextcloud_cron_age_seconds`.
- Set *Split by* to `instance` and *Unit* to *Duration*.
- Under *Colour*, choose *at or above* and enter `15m` for yellow and `1h` for red.
- Save the panel, then *Save* the dashboard.

Times take `15m`, `2h` or `14d`, and sizes take `500MB` or `10GB`. For values where low is bad, such as the login check (1 = works) or the days left on a certificate, choose *at or below*. Leave both fields empty for no colour. A tile with several instances colours each number on its own.

## The ready-made dashboard

On *Dashboards*, add **Nextcloud health** from the starter dashboards. It has tiles that turn yellow and red and the charts behind them:

| Tile | Yellow / red |
|---|---|
| Last background job | 15 minutes / 1 hour since it last ran |
| Background job errors | 1 / 10 |
| Login check | red when it fails |
| Certificate valid for | 21 days / 7 days left |
| Nextcloud update, app updates | yellow when one waits (red at 10 apps) |
| Free data space | 20 GB / 5 GB left |
| Response time | 2 s / 5 s |
| Maintenance mode | yellow while it is on |

Change the limits by editing the panel. The dashboard needs the admin login on each instance for the first five rows.

## Alerts to set up first

A panel only shows. To be told, add rules under *Alerts, Rules, Add rule* with the type *metric*, group by `instance`, and set *If there is no data* to *Raise an alert* so you are also told when the agent goes quiet.

| Rule | Why |
|---|---|
| `nextcloud_cron_age_seconds`, highest, > 900, for 5 min | Background jobs have stopped. Notifications, cleanup and sharing quietly stop working. |
| `nextcloud_webdav_ok`, lowest, < 1, for 5 min | Logging in no longer works, even though the page answers. |
| `nextcloud_tls_cert_expiry_seconds`, lowest, < 1209600 | The certificate runs out within 14 days. |
| `nextcloud_up`, lowest, < 1, for 2 min | The instance does not answer. |
| `nextcloud_cron_errors`, highest, > 0 | Nextcloud reports errors in its background jobs. |
| `nextcloud_data_free_bytes`, lowest, < 5000000000 | Less than 5 GB left for the data. |
| `nextcloud_update_available`, highest, >= 1 | A newer Nextcloud exists. Set the importance to low. |

## Every Nextcloud metric

All metrics carry the label `instance` (the name you gave the instance). Other labels are listed per metric. If a metric does not appear in *Metrics*, the column *Needs* says what is missing.

### Availability and status

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_up` | 1 when status.php answers, 0 when it does not. The availability signal. | always (status.php) | instance |
| `nextcloud_status_response_seconds` | How long status.php took to answer. | always (status.php) | instance |
| `nextcloud_maintenance_mode` | 1 while the instance is in maintenance mode. | always (status.php) | instance |
| `nextcloud_needs_db_upgrade` | 1 when Nextcloud waits for a database upgrade (occ upgrade). | always (status.php) | instance |
| `nextcloud_installed` | 1 when Nextcloud reports that it is installed. | always (status.php) | instance |
| `nextcloud_info` | Always 1. Carries the versions as labels, which is what the Instances page shows. | needs a serverinfo token or an admin login | instance, version, php_version, db_type, db_version, update_version |

### Usage and size

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_users` | Number of accounts. | needs a serverinfo token or an admin login | instance |
| `nextcloud_files` | Number of files. | needs a serverinfo token or an admin login | instance |
| `nextcloud_active_users` | Users active in the last 5 minutes, hour and 24 hours. | needs a serverinfo token or an admin login | instance, period (5m, 1h, 24h) |
| `nextcloud_storages` | Number of storages by kind. | needs a serverinfo token or an admin login | instance, type (total, local, home, other) |
| `nextcloud_shares` | Number of shares by kind, including federated shares sent and received. | needs a serverinfo token or an admin login | instance, type (total, user, group, link, link_no_password, federated_sent, federated_received) |
| `nextcloud_data_free_bytes` | Free space where the data directory lives. | needs a serverinfo token or an admin login | instance |
| `nextcloud_database_size_bytes` | Size of the database. | needs a serverinfo token or an admin login | instance |

### Updates

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_apps_installed` | Number of installed apps. | needs a serverinfo token or an admin login | instance |
| `nextcloud_apps_updates_available` | Number of apps that have an update waiting. | needs a serverinfo token or an admin login | instance |
| `nextcloud_update_available` | 1 when a newer Nextcloud version exists. | needs a serverinfo token or an admin login | instance |
| `nextcloud_update_checked_timestamp_seconds` | When Nextcloud last checked for updates (Unix time). | needs a serverinfo token or an admin login | instance |

### PHP

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_php_memory_limit_bytes` | PHP memory_limit. -1 means unlimited and is shown as ∞. | needs a serverinfo token or an admin login | instance |
| `nextcloud_php_max_execution_time_seconds` | PHP max_execution_time. 0 means unlimited and is shown as ∞. | needs a serverinfo token or an admin login | instance |
| `nextcloud_php_upload_max_filesize_bytes` | PHP upload_max_filesize, the largest file one upload may carry. | needs a serverinfo token or an admin login | instance |
| `nextcloud_php_opcache_hit_rate` | Share of PHP requests served from the OPcache, in percent. | needs a serverinfo token or an admin login | instance |
| `nextcloud_php_opcache_used_bytes` | Memory the OPcache uses. | needs a serverinfo token or an admin login | instance |
| `nextcloud_php_opcache_wasted_percent` | Share of the OPcache that is wasted; a high value means it should be reset or made larger. | needs a serverinfo token or an admin login | instance |

### Background jobs and login check

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_cron_age_seconds` | Seconds since the last background job run. Very large if cron has never run. The one to alert on. | needs an admin login with an app password | instance |
| `nextcloud_cron_last_run_timestamp_seconds` | When the background jobs last ran (Unix time). | needs an admin login with an app password | instance |
| `nextcloud_cron_errors` | Number of cron errors Nextcloud reports. | needs an admin login with an app password | instance |
| `nextcloud_cron_mode` | Always 1. The mode is in the label. | needs an admin login with an app password | instance, mode (cron, webcron, ajax) |
| `nextcloud_webdav_ok` | 1 when logging in over WebDAV works, 0 when it does not. | needs an admin login with an app password | instance |
| `nextcloud_webdav_response_seconds` | How long the WebDAV login took (only while it works). | needs an admin login with an app password | instance |

### Accounts and stored data

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_storage_used_bytes` | What all accounts' files take together, as Nextcloud counts them against quota. What billing uses. | needs an admin login with an app password | instance |
| `nextcloud_accounts` | Number of enabled and disabled accounts. | needs an admin login with an app password | instance, state (enabled, disabled) |

### Certificate

| Metric | What it is | Needs | Labels |
|---|---|---|---|
| `nextcloud_tls_cert_expiry_seconds` | Seconds until the TLS certificate expires. Negative once it has expired. | https instances (reads the certificate) | instance |
| `nextcloud_tls_cert_not_after_timestamp_seconds` | When the certificate expires (Unix time). | https instances (reads the certificate) | instance |
| `nextcloud_tls_cert_valid` | 1 when the certificate is trusted and made for this name. | https instances (reads the certificate) | instance |

## Good to know

- The background-job and login metrics are read about once a minute and the certificate every ten minutes, so allow a minute or two after a change.
- If Nextcloud refuses the login, the agent pauses and the metrics stop. Alerts with *Raise an alert* on no data tell you. See the troubleshooting guide.
- `nextcloud_cron_age_seconds` is very large when cron has never run. That is on purpose, so that the alert fires.
- An unlimited PHP memory limit (-1) and run time (0) are kept as they are and shown as ∞.

