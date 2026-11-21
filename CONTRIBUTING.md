# Adding or updating a subject

Each subject is one self-contained **subject pack**: `data/<name>.js`. Name it `<board>-<subject>.js`, for example `data/aqa-spanish.js`, and use only letters, digits, `-` and `_`.

`subjects.js` lists every pack along with its SHA-256. The app uses this list to load packs. The `gcse-revision` program also uses it to download new or changed packs for people who already have the app installed. After merging, users get the update the next time they start the app, without reinstalling.

## Steps

1. Create or edit `data/<name>.js` using the format below.
2. Run `go run . manifest`. This regenerates `subjects.js` and checks for a few common mistakes.
3. Open `app.html` in a browser, or run `go run .`, and check the subject. Switch to it with **Switch subject**.
4. Commit both files and open a pull request. CI fails if `subjects.js` is out of date.

## Pack format

```js
window.REVISION_SUBJECTS = window.REVISION_SUBJECTS || [];
window.REVISION_SUBJECTS.push({
  config: {
    storageId: "aqa_spanish",            // REQUIRED, unique, never change it (saved progress is keyed by it)
    examBoard: "AQA GCSE",               // shown in the subject picker
    subjectArea: "Languages",            // picker group: "Religious Studies", "Languages", …
    subjectName: "Spanish",
    brandIcon: "🇪🇸", brandTitle: "Spanish Revision", brandSubtitle: "AQA GCSE (8692)",
    pageTitle: "AQA GCSE Spanish Revision", breadcrumb: "Languages / Spanish",
    heroEyebrow: "Paper 1 · Paper 2 · Paper 3 · Paper 4",
    papers: [ { id: "Paper 1", label: "Paper 1", title: "Listening", subtitle: "themes 1–3" } /* … */ ],
    footerNote: "Original practice questions written to the specification; model answers are indicative only."
  },
  questions: {                            // one array per paper: paper1, paper2, …
    paper1: [{
      id: "es-p1-1-1-0",                  // unique within the pack
      paper: "Paper 1",                   // must match a config.papers[].id
      module: "1.1", moduleTitle: "Identity and relationships with others",
      question: "…", marks: 4, command: "Outline",
      sourcePage: null, rubric: "",
      modelAnswer: "…",                   // plain text; \n for line breaks
      evidence: ["…"],                    // quotations / sources / key vocab used in the answer
      examTip: "…",
      answerTopic: "family"
    }]
  },
  notes: [{                               // one revision note per module
    id: "es-p1-1.1", paper: "Paper 1", module: "1.1", title: "…",
    specFocus: "…", sourcePages: "Written to match the AQA specification.", summary: "…"
  }],
  rubrics: { "Paper 1": { "4": "How 4-mark answers are credited…" } },  // keyed by paper, then marks or command
  moduleCounts: { paper1: 9 },
  meta: { title: "AQA GCSE Spanish", qualification: "GCSE Spanish (8692)", questionTotal: 0, provenance: "…" }
});
```

## Content rules (this is a public repo)

- **Write original questions.** Base them on the exam board's *published specification*: its topics, question styles and mark tariffs. Don't copy past papers, mark schemes, textbooks or paid revision guides.
- **Make it clear that model answers are indicative exemplars,** not official mark schemes.
- Short quotations from scripture and other primary religious texts are fine. Cite them in `evidence`.
- In `meta.provenance`, state accurately where the content came from.
