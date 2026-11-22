# 6. Operating-system calls are replaceable in tests, and coverage is 100%

Status: accepted. November 2026.

## Context

The code that manages services, upgrades and the AI helper had almost no tests, because it called
`launchctl`, `brew` and other system tools directly. Coverage was 33%, and the code review found the most
serious bugs in exactly that code.

## Decision

- Make every operating-system call through a variable in `seams.go` that tests can replace.
- Fake system commands by re-running the test program as a stand-in.
- Fail CI if unit-test coverage drops below 100%.

## Consequences

- Every platform's code is tested in about 3 seconds, on any computer.
- Getting to 100% found and fixed two bugs in pack export.
- Tests can't run in parallel, because they change shared variables.
- Unit tests can't prove the real system tools behave as faked. The release smoke tests cover that.
