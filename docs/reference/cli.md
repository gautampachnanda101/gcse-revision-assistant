# Commands

Run `gcse-revision help` for this list on any computer.

## For everyone

- `gcse-revision`: Starts the app and opens the browser.
- `gcse-revision doctor`: Prints a health report.
- `gcse-revision update`: Downloads new subjects now.
- `gcse-revision upgrade`: Installs a newer version of the program.
- `gcse-revision setup-ai`: Sets up the AI helper and downloads its model.
- `gcse-revision import <folder, .zip or .pack.json>`: Adds a private subject pack.
- `gcse-revision export <pack.js> [file]`: Saves a pack in the shareable format.
- `gcse-revision version`: Prints the version.
- `gcse-revision completion <bash, zsh, fish or powershell>`: Prints a shell completion script.

## Background service

- `gcse-revision service install`: Sets the app to run in the background, and starts it.
- `gcse-revision service status`: Shows whether it's running.
- `gcse-revision service restart`: Restarts it and waits until it answers.
- `gcse-revision service start` / `stop`: Starts or stops it.
- `gcse-revision service logs`: Shows the last 100 log lines.
- `gcse-revision service uninstall`: Stops it and removes it.
- `gcse-revision service ensure`: Reinstalls it, but only if it was installed before.

## For maintainers

- `gcse-revision build content/<subject>`: Builds a subject and updates `subjects.js`.
- `gcse-revision validate`: Checks every subject.
- `gcse-revision manifest`: Rebuilds `subjects.js` from `data/`.
- `gcse-revision sign` / `verify`: Signs or checks `subjects.js`. CI uses these.
- `gcse-revision keygen`: Creates a signing key pair. Use `scripts/setup-signing.sh` instead.
- `gcse-revision encrypt-private`: Encrypts packs in `private/` for a personal build.

## Options

- `--port`: The port to use. Default: `8765`.
- `--offline`: Never go online. Default: off.
- `--no-ai`: Don't set up the AI helper. Default: off.
- `--no-browser`: Don't open the browser. Default: off.
- `--no-auto-update`: Don't upgrade automatically. Default: off.
- `--model`: The AI model to use. Default: chosen from memory size.
- `--repo`: Where to download subjects from. Default: this project.

Put options before the command or straight after it:

    gcse-revision --offline update      # works
    gcse-revision update --offline      # works
    gcse-revision service install --port 8777   # the port is ignored
    gcse-revision --port 8777 service install   # works

The program exits with `0` on success, `1` if the command failed and `2` if it was typed wrongly. Use
`gcse-revision version`, not `--version`.
