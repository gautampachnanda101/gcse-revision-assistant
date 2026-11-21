# Agent instructions

## Project shape

- Single-binary, dependency-free Go application; the UI is embedded from `app.html`.
- Public packs have editable sources in `content/<id>/` and generated output in `data/<id>.js`.
- `subjects.js` is generated from `data/*.js`; never hand-edit it.
- Private material belongs only in `private/` or the user's private pack directory and must never be staged.

## Required validation

After code, content, schema or UI changes:

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go run . validate
```

For pack changes:

```sh
GOWORK=off go run . build content/<id>
```

Cross-compile release-sensitive Go changes for at least Windows:

```sh
GOOS=windows GOARCH=amd64 GOWORK=off go test -c -o /tmp/gcse-win.exe .
```

## Pack and marking rules

- Subject packs are governed by `schema/gcse-revision-pack.schema.json`.
- Preserve stable IDs and `meta.idPrefix`; they protect saved progress.
- Revision subjects and marking schemas are separate. A pack references a marking schema by `markingSchemaId`.
- Label board-derived marking plans as `adaptable` unless wording is directly verified; never present generated guidance as an official mark scheme.
- The marking DSL is consumed by AI agents and must remain compatible with `SCHEME`, `COMMAND`, `MARKS`, `AWARD`, `BAND`, `REQUIRE`, `NEVER`, and `FEEDBACK`.
- Do not copy past papers, mark schemes, textbooks or paid third-party model answers.

## Service and release invariants

- Package-managed service installs must write a concrete, versioned executable path when the platform provides one.
- A package upgrade must ensure an existing service is restarted and the running `/api/info` version matches the installed CLI.
- `gcse-revision doctor` is the supported diagnostic command.
- Homebrew and Scoop post-install behavior must be safe in fresh/headless CI: only modify an existing user service configuration.
- Do not remove or weaken post-release smoke tests for Windows, macOS, Linux, Homebrew and Scoop.

## UI requirements

- Keep light and dark modes complete: content, sidebar, code blocks, status badges and forms must all use theme variables.
- Mark machine-generated feedback and model answers as potentially wrong/unvetted.
- Audio labels must distinguish text-to-speech (`Read prompt aloud`) from recorded audio (`Play recorded audio`).
- Speaking recordings stay local to the browser unless the user explicitly chooses a cloud transcription/provider.

## Git

- Do not stage `private/`, scratch scripts, caches or generated temporary files.
- Commit with a clear conventional commit message.
- Co-author trailer:

```text
Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```
