# CLAUDE.md — entry point

Read [AGENTS.md](AGENTS.md) before editing. It is the authoritative,
tool-agnostic repository guide and includes Definition of Done, Hard Stops,
release/service invariants, privacy rules and evidence requirements.

Order of precedence:

1. The user's explicit instructions in the current conversation.
2. `AGENTS.md` for this repository.
3. General engineering judgment for uncovered details.

If instructions conflict, do not silently weaken a guardrail. State the
conflict and ask for the explicit approval required by `AGENTS.md`.
