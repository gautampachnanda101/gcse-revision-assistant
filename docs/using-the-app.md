# Using the app

This page is for parents, carers and students. You don't need any technical knowledge.

## Install

Open **Terminal** (Mac or Linux) or **PowerShell** (Windows). Paste the commands for your computer and press
Enter.

**Mac or Linux**

    curl -fsSL https://raw.githubusercontent.com/\
    gautampachnanda101/gcse-revision-assistant/main/install.sh | sh

**Windows**

    $u = "https://raw.githubusercontent.com/gautampachnanda101/" +
         "gcse-revision-assistant/main/install.ps1"
    irm $u | iex

**Mac or Linux, with Homebrew**

    brew install gautampachnanda101/tap/gcse-revision

**Windows, with Scoop**

    scoop bucket add gautampachnanda101 `
      https://github.com/gautampachnanda101/scoop-bucket
    scoop install gcse-revision

When it finishes, the app opens at <http://gcse-revision.localhost:8765/>. Bookmark that address. The app
starts by itself every time you log in.

## Revise

1. Choose a subject with **Switch subject**.
2. Open a question and write your answer.
3. Reveal the **model answer** and the exam tip to compare.
4. Tick the question as revised, or bookmark it to come back to.

Model answers show one good way to answer. Examiners also accept other correct answers.

## Keep your progress

Progress is saved in the browser on that computer. To move it to another computer or browser:

1. Click **Export progress** and save the file.
2. On the new computer, click **Import a progress backup** and choose the file.

## The AI helper

The AI helper reads an answer and suggests how to improve it. You don't need it: model answers always work.

- It runs on your own computer, so answers stay private.
- An adult can connect a cloud AI service instead, in the **Grown-ups** settings. Answers are then sent to
  that service.
- AI feedback can be wrong. Treat any mark it gives as a rough guide, not an official mark.

## Privacy

- Only your own computer can open the app.
- It goes online only to download new subjects and app updates.
- There's no account, no advertising and no tracking.
- The app isn't connected with AQA, Edexcel, OCR or any other exam board.

## If something goes wrong

- **The page won't open**: Run `gcse-revision service restart`, wait 10 seconds and reload.
- **A new subject is missing**: Run `gcse-revision service restart`. Subjects update when the app starts.
- **The AI helper isn't ready**: Run `gcse-revision setup-ai`.
- **Still stuck**: Run `gcse-revision doctor` and include the output when you [ask for help](https://github.com/gautampachnanda101/gcse-revision-assistant/issues).

## Uninstall

    gcse-revision service uninstall

Then delete the program: `~/.local/bin/gcse-revision` on Mac or Linux, or the
`%LOCALAPPDATA%\Programs\gcse-revision` folder on Windows. If you used Homebrew or Scoop, run
`brew uninstall gcse-revision` or `scoop uninstall gcse-revision` instead.
