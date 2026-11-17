# Installing Kluard

Two ways to install the **server**. Pick one and follow it from top to bottom.

| | A. Clone and Docker | B. Release file (`.tar.gz`) |
|---|---|---|
| You get | Kluard, ClickHouse and Elasticsearch, all in Docker, set up by one script | The `kluard` program, started by systemd; ClickHouse in one Docker container |
| Good for | Most people. Fewest steps. | Servers where you do not want to build from source, or want to pin an exact release |
| Needs | Git, Docker (the script installs it), 6 GB RAM (8 GB+ recommended) | Docker for ClickHouse, `curl`, `tar`, systemd |
| Updating | [UPGRADING.md](UPGRADING.md), "Source with Docker" | [UPGRADING.md](UPGRADING.md), "Release file" |

Both need a Linux server with 2+ vCPU and 20 GB+ of local SSD (Rocky Linux, AlmaLinux or RHEL 9, Ubuntu or Debian), a name such as `kluard.example.com` that points to it, and TCP port 4318 reachable from the machines you will monitor (or HTTPS in front, see the last step). Remove it again: [UPGRADING.md](UPGRADING.md), "Uninstalling Kluard".

---

## A. Clone and Docker

**1. Get the source**

```bash
git clone https://github.com/getkluard/kluard.git
cd kluard
```

To install an exact release instead of the newest source, add `git checkout v0.1.4` after `cd kluard`.

**2. Let Elasticsearch have enough memory maps (Linux)**

```bash
sudo sysctl -w vm.max_map_count=262144
echo 'vm.max_map_count=262144' | sudo tee /etc/sysctl.d/99-kluard.conf
```

**3. Run the installer**

```bash
sudo ./install.sh --public-url https://kluard.example.com --tenant main
```

It installs Docker if it is missing, creates the secrets in `deploy/.env`, checks the machine, builds and starts Kluard, ClickHouse and Elasticsearch, waits until they are healthy and prints the address and the `admin` login. Add `--allow-podman-removal` if podman is installed. Use your own address after `--public-url`; it is written into the agent install commands.

**4. Save `deploy/.env`**

It holds `KLUARD_SECRET_KEY`, which encrypts the Nextcloud tokens Kluard stores. Without it they cannot be read again. Copy the file somewhere safe, outside the server.

**5. Check that it runs**

```bash
curl -s http://127.0.0.1:4318/healthz       # prints: ok
cd deploy && docker compose ps               # all three services "running" / "healthy"
```

Continue at **Both ways: the last steps** below.

---

## B. Release file

**1. Download and check the file**

Open the [releases page](https://github.com/getkluard/kluard/releases), pick a version and set it here. Use `arm64` instead of `amd64` on an ARM machine.

```bash
VERSION=v0.1.4
ARCH=amd64
cd ~
curl -fLO https://github.com/getkluard/kluard/releases/download/$VERSION/kluard-server-linux-$ARCH.tar.gz
curl -fLO https://github.com/getkluard/kluard/releases/download/$VERSION/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS      # must say: kluard-server-linux-amd64.tar.gz: OK
```

The file holds the server (`kluard`) and a `dist/` folder with the agents for Linux and Windows. *Add a machine* hands those agents out, so keep `dist/` next to the program.

**2. Put it in place**

```bash
sudo mkdir -p /opt/kluard
sudo tar xzf kluard-server-linux-$ARCH.tar.gz -C /opt/kluard --strip-components=1
sudo useradd --system --home-dir /var/lib/kluard --create-home --shell /usr/sbin/nologin kluard
sudo mkdir -p /var/lib/kluard/backup /etc/kluard
sudo chown -R kluard:kluard /var/lib/kluard
ls /opt/kluard                                     # dist  kluard  LICENSE  README.md
```

**3. Start ClickHouse (one Docker container)**

Kluard keeps its telemetry in ClickHouse. If Docker is not installed: `curl -fsSL https://get.docker.com | sudo sh`.

```bash
sudo mkdir -p /etc/kluard/clickhouse
sudo curl -fsSL -o /etc/kluard/clickhouse/kluard.xml https://raw.githubusercontent.com/getkluard/kluard/$VERSION/deploy/clickhouse/kluard.xml
CH_PASSWORD=$(openssl rand -hex 24)
sudo docker run -d --name kluard-clickhouse --restart unless-stopped \
  -e CLICKHOUSE_USER=default -e CLICKHOUSE_PASSWORD="$CH_PASSWORD" \
  -v kluard-chdata:/var/lib/clickhouse \
  -v /etc/kluard/clickhouse/kluard.xml:/etc/clickhouse-server/config.d/kluard.xml:ro,z \
  --ulimit nofile=262144:262144 \
  -p 127.0.0.1:8123:8123 \
  clickhouse/clickhouse-server:26.8.37
curl -s http://127.0.0.1:8123/ping                 # prints: Ok.
```

The `kluard.xml` file switches off ClickHouse's own diagnostic logs, which otherwise grow to tens of gigabytes. The port is only open on the machine itself.

**4. Write the settings**

This creates the first administrator `admin` with a password it generates, and the key that encrypts stored tokens. Use your own address in `KLUARD_PUBLIC_URL`.

```bash
sudo tee /etc/kluard/kluard.env >/dev/null <<ENV
KLUARD_PUBLIC_URL=https://kluard.example.com
KLUARD_CLICKHOUSE_URL=http://127.0.0.1:8123
KLUARD_CLICKHOUSE_PASSWORD=$CH_PASSWORD
KLUARD_DATA_DIR=/var/lib/kluard
KLUARD_BACKUP_DIR=/var/lib/kluard/backup
KLUARD_DIST_DIR=/opt/kluard/dist
KLUARD_SECRET_KEY=$(openssl rand -hex 32)
KLUARD_ADMIN_USER=admin
KLUARD_ADMIN_PASSWORD=$(openssl rand -hex 12)
KLUARD_ADMIN_TENANT=main
ENV
sudo chmod 600 /etc/kluard/kluard.env
sudo grep -E '^KLUARD_ADMIN_(USER|PASSWORD)=' /etc/kluard/kluard.env     # your login: write the password down
```

Without `KLUARD_ELASTICSEARCH_URL` Kluard keeps users, keys and dashboards in a JSON file in `/var/lib/kluard`, which is fine for one server. To use Elasticsearch instead, add its address and login to this file (see the README, *Configuration*).

**5. Run it as a service**

```bash
sudo tee /etc/systemd/system/kluard.service >/dev/null <<'UNIT'
[Unit]
Description=Kluard
After=network-online.target docker.service
Wants=network-online.target

[Service]
User=kluard
EnvironmentFile=/etc/kluard/kluard.env
ExecStart=/opt/kluard/kluard
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=full
ReadWritePaths=/var/lib/kluard

[Install]
WantedBy=multi-user.target
UNIT
sudo systemctl daemon-reload
sudo systemctl enable --now kluard
```

**6. Check that it runs**

```bash
curl -s http://127.0.0.1:4318/healthz              # prints: ok
sudo journalctl -u kluard -n 20 --no-pager         # no errors
```

**7. Save the settings**

Copy `/etc/kluard/kluard.env` somewhere safe, outside the server. It holds `KLUARD_SECRET_KEY`; without it the stored Nextcloud tokens cannot be read again.

Continue at **Both ways: the last steps**.

---

## Both ways: the last steps

**Put HTTPS in front.** API keys travel in headers and Nextcloud tokens are sent to the agents, so use HTTPS. Put Nginx or Caddy in front of port 4318 and make Kluard listen on the machine only:

- Way A: set `KLUARD_BIND=127.0.0.1` in `deploy/.env`, then `cd deploy && docker compose up -d`.
- Way B: add `KLUARD_ADDR=127.0.0.1:4318` to `/etc/kluard/kluard.env`, then `sudo systemctl restart kluard`.

Ports that Docker publishes usually bypass firewalld, so do not rely on the firewall alone.

**Log in.** Open the address, sign in as `admin` with the password from the installer (way A: also in `deploy/.env`, way B: in `/etc/kluard/kluard.env`) and change it.

**Add the first machine.** Open **Add a machine**, choose Linux, Windows or Docker and paste the command it shows on the server you want to monitor. The machine appears under **Hosts** within a minute.

**Add a Nextcloud instance.** Create a token on the Nextcloud server and an app password for an administrator, then open **Instances, Add instance**. The commands are in the README, *Nextcloud monitoring*.

**Turn on alerts.** Under **Alerts**, add a channel, press **Test**, then add the starter rules.

## If something does not work

| Symptom | Check |
|---|---|
| `healthz` does not answer | Way A: `cd deploy && docker compose logs kluard`. Way B: `sudo journalctl -u kluard -n 50`. |
| Way B: "connection refused" to ClickHouse | `sudo docker ps` shows `kluard-clickhouse`; `curl -s http://127.0.0.1:8123/ping` prints `Ok.`; the password in `kluard.env` is the one you used in step 3. |
| Way A: Elasticsearch keeps restarting | `sysctl vm.max_map_count` must print `262144` (step 2). |
| Cannot log in | Way A: the login is in `deploy/.env`. Way B: in `/etc/kluard/kluard.env`. The first administrator is created only when there is no user yet. |
| The agent command does not work from another machine | The address in `KLUARD_PUBLIC_URL` must be reachable from that machine, and port 4318 (or your HTTPS port) must be open. |
