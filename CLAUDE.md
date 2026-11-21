# CLAUDE.md

This file is the project guide for Claude and other terminal coding agents. Follow `AGENTS.md` as the complete repository policy.

## Quick start

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go run . validate
```

Use `go run . build content/<id>` after editing a source pack.

## Critical constraints

- Never commit private packs or any file under `private/`.
- `data/*.js` is generated; edit `content/<id>/` and rebuild.
- Keep the open pack schema and import/export round trips compatible.
- A marking schema is separate from a revision pack and must remain reusable across subjects.
- AI output is machine-generated, may be wrong, and is not an official exam-board mark.
- Package upgrades must leave the service running at the same version as the installed CLI.
- Use actual browser/system capabilities: Apple Intelligence is detected via the embedded bridge; Windows Copilot itself is not treated as an API.

## Work style

- Prefer stdlib Go and existing repository patterns.
- Make surgical changes and add focused regression tests.
- Validate actual release behavior, not only compilation: service restart, `doctor`, Homebrew/Scoop smoke tests, and UI version/marking-plan indicators.
- Do not claim a release is complete until CI and the package smoke matrix are green.
