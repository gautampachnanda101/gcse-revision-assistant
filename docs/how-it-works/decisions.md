# Decisions

Each decision has a short record (an ADR) in the `docs/adrs` folder. These six were written down in
November 2026 to describe how the app already works.

1. [One Go program with no dependencies](../adrs/0001-single-binary.md). Families can install it with one command.
2. [The web server only talks to this computer](../adrs/0002-local-only-server.md). Students' data stays private, and other websites can't reach it.
3. [Subjects are signed and released separately](../adrs/0003-signed-subjects.md). Fixes reach students quickly, and nobody can push fake content.
4. [Each computer's own service manager runs the app](../adrs/0004-background-service.md). It's always available after login.
5. [Private material never leaves the computer](../adrs/0005-private-packs.md). Families can use school material without it being shared.
6. [Operating-system calls are replaceable in tests, and coverage is 100%](../adrs/0006-testable-os-calls.md). The riskiest code is tested on every platform.

To record a new decision, copy any ADR, give it the next number and add it to this list.
