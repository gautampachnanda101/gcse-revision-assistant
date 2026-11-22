# 2. The web server only talks to this computer

Status: accepted. Recorded November 2026.

## Context

The app is a web server on the student's computer. Other devices on the network, and other websites open in
the same browser, could try to use it.

## Decision

- Listen on `127.0.0.1` only.
- Accept requests only for `127.0.0.1`, `localhost` or `gcse-revision.localhost`.
- Refuse requests that change data when they come from another website.
- Send the browser's requests to the AI helper through the app, so the AI helper is never exposed to other
  websites.

## Consequences

- A tablet on the same Wi-Fi can't use a student's copy. That's deliberate.
- New features are protected automatically, because every request passes the same check.
