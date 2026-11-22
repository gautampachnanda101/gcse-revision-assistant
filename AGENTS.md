# AGENTS.md — Working in gcse-revision-assistant

These instructions apply to every coding agent and every tool. They are not
end-user documentation. `AGENTS.md` is the source of truth for agent rules
and workflow. The implementation in the checked-in code, schema, generated
packs and tests is the source of truth for runtime behavior; if documentation
and code disagree, do not guess—verify the code and update stale documentation.

## 1. Ground every report in evidence

- Base claims only on commands, files, logs, screenshots, or measured results
  from the current session.
- State what was not verified. Distinguish "I read", "the test reported", and
  "my inference"; never present a model judgment as a human review.
- Report failures and unresolved uncertainty with the result. Do not describe
  a change as done while a required check remains unrun or failing.
- When package/service behavior is involved, verify the running process version,
  not only the installed executable.

## 2. Definition of Done

For every code or UI change, run the applicable checks:

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go run . validate
```

Also:

- Cross-compile release-sensitive Go changes:
  `GOOS=windows GOARCH=amd64 GOWORK=off go test -c -o /tmp/gcse-win.exe .`
- For pack edits: `GOWORK=off go run . build content/<id>`.
- For UI changes: open the real app page and verify the affected flow in
  light and dark modes, including mobile layout where relevant.
- For AI marking: show the exact marking schema used and verify the prompt
  passes DSL + parsed rules, not remembered board guidance.
- For audio: distinguish recorded audio from browser text-to-speech. Verify
  play, voice selection, speed, and clear labels when the browser exposes no
  matching voices.
- For speaking questions: verify microphone permission, record, stop,
  playback, delete, and transcript handling. Never claim pronunciation was
  marked unless a verified speech-scoring model exists.
- For package/service changes: verify:
  - `gcse-revision version`
  - `gcse-revision doctor`
  - service manager status
  - `/api/info` running version equals the CLI version
  - an actual Homebrew/Scoop upgrade path, when release scope allows
- Confirm CI green on the exact commit:
  `gh run list --branch main --limit 1 --json status,conclusion,headSha`.
- Confirm post-release smoke jobs for Windows, macOS, Linux, Homebrew and
  Scoop before reporting the release complete.

If any item is not applicable, say why. If it is applicable but unavailable,
state the gap explicitly instead of implying the task is fully verified.

## 3. Hard stops

Pause and get explicit approval before:

- force-pushing, rewriting history, deleting tags, or rewriting releases;
- `git reset --hard`, `git clean -f`, broad recursive deletion, or deleting
  user/private data;
- committing secrets, credentials, `.env` files, private packs, or school
  material without redistribution rights;
- weakening/removing CI, signing, validation, privacy, AI disclaimer, or
  copyright checks;
- treating a third-party archive or remembered exam-board rule as authoritative
  without an official source and provenance note;
- calling the consumer Windows assistant, Apple Intelligence, or another vendor API as supported
  without a verified documented integration.

## 4. Repository map

- `app.html`: embedded single-file UI and browser logic.
- `*.go`: stdlib-only CLI, server, pack builder, import/export and AI manager.
- `apple_ai.go` + `native/`: embedded Apple Foundation Models bridge.
- `content/<id>/`: source packs; edit these, then build.
- `data/<id>.js`: generated packs; never hand-edit.
- `subjects.js`: generated manifest; never hand-edit.
- `schema/gcse-revision-pack.schema.json`: open pack/marking schema.
- `private/`: gitignored personal material; never stage.
- `.github/workflows/release.yml`: releases plus package smoke tests.

## 5. Pack and marking invariants

- Preserve stable IDs and `meta.idPrefix`; saved progress depends on them.
- One module's questions must remain in stable positional order unless a data
  migration is deliberately implemented.
- A revision subject and a marking schema are separate reusable objects.
- Mark board-derived schemas `adaptable` unless every descriptor was directly
  verified from a current official mark scheme.
- AI receives the configured DSL and parsed rules. Do not replace them with
  model memory or generic "examiner knowledge".
- Do not reproduce past papers, mark schemes, textbooks or paid model answers.
- Model answers, AI drafts and AI marking are indicative and unvetted.

## 6. Service ownership rules

- Standalone installers own their service definitions:
  - macOS: LaunchAgent
  - Linux: systemd user unit
  - Windows: Task Scheduler
- Homebrew-managed installs use Homebrew's `service do` and
  `sh.brew.gcse-revision`; CLI commands must detect that label and delegate
  lifecycle operations to `brew services`.
- Do not assume a package post-install hook can safely control launchd. Package
  sandboxing and process-tree termination can block or kill replacement
  helpers.
- Running package-managed services must self-detect an installed package
  version mismatch and replace the service from the new binary.
- `doctor` must compare CLI and `/api/info` versions and report a mismatch with
  the corrective command.

## 7. UI and AI rules

- Light/dark coverage includes sidebar, code/pre blocks, forms, controls,
  badges, AI/marking panels and mobile navigation.
- Status text must describe actual state; do not combine "offline" with "AI
  disconnected" when only one is true.
- The question bank displays the active marking-plan name and status.
- After AI feedback, expose the exact marking plan sent to the model for
  inspection.
- Browser text-to-speech labels say "Read prompt aloud (computer voice)" and
  distinguish it from included recorded audio.
- System voice lists contain only voices detected for the configured language.
- Speaking recording is local to the tab. Browser transcription may be remote;
  say so, and require the user to review the transcript before AI marking.

## 8. Git discipline

- Never stage `private/`, temporary scripts, caches or logs.
- Use focused, conventional commits whose subject states why.
- Before force-pushing or recreating tags, report exactly which refs/releases
  will change and wait for approval.
- After a push, verify CI on the exact pushed SHA; after release, verify package
  artifacts and the installed/running version.
