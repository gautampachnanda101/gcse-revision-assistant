# Releasing

Subjects and the program are released separately.

- **Subjects** reach students the next time their app starts, with no app update needed.
- **The program** is released as a new version, and installed apps upgrade themselves within a day.

## Subjects

![How subjects are published](../diagrams/publish-subjects.svg)

Nothing to do by hand. When a change to `data/` or `subjects.js` is merged, the **Publish content** workflow:

1. checks every pack;
2. signs `subjects.js` with the `CONTENT_SIGNING_KEY` secret;
3. publishes the subjects as a new `content-…` release on GitHub;
4. installs a fresh copy of the app and checks that it receives the new subjects.

To publish again without a change:

    gh workflow run publish-content.yml --ref main

## The program

Versions are worked out from commit messages after CI passes on `main`:

- `fix:` or `perf:` makes a patch release, for example 0.12.3 → 0.12.4.
- `feat:` makes a minor release, for example 0.12.3 → 0.13.0.
- `feat!:`, or a `BREAKING CHANGE` footer, makes a major release (minor while the version is 0.x).
- `docs:`, `test:` and `chore:` don't make a release.

A release happens only if a shipped file changed: Go code, `app.html`, `subjects.js` or the install
scripts. Because `subjects.js` is shipped, a `fix:` to a subject also produces a patch release.

The **Release** workflow builds all six platforms, publishes the release, updates Homebrew and Scoop, then
installs the real release on macOS, Linux and Windows to test it.

### Check a release

A release isn't done until these pass:

    gh run list --workflow release.yml --limit 1
    gcse-revision upgrade
    gcse-revision version
    gcse-revision doctor

- The release run must be green, including the smoke tests.
- `upgrade` must install the new version, and `version` must print it.
- `doctor` must say `Running version … (matches this CLI)`.

Never delete or move a tag, or edit a published release. Fix problems with a new version instead.

## Rotating the signing key

Do this only if the key is lost or exposed.

1. Run `sh scripts/setup-signing.sh`. It creates a new key pair, stores the private key as the
   `CONTENT_SIGNING_KEY` secret, and writes the public key into `signing.go`.
2. Commit `signing.go` with a `fix:` message and let the release go out.
3. Run `gh workflow run publish-content.yml --ref main` to re-sign the subjects.

Installed apps can't verify subjects signed with the new key until they upgrade to the new version. Until
then they keep the subjects they already have. Never commit the private key.
