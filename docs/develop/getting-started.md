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

You can also open `app.html` straight from the checkout, without the program. The AI helper then needs a
server set up by hand in the app's **Grown-ups** settings.

These options keep your copy apart from an installed one: another port, only the subjects in your checkout,
no AI set-up and no self-upgrade. See [Commands](../reference/cli.md#options).

## Make a change

To change questions, follow [Subjects](subjects.md). To change code, follow [Testing](testing.md). Reload
the page to see your change.

## Run the checks

Run the [checks in Testing](testing.md#before-you-push) before every pull request. CI runs the same ones.

## Open a pull request

    git checkout -b fix/sikhism-exam-tip
    git add content/aqa-rs-a-sikhism data/aqa-rs-a-sikhism.js subjects.js
    git commit -m "fix: clarify the exam tip for the Mool Mantra question"
    gh pr create --fill

The commit prefix (`fix:`, `feat:` …) decides the next version number. See [Releasing](releasing.md#the-program). Never use `git add -A`, because it can pick up files
from `private/`.
