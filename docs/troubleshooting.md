# Troubleshooting

Start with:

    gcse-revision doctor

It prints a health report like this:

    Version              gcse-revision v0.12.3
    Installation         package-managed
    Background service   running
    App                  healthy (http://gcse-revision.localhost:8765/)
    Running version      v0.12.3 (matches this CLI)
    Auto-upgrade         enabled
    Built-in AI          available (Apple Intelligence)

Then find the problem below.

## The page won't open

    gcse-revision service restart

Wait 10 seconds and reload. If it still fails, check the log:

    gcse-revision service logs

Common messages, and what to do:

- `configured service port 8765 is already in use`: Another program is using port 8765. Close it, or run `gcse-revision --port 8777 service install` to use another port.
- `background service did not stop in time`: Stop the old copy by hand (see below), then run `gcse-revision service start`.
- `service started but … is not responding`: Run `gcse-revision --no-browser` in a terminal to see the error.

To stop the service by hand:

- **Mac:** `launchctl bootout gui/$(id -u)/com.gautampachnanda101.gcse-revision`
- **Mac with Homebrew:** `brew services stop gautampachnanda101/tap/gcse-revision`
- **Linux:** `systemctl --user stop gcse-revision.service`
- **Windows:** `schtasks /End /TN GCSERevision`

Options such as `--port` must come before `service`. See [Commands](reference/cli.md#options).

## The version doesn't match after an upgrade

`doctor` shows a line like `Running version  v0.12.2 (CLI is v0.12.3; …)`. The new version is installed, but
the old one is still running. Restart it.

Standard install:

    gcse-revision service restart

Homebrew:

    brew services restart gautampachnanda101/tap/gcse-revision

## A subject is missing or out of date

Subjects update when the app starts. Restart it with `gcse-revision service restart`. If that doesn't help,
run the update yourself to see the reason:

    gcse-revision update

What the message means:

- `Subject packs up to date (0 downloaded).`: Nothing new has been published yet.
- `subjects.js signature is invalid — refusing downloaded content`: The signing key changed. Run `gcse-revision upgrade`, then `update` again.
- `checksum mismatch`: The published files are damaged. A maintainer needs to publish again.
- `GET https://api.github.com/…: 403 Forbidden`: GitHub is limiting requests from this network. Try again in an hour.

The app keeps working with the subjects it already has.

## The AI helper isn't ready

    gcse-revision setup-ai

This runs the whole set-up and prints what's wrong. Common messages, and what to do:

- `switched off (started with --no-ai)`: Normal for the background app. Click the set-up button in the app, or run `setup-ai`.
- `…started with --offline…`: Run it once without `--offline` so it can download.
- `didn't start within 30 seconds`: Check `ollama.log` in the cache folder shown by `doctor`.
- **Asks for a password on Linux**: Install `zstd` (`sudo apt-get install zstd`) and run `setup-ai` again.

The first model download is a few gigabytes. Model answers work without the AI helper.

## Still stuck?

[Open an issue](https://github.com/gautampachnanda101/gcse-revision-assistant/issues) with the output of
`gcse-revision doctor` and `gcse-revision service logs`. Remove anything private, such as folder names, first.
