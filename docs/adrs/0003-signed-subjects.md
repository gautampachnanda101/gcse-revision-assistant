# 3. Subjects are signed and released separately

Status: accepted. Recorded November 2026.

## Context

Questions change much more often than the program. Waiting for an app release to fix a typo is too slow.
But if the app downloads content, someone could publish fake content to every student.

## Decision

- CI signs the subject list with a private key that only GitHub Actions has, and publishes it as a
  `content-…` release.
- The app has the public key built in. It uses downloaded subjects only if the signature and every checksum
  are valid.

## Consequences

- Subject fixes reach students the next time their app starts.
- Write access to the repository isn't enough to publish subjects.
- Changing the key needs an app release first. Older apps keep their current subjects until they upgrade.
