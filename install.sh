#!/usr/bin/env bash
# Kluard one-step server installer (Rocky Linux / AlmaLinux / RHEL 9 and Ubuntu / Debian; also works on any host that already has Docker).
#
#   sudo ./install.sh [--public-url https://kluard.example.com] [--tenant main] [--with-agent] [--allow-podman-removal] [--force]
#
# Safe to re-run to upgrade: existing secrets and the address already set in deploy/.env are kept
# (the address only changes if you pass --public-url).
#
# Installs Docker if missing, generates secrets, starts Kluard + ClickHouse, waits until healthy, and prints
# the ready-to-paste commands for enrolling Linux and Windows agents.
set -euo pipefail
cd "$(dirname "$0")"
die() { echo "error: $*" >&2; exit 1; }
PUBLIC_URL=""; TENANT="main"; WITH_AGENT=0; FORCE=0; DOCKER_FLAGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --public-url) [ $# -ge 2 ] || die "--public-url needs a value"; PUBLIC_URL="${2%/}"; shift 2;;
    --tenant) [ $# -ge 2 ] || die "--tenant needs a value"; TENANT="$2"; shift 2;;
    --with-agent) WITH_AGENT=1; shift;;
    --allow-podman-removal) DOCKER_FLAGS+=(--allow-podman-removal); shift;;
    --force) FORCE=1; shift;;
    -h|--help) sed -n '2,11p' "$0"; exit 0;;
    *) die "unknown option: $1";;
  esac
done
[ "$(id -u)" -eq 0 ] || die "run as root: sudo ./install.sh"

echo "==> 1/5 Docker"
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  . /etc/os-release
  case "$ID" in
    rocky|almalinux|rhel|centos) bash deploy/install-rhel-family.sh "${DOCKER_FLAGS[@]}";;
    ubuntu|debian) bash deploy/install-debian-family.sh "${DOCKER_FLAGS[@]}";;
    *) die "Docker with the compose plugin is required; automatic install supports Rocky/Alma/RHEL and Ubuntu/Debian";;
  esac
else echo "Docker already installed."; fi

echo "Host setting for Elasticsearch: vm.max_map_count=262144"
sysctl -w vm.max_map_count=262144
echo 'vm.max_map_count=262144' | tee /etc/sysctl.d/99-kluard.conf

echo "==> 2/5 Secrets"
NEW_ENV=0
if [ ! -f deploy/.env ]; then bash deploy/gen-env.sh "$TENANT" | tee /tmp/kluard-genenv.$$ >/dev/null; NEW_ENV=1; fi
# The address in deploy/.env is yours: it is only changed when you pass --public-url. Re-running the installer to
# upgrade must never replace it (this machine may use a different address than the others you run).
EXISTING_URL="$(grep '^KLUARD_PUBLIC_URL=' deploy/.env 2>/dev/null | head -n1 | cut -d= -f2- || true)"
if [ -n "$PUBLIC_URL" ]; then
  if [ -n "$EXISTING_URL" ] && [ "$EXISTING_URL" != "$PUBLIC_URL" ]; then echo "Changing KLUARD_PUBLIC_URL from $EXISTING_URL to $PUBLIC_URL (you passed --public-url)."; fi
elif [ -n "$EXISTING_URL" ]; then
  PUBLIC_URL="$EXISTING_URL"
  echo "Keeping the existing KLUARD_PUBLIC_URL=$PUBLIC_URL (pass --public-url to change it)."
else
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"; PUBLIC_URL="http://${IP:-localhost}:4318"
  echo "No --public-url given and none set yet, using $PUBLIC_URL (plain HTTP: put a TLS proxy in front before production)."
fi
if [ "$PUBLIC_URL" != "$EXISTING_URL" ]; then
  if grep -q '^KLUARD_PUBLIC_URL=' deploy/.env; then sed -i "s#^KLUARD_PUBLIC_URL=.*#KLUARD_PUBLIC_URL=$PUBLIC_URL#" deploy/.env; else echo "KLUARD_PUBLIC_URL=$PUBLIC_URL" >> deploy/.env; fi
fi
rm -f /tmp/kluard-genenv.$$
if ! grep -q '^KLUARD_ADMIN_PASSWORD=' deploy/.env; then
  printf 'KLUARD_ADMIN_USER=admin\nKLUARD_ADMIN_PASSWORD=%s\nKLUARD_ADMIN_TENANT=%s\n' "$(openssl rand -hex 12)" "${TENANT:-main}" >> deploy/.env
fi
grep -q '^ELASTIC_PASSWORD=' deploy/.env || printf 'ELASTIC_PASSWORD=%s\n' "$(openssl rand -hex 16)" >> deploy/.env
grep -q '^KLUARD_SECRET_KEY=' deploy/.env || printf 'KLUARD_SECRET_KEY=%s\n' "$(openssl rand -hex 32)" >> deploy/.env
ADMIN_USER="$(grep '^KLUARD_ADMIN_USER=' deploy/.env | cut -d= -f2-)"; ADMIN_PW="$(grep '^KLUARD_ADMIN_PASSWORD=' deploy/.env | cut -d= -f2-)"

echo "==> 3/5 Preflight"
if ! bash deploy/preflight.sh; then [ "$FORCE" = 1 ] || die "preflight failed (fix the FAIL lines, or re-run with --force)"; fi

echo "==> 4/5 Starting Kluard"
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
for i in $(seq 1 60); do
  curl -fsS -m 3 http://127.0.0.1:4318/healthz >/dev/null 2>&1 && break
  [ "$i" = 60 ] && { docker compose -f deploy/docker-compose.yml logs --tail 30; die "Kluard did not become healthy in 3 minutes"; }
  sleep 3
done

KEY="$(grep '^KLUARD_API_KEYS=' deploy/.env | cut -d= -f2- | cut -d, -f1 | cut -d: -f1)"
if [ "$WITH_AGENT" = 1 ]; then
  echo "==> 5/5 Agent on this server"
  curl -fsSL http://127.0.0.1:4318/install/agent.sh | sh -s -- --key "$KEY" --url http://127.0.0.1:4318
else echo "==> 5/5 Done"; fi

cat <<MSG

Kluard is running.
  Web UI:   $PUBLIC_URL/
  Log in:   $ADMIN_USER  /  $ADMIN_PW   (also in deploy/.env; change the password in the UI)
  Agent key: $KEY   (tenant: $TENANT; you can also create keys in the UI under "Add a machine")

Enrol a Linux machine (as root):
  curl -fsSL $PUBLIC_URL/install/agent.sh | sudo sh -s -- --key $KEY

Enrol a Windows machine (elevated PowerShell):
  & ([scriptblock]::Create((irm $PUBLIC_URL/install/agent.ps1))) -Key $KEY

The same commands are shown in the UI under "Add a machine".
MSG
