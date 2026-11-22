# Getting started

This page takes you from a fresh clone to a pull request.

## Set up

You need Git and Go 1.22 or newer. Nothing else is required.

    git clone https://github.com/gautampachnanda101/gcse-revision-assistant
    cd gcse-revision-assistant
    GOWORK=off go build -o gcse-revision .

## Run your copy

    ./gcse-revision --port 18765 --offline --no-ai --no-auto-update

Open <http://gcse-revision.localhost:18765/>.

The flags keep your copy separate from any installed version:

- `--port 18765` avoids the installed app on port 8765.
- `--offline` shows only the subjects in your checkout.
- `--no-ai` skips the AI set-up.
- `--no-auto-update` stops the program replacing itself.

## Make a change

Subjects live in `content/<subject>/`. Never edit `data/` or `subjects.js`; they're generated.

For example, to improve a question in the AQA Sikhism pack, edit
`content/aqa-rs-a-sikhism/modules/1.1.json`, then rebuild the pack:

    GOWORK=off go run . build content/aqa-rs-a-sikhism

It prints:

    Built data/aqa-rs-a-sikhism.js: 110 questions across 11 modules.
    Wrote subjects.js with 16 subject pack(s).

Reload the page to check your change.

See [Subjects](subjects.md) for the full rules.

## Run the checks

Run these before every pull request. CI runs the same checks.

    gofmt -l .                  # should print nothing
    GOWORK=off go vet ./...
    GOWORK=off go test ./...    # coverage must stay at 100%
    GOWORK=off go run . validate

If you changed Go code, read [Testing](testing.md).

## Open a pull request

    git checkout -b fix/sikhism-exam-tip
    git add content/aqa-rs-a-sikhism data/aqa-rs-a-sikhism.js subjects.js
    git commit -m "fix: clarify the exam tip for the Mool Mantra question"
    gh pr create --fill

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org). The prefix decides the
next version number (see [Releasing](releasing.md)). Never use `git add -A`, because it can pick up files
from `private/`.
