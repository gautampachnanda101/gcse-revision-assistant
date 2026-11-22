#!/bin/sh
# Regenerates docs/diagrams/*.svg from the Mermaid sources in docs/diagrams/src/.
# The SVGs are committed so diagrams show everywhere: GitHub, Backstage (no add-on needed),
# MkDocs, and any Markdown viewer or browser extension.
# Requires Docker. Run from the repository root:  sh scripts/render-diagrams.sh
set -eu
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/docs/diagrams":/data --entrypoint sh minlag/mermaid-cli -c '
  cd /data/src
  for f in *.mmd; do
    /home/mermaidcli/node_modules/.bin/mmdc -q -p /puppeteer-config.json -c config.json -b white \
      -i "$f" -o "../${f%.mmd}.svg"
    echo "rendered docs/diagrams/${f%.mmd}.svg"
  done'
