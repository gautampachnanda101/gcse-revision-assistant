# Using the app

This page is for parents, carers and students. You don't need any technical knowledge.

## Install

Open **Terminal** (Mac or Linux) or **PowerShell** (Windows). Paste the commands for your computer and press
Enter.

### Mac or Linux

    curl -fsSL https://raw.githubusercontent.com/\
    gautampachnanda101/gcse-revision-assistant/main/install.sh | sh

### Windows

    $u = "https://raw.githubusercontent.com/gautampachnanda101/" +
         "gcse-revision-assistant/main/install.ps1"
    irm $u | iex

### Mac or Linux, with Homebrew

    brew install gautampachnanda101/tap/gcse-revision

### Windows, with Scoop

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

AI feedback can be wrong. Treat any mark it gives as a rough guide, not an official mark.

The app tries these in order:

1. Built-in AI on the computer. On Apple Silicon Macs with Apple Intelligence switched on, this needs no
   set-up. Run `gcse-revision doctor` to see whether it's available.
2. A local AI program (Ollama). If it isn't installed, the app can download it, with no administrator
   password. It then downloads a model sized to the computer's memory: `qwen3:1.7b` under 7 GB,
   `llama3.2` from 7 to 13 GB, and `gemma3:4b` from 14 GB. A suitable model you already have is reused.
3. A cloud service, but only if an adult has set one up in the **Grown-ups** settings. Answers are then
   sent to that service.

On Windows, a built-in option appears only if a supported local AI bridge is installed.

## Listening practice

Language subjects have a **Read prompt aloud (computer voice)** button. It uses the voices built into your
computer, so it works offline. Some languages, such as Chinese, Russian or Hindi, may need an extra voice
pack from your computer's settings.

## Privacy

The app keeps students' work on the computer. See [Security and privacy](how-it-works/security-and-privacy.md).

## If something goes wrong

See [Troubleshooting](troubleshooting.md).

## Uninstall

    gcse-revision service uninstall

Then delete the program: `~/.local/bin/gcse-revision` on Mac or Linux, or the
`%LOCALAPPDATA%\Programs\gcse-revision` folder on Windows. If you used Homebrew or Scoop, run
`brew uninstall gcse-revision` or `scoop uninstall gcse-revision` instead.
