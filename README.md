# GCSE Revision

A free, offline-first GCSE revision app: practice questions with model answers, revision notes and progress
tracking, plus an optional AI helper that comments on answers. Everything runs on your own computer.

## Install

Open **Terminal** (Mac or Linux) or **PowerShell** (Windows) and paste the commands for your computer.

### Mac or Linux

    curl -fsSL https://raw.githubusercontent.com/\
    gautampachnanda101/gcse-revision-assistant/main/install.sh | sh

### Windows

    $u = "https://raw.githubusercontent.com/gautampachnanda101/" +
         "gcse-revision-assistant/main/install.ps1"
    irm $u | iex

### Homebrew (Mac or Linux)

    brew install gautampachnanda101/tap/gcse-revision

### Scoop (Windows)

    scoop bucket add gautampachnanda101 `
      https://github.com/gautampachnanda101/scoop-bucket
    scoop install gcse-revision

The app then opens at <http://gcse-revision.localhost:8765/> and starts by itself every time you log in.

## Documentation

- [Using the app](docs/using-the-app.md): for parents, carers and students.
- [Troubleshooting](docs/troubleshooting.md): when something doesn't work.
- [Getting started](docs/develop/getting-started.md): build it and make your first change.
- [Subjects](docs/develop/subjects.md): add or edit revision questions.
- [Testing](docs/develop/testing.md) and [Releasing](docs/develop/releasing.md): for maintainers.
- [Architecture](docs/how-it-works/architecture.md), [Security and privacy](docs/how-it-works/security-and-privacy.md)
  and [Decisions](docs/how-it-works/decisions.md): how and why it works the way it does.
- Reference: [Commands](docs/reference/cli.md), [Web API](docs/reference/http-api.md),
  [Configuration](docs/reference/configuration.md) and [Pack format](docs/reference/pack-format.md).

The same pages are published in Backstage TechDocs (see `catalog-info.yaml` and `mkdocs.yml`).

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md). Rules for coding agents and maintainers are in
[AGENTS.md](AGENTS.md).

## Legal notice

- **Not affiliated.** This project is independent. It is not affiliated with, sponsored by or endorsed by
  AQA, Pearson/Edexcel, OCR, WJEC or any other exam board, school or publisher. Exam board names,
  specification codes and marks are the property of their owners and are used only to say which
  specification a revision item follows.
- **Original content.** Public subject packs contain original practice questions written to follow each
  board's published specification. They are not past-paper questions, and they don't reproduce board,
  publisher or school materials. Model answers are indicative exemplars, not official mark schemes. Other
  valid answers are credited in the exam.
- **Third-party material stays private.** Anything derived from material supplied by a school, publisher or
  board, such as course booklets, is for personal use only. It is never committed to this repository, the
  signed content channel or a release. See
  [Private material](docs/develop/subjects.md#private-material).
- **No warranty.** The software and content are provided "as is", without warranty of any kind, and may
  contain errors. Check them against your own course materials and teacher. Use is at your own risk, and the
  authors aren't liable for any loss, including exam outcomes.
- **AI helper.** Answers from the AI helper are machine-generated, may be wrong, and are not vetted or
  endorsed by the authors. It runs locally using third-party models and software under their own licences.
- **Takedown.** If you believe something in this repository infringes your rights, open an issue or contact
  the maintainer and it will be removed promptly.
