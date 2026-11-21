#!/bin/sh
# GCSE Revision — one-command install for macOS and Linux:
#   curl -fsSL https://raw.githubusercontent.com/gautampachnanda101/gcse-revision-assistant/main/install.sh | sh
# Installs a single self-contained program to ~/.local/bin, then starts it (set GCSE_REVISION_NO_RUN=1 to skip).
set -eu

REPO="${GCSE_REVISION_REPO:-gautampachnanda101/gcse-revision-assistant}"
BIN_DIR="${GCSE_REVISION_BIN_DIR:-$HOME/.local/bin}"
NAME=gcse-revision

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "Unsupported OS: $(uname -s). On Windows run: irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

asset="$NAME-$os-$arch"
base="https://github.com/$REPO/releases/latest/download"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch() { if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi; }

echo "Downloading $asset…"
fetch "$base/$asset" "$tmp/$NAME"
fetch "$base/checksums.txt" "$tmp/checksums.txt"
want="$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$NAME" | cut -d' ' -f1)"; else got="$(shasum -a 256 "$tmp/$NAME" | cut -d' ' -f1)"; fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then echo "Checksum check failed — download corrupted? Try again." >&2; exit 1; fi

mkdir -p "$BIN_DIR"
chmod +x "$tmp/$NAME"
mv "$tmp/$NAME" "$BIN_DIR/$NAME"
echo "Installed $BIN_DIR/$NAME"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Tip: add $BIN_DIR to your PATH to run '$NAME' from anywhere, e.g.:"
     echo "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.profile" ;;
esac

if [ "${GCSE_REVISION_NO_RUN:-0}" != "1" ]; then
  echo "Starting GCSE Revision… (next time just run: $NAME)"
  if [ -r /dev/tty ]; then exec "$BIN_DIR/$NAME" </dev/tty; else exec "$BIN_DIR/$NAME"; fi
fi
