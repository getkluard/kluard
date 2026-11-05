#!/bin/sh
# Runs INSIDE the agent container (alpine): downloads the checksum-verified agent and starts it.
# Configuration comes from KLUARD_AGENT_* environment variables.
set -eu
KLUARD_URL="${KLUARD_AGENT_URL:-__KLUARD_URL__}"
case "$(uname -m)" in
  x86_64) ARCH=amd64;; aarch64) ARCH=arm64;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1;;
esac
NAME="kluard-agent-linux-$ARCH"
wget -qO /tmp/kluard-agent "$KLUARD_URL/download/$NAME"
wget -qO /tmp/SHA256SUMS "$KLUARD_URL/download/SHA256SUMS"
WANT="$(grep " $NAME\$" /tmp/SHA256SUMS | cut -d' ' -f1)"
[ -n "$WANT" ] || { echo "no checksum for $NAME" >&2; exit 1; }
echo "$WANT  /tmp/kluard-agent" | sha256sum -c - >/dev/null || { echo "checksum mismatch, refusing to start" >&2; exit 1; }
chmod +x /tmp/kluard-agent
# lets the Kluard UI update this agent with one click: it downloads the new version, checks it and restarts itself in place
export KLUARD_AGENT_SELF_UPDATE=exec:/tmp
exec /tmp/kluard-agent
