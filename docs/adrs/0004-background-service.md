# 4. Each computer's own service manager runs the app

Status: accepted. Recorded November 2026.

## Context

Students forget to start programs, so the app should always be running after login. Homebrew installs come
with their own service definition, and two definitions for the same app clash.

## Decision

- Use the system's own tool: a LaunchAgent on Mac, a systemd user service on Linux, a Task Scheduler task on
  Windows.
- If Homebrew installed the app, let Homebrew run the service.
- After a package-manager upgrade, the running app notices the new version and restarts the service with it.

## Consequences

- The same commands (`gcse-revision service …`) work on every computer.
- The automatic restart after Homebrew upgrades hasn't been tested against a real `brew upgrade` yet. See
  [Troubleshooting](../troubleshooting.md) if versions don't match.
