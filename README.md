# GCSE Revision

A free, offline-first GCSE revision app: question banks with model answers, revision notes and progress tracking.
It also has an optional AI helper that gives feedback on your answers. The AI helper runs **on your own computer**, so nothing you write is sent anywhere.

## Install and run (one command)

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/gautampachnanda101/gcse-revision-assistant/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/gautampachnanda101/gcse-revision-assistant/main/install.ps1 | iex
```

The installer registers a per-user background service (launchd on macOS, systemd on Linux, or Task Scheduler on Windows), opens `http://gcse-revision.localhost:8765/`, and leaves the app running after logout/login. Standalone installations check for checksum-verified binary upgrades daily. Homebrew and Scoop installations invoke their package manager automatically.

Useful commands:

```text
gcse-revision service status
gcse-revision service restart
gcse-revision service logs
gcse-revision upgrade
```

Set `GCSE_REVISION_AUTO_UPDATE=0` or pass `--no-auto-update` to disable automatic application upgrades. The app remains fully usable offline.

**Homebrew / Scoop**

```sh
brew install gautampachnanda101/tap/gcse-revision
scoop bucket add gautampachnanda101 https://github.com/gautampachnanda101/scoop-bucket && scoop install gcse-revision
```

This installs one small program (`gcse-revision`) with no other dependencies, then opens the app in your browser.
To start it again later, run `gcse-revision`.

## What the program does

- **Serves the app** at `http://127.0.0.1:8765`. It's only reachable from your own computer.
- **Keeps subjects up to date without app updates.** At start-up it checks this repo's `content` branch for new or updated subject packs and downloads them. That branch is signed: CI signs the subject list (which records every pack's SHA-256) with a key only the maintainer holds, and the app rejects anything that doesn't verify against the public key built into it. Offline, it uses the subjects it already has, so everything works without internet once installed.
- **Sets up the AI helper automatically.** It uses [Ollama](https://ollama.com) if it's already installed. If not, it downloads the official Ollama release into its own folder, with checksums verified and no admin rights needed. It then starts Ollama and downloads a model sized to your computer's memory:

  | RAM | Model |
  | --- | --- |
  | under 7 GB | `qwen3:1.7b` |
  | 7–13 GB | `llama3.2` |
  | 14 GB or more | `gemma3:4b` |

  If you've already downloaded a suitable model, it reuses that one instead. The first-time model download is a few GB; after that the AI helper works offline.

### Options

```
gcse-revision [--port 8765] [--no-browser] [--offline] [--no-ai] [--model NAME]
gcse-revision update      # fetch new/updated subject packs
gcse-revision setup-ai    # install the AI helper and download its model now
gcse-revision version
```

Downloaded subjects and the private Ollama copy live in your user cache folder: `~/Library/Caches/gcse-revision` on macOS, `~/.cache/gcse-revision` on Linux, and `%LOCALAPPDATA%\gcse-revision` on Windows. Ollama keeps its models in its usual place (`~/.ollama`).

You can also open `app.html` straight from a checkout of this repo without the program. In that case the AI helper needs a manually configured server; see the in-app "Grown-ups" settings.

## Adding a subject

See [CONTRIBUTING.md](CONTRIBUTING.md). In short:

1. Add `data/<subject>.js`.
2. Run `go run . manifest`.
3. Open a pull request.

Once it's merged, everyone gets the new subject the next time they start the app. They don't need to reinstall.

## Open pack format, import and export

Subjects are plain JSON described by a JSON Schema ([`schema/gcse-revision-pack.schema.json`](schema/gcse-revision-pack.schema.json), draft 2020-12). A pack is either a folder (`pack.json` + `modules/*.json`) or a single `.pack.json` bundle, optionally zipped.

- `gcse-revision import <bundle.json | folder | .zip>` validates against the schema and installs the pack on this device only (or use **Switch subject → Import pack…** in the app).
- `gcse-revision export <pack.js> [out.pack.json]` turns an installed pack back into the open format. Packs containing recordings must be exported to a `.zip` so their audio files are included.
- `gcse-revision validate` checks every pack and `content/` folder against the schema.

Imported packs are private to your machine and are never published.

**Audio.** Language packs set `config.speechLang` (e.g. `fr-FR`), which adds a 🔊 Listen button using your browser/OS text-to-speech voices (no files, works offline; voice quality varies and Chinese, Russian or Hindi may need an OS voice pack). A question can also carry its own recording: put files in the pack's `audio/` folder (`.mp3 .ogg .wav .m4a .webm`) and set `"audio": "clip1.mp3"` on the question. Recordings are supported for imported (private) packs only, so only use audio you have the right to use.

## Private subjects (personal use only)

Some material can be used for your own revision but can't be published, for example booklets supplied by a school.
There are two ways to use it:

- **Drop-in folder.** Put pack files in the folder that `gcse-revision --help` names. They're loaded on this computer only.
- **Baked into your own binary.** Put pack files in `./private/` (gitignored), then run `go build -o gcse-revision .`.
  To keep them encrypted on disk, set `GCSE_PRIVATE_KEY` (base64 of 32 random bytes, `openssl rand -base64 32`) and run `go run . encrypt-private`. Build with `-ldflags "-X main.privatePackKey=$GCSE_PRIVATE_KEY"` so the binary can decrypt them.

  The resulting binary includes those subjects and works fully offline. The key is inside the binary, so this is protection against casual inspection, not copy protection. Don't redistribute that binary or publish it as a release. CI and public releases never contain private packs.

## Publishing content (maintainers)

Merge a subject pack into `main`. The **Publish content** workflow then:

1. validates it;
2. signs `subjects.js`;
3. force-pushes the signed set to the `content` branch;
4. checks that a fresh install downloads and verifies exactly that content from GitHub.

Installed apps pick it up the next time they start. One-time set-up of the signing key: `sh scripts/setup-signing.sh`.

## Releases (maintainers)

Versions are [semantic](https://semver.org) and automatic. After CI passes on `main`, the **Auto tag** workflow reads the [conventional commits](https://www.conventionalcommits.org) since the last tag:

- `feat:` bumps the minor version.
- `fix:` or `perf:` bumps the patch version.
- `type!:` or a `BREAKING CHANGE` footer bumps the major version (the minor version while still 0.x).

It releases only if one of those commits changed files that ship in the binary (Go code, `app.html`, `subjects.js`, the installers). Subject-pack content changes go out through the content channel and don't need a release.

The **Release** workflow then builds all platforms, publishes the GitHub release and updates the Homebrew formula in `homebrew-tap` and the Scoop manifest in `scoop-bucket`. That needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret on this repo: a token with write access to both of those repos. You can also release by hand with `git tag vX.Y.Z && git push origin vX.Y.Z`.

## Tests

- `go test ./...` runs the unit tests.
- `go test -tags e2e -v ./...` runs the end-to-end tests. They build the real binary and test:
  - signed sync, including rejection of a tampered list or pack;
  - offline serving of downloaded and private subjects;
  - local-only access rules;
  - the page rendered in headless Chrome;
  - the one-command installer, including rejection of a bad checksum.
- Add `E2E_AI=1` to the end-to-end tests to also download a private Ollama and a tiny model, then chat through the app's proxy.

CI runs all of this on Linux, macOS and Windows. After each release, it installs the real release on all three operating systems with the one-line command.

## Building from source

```sh
go build -o gcse-revision .   # Go 1.22+, standard library only
```

## Legal notice

- **Not affiliated.** This project is independent. It is not affiliated with, sponsored by or endorsed by AQA, Pearson/Edexcel, OCR, WJEC or any other exam board, school or publisher. Exam board names, specification codes and marks are the property of their owners and are used only to say which specification a revision item follows.
- **Original content.** Public subject packs contain original practice questions written to follow each board's published specification. They are not past-paper questions, and they don't reproduce board, publisher or school materials. Model answers are indicative exemplars, not official mark schemes. Other valid answers are credited in the exam.
- **Third-party material stays private.** Anything derived from material supplied by a school, publisher or board, such as course booklets, is for personal use only. It is never committed to this repository, the signed content channel or a release. See "Private subjects".
- **No warranty.** The software and content are provided "as is", without warranty of any kind, and may contain errors. Check them against your own course materials and teacher. Use is at your own risk, and the authors aren't liable for any loss, including exam outcomes.
- **AI helper.** Answers from the AI helper are machine-generated, may be wrong, and are not vetted or endorsed by the authors. It runs locally using third-party models and software under their own licences.
- **Takedown.** If you believe something in this repository infringes your rights, open an issue or contact the maintainer and it will be removed promptly.
