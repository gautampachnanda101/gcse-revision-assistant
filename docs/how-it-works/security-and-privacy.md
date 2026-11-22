# Security and privacy

The app is used by school students, so it's private by default. Families don't have to change any settings.

## Students' data stays on the computer

- The app only accepts connections from the same computer.
- Progress is stored in the browser, not on a server.
- There are no accounts, no analytics and no adverts.
- The AI helper runs locally unless an adult connects a cloud service.
- Speaking practice is recorded in the browser tab only. The browser's speech-to-text may use an online
  service, so students are asked to check the transcript before AI feedback.

## Other websites can't use the app

A website open in the same browser could try to send requests to the app. The web server refuses any
request that:

- uses an address other than `127.0.0.1`, `localhost` or `gcse-revision.localhost`;
- changes data and comes from another website;
- the browser marks as cross-site.

You can check this on a running copy. Both commands print `403`:

    curl -s -o /dev/null -w '%{http_code}\n' \
      -H 'Host: evil.example' \
      http://127.0.0.1:8765/api/info

    curl -s -o /dev/null -w '%{http_code}\n' -X POST \
      -H 'Host: gcse-revision.localhost:8765' \
      -H 'Origin: http://evil.example' \
      http://127.0.0.1:8765/api/import

## Downloaded subjects can't be tampered with

- The app accepts new subjects only if they're signed with the project's key and every file matches its
  checksum.
- Having write access to the repository isn't enough to publish subjects. The signing key is needed too.
- Downloads are size-limited and may contain only the expected files.
- If anything fails, the app keeps its existing subjects.

## Downloaded programs are checked

- The installers and the self-upgrade check every download against the release's checksums and refuse a
  mismatch.
- The private copy of Ollama is checked against Ollama's published checksums before it's unpacked.

## Private material stays private

- Imported packs are stored only on that computer, and are never uploaded or published.
- The `private/` folder is excluded from Git, CI and every release.

## Known weaknesses

A code review found these. They're not fixed yet.

- **App downloads aren't signed.** The checksums come from the same GitHub release as the program. Anyone
  who can publish a release could therefore ship a program. Content doesn't have this problem, because it's
  signed.
- **On Linux without `zstd`, Ollama is installed with its official script.** That script isn't checksum-checked.
- **The saved subject list isn't re-checked on start-up**, and an older signed list would be accepted.
