#!/bin/sh
# One-time: create the content-signing key pair.
#  - private key → GitHub Actions secret CONTENT_SIGNING_KEY (+ a backup only you can read)
#  - public key  → signing.go (compiled into the app)
# Run from the repo root:  sh scripts/setup-signing.sh
set -eu
REPO="${GCSE_REVISION_REPO:-gautampachnanda101/gcse-revision-assistant}"
DIR="${XDG_CONFIG_HOME:-$HOME/.config}/gcse-revision"
mkdir -p "$DIR"; chmod 700 "$DIR"
KEYFILE="$DIR/content-signing-keypair.txt"
if [ -e "$KEYFILE" ]; then echo "Key already exists at $KEYFILE — refusing to overwrite." >&2; exit 1; fi
umask 077
GOWORK=off go run . keygen > "$KEYFILE"
PUB=$(grep '^PUBLIC' "$KEYFILE" | awk '{print $NF}')
grep '^PRIVATE' "$KEYFILE" | awk '{print $NF}' | tr -d '\n' | gh secret set CONTENT_SIGNING_KEY --repo "$REPO"
sed -i.bak "s|^var contentPubKey = \".*\"|var contentPubKey = \"$PUB\"|" signing.go && rm -f signing.go.bak
echo "Done. Secret CONTENT_SIGNING_KEY set on $REPO; backup at $KEYFILE (keep it safe, never commit it)."
echo "Public key written to signing.go: $PUB"
