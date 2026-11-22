# 5. Private material never leaves the computer

Status: accepted. Recorded November 2026.

## Context

Families have material they may use but not share, such as school booklets. Publishing it by mistake would
break copyright.

## Decision

- Imported packs are stored only on that computer. They're never uploaded, signed or published.
- The `private/` folder is excluded from Git and from every build made by CI.
- Audio recordings are allowed only in private packs.

## Consequences

- Families can use their own material with no account or server.
- Contributors must never commit `private/`.
