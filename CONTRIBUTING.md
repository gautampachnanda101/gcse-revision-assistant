# Contributing

Thanks for helping. The full guides live in the documentation:

- **First time?** Follow [Getting started](docs/develop/getting-started.md). It goes from clone to
  pull request.
- **Adding or editing a subject:** [Subjects](docs/develop/subjects.md).
- **Changing Go code:** [Testing](docs/develop/testing.md). CI requires 100% unit-test
  coverage.
- **Agent and maintainer rules** (Definition of Done, hard stops, privacy): [`AGENTS.md`](AGENTS.md).

## Subjects in one minute

Edit the **source** in `content/<pack-id>/` (`pack.json` plus one `modules/*.json` per specification module),
then build and validate:

```sh
GOWORK=off go run . build content/<pack-id>    # regenerates data/<pack-id>.js and subjects.js
GOWORK=off go run . validate                   # schema + "is data/ generated from content/?"
```

Commit the `content/` change together with the regenerated `data/<pack-id>.js` and `subjects.js`. **Never
edit `data/*.js` or `subjects.js` by hand**: CI fails if they don't match what `content/` builds.

Students' saved progress is keyed by `config.storageId` and by question ids, which come from each question's
position. Edit questions in place, add new ones at the end of a module, and never change `storageId` or
`meta.idPrefix` of a published pack. Field-by-field details: [pack format](docs/reference/pack-format.md).

Merged subject changes reach installed apps automatically through the signed content channel the next time
they start. No reinstall is needed.

## Content rules (this is a public repo)

- **Write original questions.** Base them on the exam board's *published specification*: its topics, question
  styles and mark tariffs. Don't copy past papers, mark schemes, textbooks or paid revision guides.
- **Make it clear that model answers are indicative exemplars,** not official mark schemes.
- Short quotations from scripture and other primary religious texts are fine. Cite them in `evidence`.
- In `meta.provenance`, state accurately where the content came from.
- Material you may use but not redistribute (for example school booklets) belongs in a
  [private pack](docs/develop/subjects.md#private-material), never in this repository.

## Before you open a pull request

```sh
gofmt -l .                       # prints nothing
GOWORK=off go vet ./...
GOWORK=off go test ./...
GOWORK=off go run . validate
```

Use a focused [conventional commit](https://www.conventionalcommits.org) whose subject says why, e.g.
`fix: correct the Mool Mantra exam tip`. Never stage `private/`.
