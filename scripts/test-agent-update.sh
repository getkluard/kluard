#!/usr/bin/env bash
# Tests the agent installer's --update and the root-owned update helper (apply-update.sh): that settings are kept, that only
# what the server offers is installed and only if its checksum matches, and that a version that does not stay up is
# replaced by the one before. Uses a fake server and temp directories; touches nothing else; works as any user.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d)"; trap 'kill $SRV 2>/dev/null || true; rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/dist" "$T/inst/bin" "$T/inst/etc" "$T/inst/lib" "$T/inst/state" "$T/inst/systemd"
REAL_ID="$(command -v id)"
printf '#!/bin/sh\n[ "$1" = "-u" ] && { echo 0; exit 0; }\nexec %s "$@"\n' "$REAL_ID" > "$T/bin/id"; chmod +x "$T/bin/id"
fail=0; ok() { echo "PASS $1"; }; bad() { echo "FAIL $1"; fail=1; [ -f "$T/last.log" ] && sed 's/^/    | /' "$T/last.log" | tail -15; }
ARCH=amd64; case "$(uname -m)" in aarch64|arm64) ARCH=arm64;; esac
NAME="kluard-agent-linux-$ARCH"
publish() { # $1 = version the fake agent claims to be
  printf '#!/bin/sh\n[ "${1:-}" = "-version" ] && { echo %s; exit 0; }\nexit 0\n' "$1" > "$T/dist/$NAME"
  (cd "$T/dist" && sha256sum "$NAME" > SHA256SUMS)
}
publish src-v1
PORT=$((20000 + RANDOM % 20000))
(cd "$T/dist" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >/dev/null 2>&1) & SRV=$!
for _ in $(seq 1 50); do curl -fs "http://127.0.0.1:$PORT/download/x" >/dev/null 2>&1 && break; ln -sfn . "$T/dist/download"; curl -fsI "http://127.0.0.1:$PORT/download/SHA256SUMS" >/dev/null 2>&1 && break; sleep 0.1; done
URL="http://127.0.0.1:$PORT"
E="KLUARD_NO_SERVICE=1 KLUARD_TEST_UPDATER=1 KLUARD_BIN_DIR=$T/inst/bin KLUARD_CONF_DIR=$T/inst/etc KLUARD_LIB_DIR=$T/inst/lib KLUARD_STATE_DIR=$T/inst/state KLUARD_SYSTEMD_DIR=$T/inst/systemd"
inst() { (cd "$T" && env $E PATH="$T/bin:$PATH" sh "$ROOT/internal/install/agent.sh" --url "$URL" "$@" >"$T/last.log" 2>&1); }
agentv() { "$T/inst/bin/kluard-agent" -version; }
helper() { (env KLUARD_APPLY_RESTART_CMD=true KLUARD_APPLY_CHECK_CMD="${CHECK:-true}" KLUARD_APPLY_WAIT=0 PATH="$T/bin:$PATH" sh "$T/inst/lib/apply-update.sh" >"$T/last.log" 2>&1); }

inst --key klu_testkey --logs '/var/log/*.log' && [ "$(agentv)" = src-v1 ] && grep -q '"api_key": "klu_testkey"' "$T/inst/etc/config.json" && ok "first install: the agent, its config" || bad "first install"
[ -x "$T/inst/lib/apply-update.sh" ] && grep -q "BIN_DIR=\"$T/inst/bin\"" "$T/inst/lib/apply-update.conf" && grep -q "PathExists=$T/inst/state/update-requested" "$T/inst/systemd/kluard-agent-update.path" && grep -q "ExecStart=$T/inst/lib/apply-update.sh" "$T/inst/systemd/kluard-agent-update.service" && ok "the update helper and its systemd units are installed" || bad "helper files"

# --update: new binary, same settings, no key
cp "$T/inst/etc/config.json" "$T/config.before"
publish src-v2
inst --update && [ "$(agentv)" = src-v2 ] && cmp -s "$T/inst/etc/config.json" "$T/config.before" && ok "--update installs the new version and leaves the config exactly as it was" || bad "--update"
inst --update --key klu_other && r=0 || r=$?; { [ $r -ne 0 ] && grep -q "keeps the existing settings and key" "$T/last.log" && cmp -s "$T/inst/etc/config.json" "$T/config.before"; } && ok "--update with a key is refused (it would not change the key, which is confusing)" || bad "--update --key"
(cd "$T" && env KLUARD_NO_SERVICE=1 KLUARD_BIN_DIR="$T/none/bin" KLUARD_CONF_DIR="$T/none/etc" PATH="$T/bin:$PATH" sh "$ROOT/internal/install/agent.sh" --url "$URL" --update >"$T/last.log" 2>&1) && bad "--update with nothing installed" || { grep -q "no agent installed here" "$T/last.log" && ok "--update with nothing installed says so and does nothing"; }

# the helper
publish src-v3
echo "src-v3" > "$T/inst/state/update-requested"
CHECK=true helper && [ "$(agentv)" = src-v3 ] && [ ! -e "$T/inst/state/update-requested" ] && [ "$("$T/inst/bin/kluard-agent.previous" -version)" = src-v2 ] && grep -q "installed src-v3 (was src-v2)" "$T/last.log" && ok "the helper installs what the server offers, removes the request first, and keeps the previous version" || bad "helper happy path"
CHECK=true helper && [ "$(agentv)" = src-v3 ] && ok "without a request the helper does nothing" || bad "no request"
echo x > "$T/inst/state/update-requested"; CHECK=true helper && grep -q "nothing to do" "$T/last.log" && ok "a request when the agent already has the server's version changes nothing (no restart)" || bad "already current"
echo '../../../bin/sh; rm -rf /' > "$T/inst/state/update-requested"; publish src-v4
CHECK=true helper && [ "$(agentv)" = src-v4 ] && ok "what is written in the request does not matter: the helper installs what the server offers and nothing else" || bad "request content ignored"

# a corrupt download is never installed
publish src-v5; printf 'garbage' >> "$T/dist/$NAME"   # the checksum list no longer matches the file
echo x > "$T/inst/state/update-requested"
if CHECK=true helper; then bad "corrupt download"; else grep -q "checksum mismatch" "$T/last.log" && [ "$(agentv)" = src-v4 ] && ok "a download that does not match the server's checksum is refused and nothing is changed" || bad "corrupt download message"; fi
# a download that is not a program for this machine is never installed
printf 'not a program' > "$T/dist/$NAME"; (cd "$T/dist" && sha256sum "$NAME" > SHA256SUMS)
echo x > "$T/inst/state/update-requested"
if CHECK=true helper; then bad "not a program"; else grep -q "does not run on this machine" "$T/last.log" && [ "$(agentv)" = src-v4 ] && ok "a file that does not run is refused before anything is replaced" || bad "not a program message"; fi
# a new version that does not stay up is replaced by the one before
publish src-v6
echo x > "$T/inst/state/update-requested"
if CHECK=false helper; then bad "rollback"; else grep -q "did not stay up; the previous version (src-v4) was put back" "$T/last.log" && [ "$(agentv)" = src-v4 ] && ok "a new version that does not start is rolled back to the previous one, and it is logged" || bad "rollback message"; fi
# a config the helper cannot read
sed -i 's#"url": "[^"]*"#"url": "ftp://evil.example.com/x y"#' "$T/inst/etc/config.json"
echo x > "$T/inst/state/update-requested"
if CHECK=true helper; then bad "bad url"; else grep -q "cannot read a valid Kluard address" "$T/last.log" && ok "an unusable address in the config stops the helper" || bad "bad url message"; fi
# a server that is down
cp "$T/config.before" "$T/inst/etc/config.json"; sed -i "s#\"url\": \"[^\"]*\"#\"url\": \"http://127.0.0.1:1\"#" "$T/inst/etc/config.json"
echo x > "$T/inst/state/update-requested"
if CHECK=true helper; then bad "server down"; else grep -q "download from http://127.0.0.1:1 failed" "$T/last.log" && [ "$(agentv)" = src-v4 ] && ok "a server that cannot be reached leaves the agent as it is" || bad "server down message"; fi

# an agent from before the rename (Lumen) is moved to the new names with its settings and key; nothing is left behind
publish src-v7
M="$T/mig"; mkdir -p "$M/bin" "$M/etc-old" "$M/lib-old" "$M/state-old" "$M/etc-new" "$M/lib-new" "$M/state-new" "$M/systemd"
printf '{"url": "%s", "api_key": "lmn_oldkey", "interval_seconds": 15}\n' "$URL" > "$M/etc-old/config.json"
printf '#!/bin/sh\necho src-old\n' > "$M/bin/lumen-agent"; chmod +x "$M/bin/lumen-agent"
EM="KLUARD_NO_SERVICE=1 KLUARD_BIN_DIR=$M/bin KLUARD_CONF_DIR=$M/etc-new KLUARD_LIB_DIR=$M/lib-new KLUARD_STATE_DIR=$M/state-new KLUARD_SYSTEMD_DIR=$M/systemd KLUARD_LEGACY_CONF_DIR=$M/etc-old KLUARD_LEGACY_LIB_DIR=$M/lib-old KLUARD_LEGACY_STATE_DIR=$M/state-old"
(cd "$T" && env $EM PATH="$T/bin:$PATH" sh "$ROOT/internal/install/agent.sh" --url "$URL" --update >"$T/last.log" 2>&1) \
 && grep -q '"api_key": "lmn_oldkey"' "$M/etc-new/config.json" && [ ! -e "$M/etc-old" ] && [ ! -e "$M/bin/lumen-agent" ] && [ "$("$M/bin/kluard-agent" -version)" = src-v7 ] \
 && ok "an agent from before the rename is moved to the new names, keeping its key and settings, and the old files are gone" || bad "migration from the old names"
exit $fail
