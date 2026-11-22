# 1. One Go program with no dependencies

Status: accepted. Recorded November 2026.

## Context

Students and parents install the app themselves, often on shared family computers. Needing Node, Python,
Docker, a database or an administrator password would stop many of them.

## Decision

Build one Go program using only the standard library. Build the web page and the built-in subjects into it.

## Consequences

- Installing means downloading one file. It runs on Mac, Linux and Windows, on Intel and ARM.
- There are no third-party packages to update or audit.
- The pack validator is written in-house, so new schema features need new code.
