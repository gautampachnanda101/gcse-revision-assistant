# Configuration

Most people never need to change anything. These settings are for testing, mirrors and private material.

## Environment variables

- `GCSE_REVISION_AUTO_UPDATE=0`: Turns off automatic upgrades.
- `GCSE_REVISION_PRIVATE_PACKS`: Changes where private packs are stored.
- `GCSE_REVISION_MODEL`: Chooses the AI model, if `--model` isn't given.
- `OLLAMA_HOST`: Where Ollama is running. The default is `127.0.0.1:11434`.
- `GCSE_REVISION_REPO`: Where subjects are downloaded from.
- `GCSE_PRIVATE_KEY`: Key for encrypted packs in `private/`: base64 of 32 bytes, e.g. `openssl rand -base64 32`.
- `NO_COLOR=1`: Plain text output, without colours.

### For testing

- `GCSE_REVISION_API_BASE`: A different GitHub API address, such as a test server.
- `GCSE_REVISION_RAW_BASE`: Downloads subjects from a plain file server instead of releases. Signatures are still checked.
- `GCSE_REVISION_PRIVATE_OLLAMA=1`: Ignores any installed Ollama and uses only the app's own copy.

### For the install scripts

- `GCSE_REVISION_BIN_DIR`: Where `install.sh` puts the program. The default is `~/.local/bin`.
- `GCSE_REVISION_NO_RUN=1`: Installs the program without starting the background service.
- `GCSE_REVISION_NO_PATH=1`: Stops `install.ps1` changing `PATH`.
- `GCSE_REVISION_DOWNLOAD_BASE`: Downloads the program from somewhere else (for testing).

`CONTENT_SIGNING_KEY` is used only by CI to sign subjects. It's stored as a GitHub Actions secret and nowhere
else.

## Folders

Run `gcse-revision doctor` to see the folders on your computer.

- **Cache (downloads, logs)**
    - Mac: `~/Library/Caches/gcse-revision`
    - Linux: `~/.cache/gcse-revision`
    - Windows: `%LOCALAPPDATA%\gcse-revision`
- **Private packs**
    - Mac: `~/Library/Application Support/gcse-revision/packs`
    - Linux: `~/.config/gcse-revision/packs`
    - Windows: `%APPDATA%\gcse-revision\packs`
- **Service log**: `service/gcse-revision.log`, inside the cache folder.
- **Progress**: in the browser, on every computer.

## Build settings

These are set when the program is built, with `go build -ldflags "-X main.<name>=<value>"`.

- `version`: Set by the release workflow. The default, `dev`, never upgrades.
- `contentPubKey`: The public key used to check subjects.
- `privatePackKey`: Key for encrypted private packs. Personal builds only; never publish them.
