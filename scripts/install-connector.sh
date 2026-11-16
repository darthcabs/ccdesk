#!/bin/sh
# Install the Claude Code ACP connector (claude-agent-acp) that ccdesk talks to.
# Downloads a pinned release from GitHub, verifies its SHA-256, and installs it to
#   $PREFIX/claude-agent-acp/<version>   with a "current" symlink.
set -eu

VERSION="v0.107.0"
PREFIX="${PREFIX:-$HOME/.local/lib}"

case "$(uname -m)" in
  x86_64|amd64)  ASSET="linux-x64";   SHA256="768f0c910587b8c2a8c3a96aedd5a8876128277f6d633bbcd52092b229e056a9" ;;
  aarch64|arm64) ASSET="linux-arm64"; SHA256="b63b7e5164a0585bdc4837943238170f156daed0d3a9effb093f7d93da206d7d" ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

URL="https://github.com/agentclientprotocol/claude-agent-acp/releases/download/$VERSION/claude-agent-acp-$ASSET.tar.gz"
DEST="$PREFIX/claude-agent-acp/$VERSION"

if [ -x "$DEST/claude-agent-acp" ]; then
  echo "claude-agent-acp $VERSION is already installed in $DEST"
else
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  echo "Downloading claude-agent-acp $VERSION ($ASSET)..."
  curl -fL --progress-bar -o "$tmp/acp.tar.gz" "$URL"
  echo "$SHA256  $tmp/acp.tar.gz" | sha256sum -c - >/dev/null || {
    echo "Checksum mismatch: refusing to install." >&2; exit 1; }
  mkdir -p "$DEST"
  tar -xzf "$tmp/acp.tar.gz" -C "$DEST"
  echo "Installed to $DEST"
fi
ln -sfn "$VERSION" "$PREFIX/claude-agent-acp/current"
echo "Linked $PREFIX/claude-agent-acp/current -> $VERSION"
