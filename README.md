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

This installs one small program (`gcse-revision`) with no other dependencies, then opens the app in your browser.
To start it again later, run `gcse-revision`.

## What the program does

- **Serves the app** at `http://127.0.0.1:8765`. It's only reachable from your own computer.
- **Keeps subjects up to date.** At start-up it checks this repo for new or updated subject packs and downloads them. Each pack is checked against its SHA-256 before use. Offline, it uses the subjects it already has: everything works without internet once installed.
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

## Private subjects (personal use only)

Some material can be used for your own revision but can't be published, for example booklets supplied by a school.
There are two ways to use it:

- **Drop-in folder.** Put pack files in the folder that `gcse-revision --help` names. They're loaded on this computer only.
- **Baked into your own binary.** Put pack files in `./private/` (gitignored), then build with:

  ```sh
  go build -tags private -o gcse-revision .
  ```

  The resulting binary includes those subjects and works fully offline. Don't redistribute it, and don't publish it as a release.

## Building from source

```sh
go build -o gcse-revision .   # Go 1.22+, standard library only
```

## Content notice

Questions are original practice items written to follow each exam board's published specification. Model answers are indicative exemplars only, not official mark schemes. Exam boards and their specifications are the property of their respective owners, and this project is not affiliated with or endorsed by any exam board.
