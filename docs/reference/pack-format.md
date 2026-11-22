# Pack format

A subject pack is JSON. The formal definition is
[`schema/gcse-revision-pack.schema.json`](https://github.com/gautampachnanda101/gcse-revision-assistant/blob/main/schema/gcse-revision-pack.schema.json).

## Files

    content/<subject>/pack.json        settings for the whole subject
    content/<subject>/modules/*.json   one file per specification module

`gcse-revision build` turns these into `data/<subject>.js`. To share a pack as one file, use
`gcse-revision export`; the result has the same fields, with the modules in a `modules` list.

## `pack.json`

    {
      "config": {
        "storageId": "aqa_rs_a_sikhism",
        "examBoard": "AQA GCSE",
        "subjectArea": "Religious Studies",
        "subjectName": "Sikhism",
        "brandTitle": "Sikhism (AQA RS A)",
        "papers": [
          {
            "id": "Beliefs",
            "label": "Beliefs & teachings",
            "title": "Sikhism: Beliefs and teachings"
          }
        ]
      },
      "rubrics": {
        "Beliefs": { "1": "1 mark. One accurate, relevant answer." }
      },
      "meta": {
        "title": "AQA GCSE Religious Studies A: Sikhism",
        "provenance": "Original questions written to the AQA specification."
      }
    }

### `config`

- `storageId`: Required. Lowercase letters, digits and `_`. Never change it after publishing.
- `examBoard`: Required. For example `AQA GCSE`.
- `subjectArea`: Required. The group in the subject list, e.g. `Languages`.
- `subjectName`: Required. For example `Sikhism`.
- `brandTitle`: Required. The title students see.
- `papers`: Required. Each paper needs `id`, `label` and `title`.
- `speechLang`: Optional. For language subjects, e.g. `fr-FR`. Adds a "Read prompt aloud" button.
- `brandIcon`, `brandSubtitle`, `pageTitle`, `breadcrumb`, `heroEyebrow`, `footerNote`: Optional. Extra display text.

### `rubrics`

How marks are awarded, by paper and then by mark value or command word.

### `meta`

`provenance` says where the content came from. `idPrefix` exists only in older packs, to keep their question
ids unchanged.

### `markingSchema`

Optional. These are the rules the AI helper uses to mark answers. Religious Studies packs from AQA or Edexcel
get a general one automatically. It's marked `adaptable` because it hasn't been checked against an official
mark scheme.

## Module files

    {
      "paper": "Beliefs",
      "module": "1.1",
      "moduleTitle": "The nature of God as expressed in the Mool Mantra",
      "note": {
        "specFocus": "What the specification says this module covers.",
        "summary": "The revision note."
      },
      "questions": [
        {
          "question": "Give one quality of God described in the Mool Mantra.",
          "marks": 1,
          "command": "Give",
          "modelAnswer": "Any one of: God is One (Ik Onkar); …",
          "evidence": ["Mool Mantra, Ang 1"],
          "examTip": "A 1-mark question needs one accurate point only.",
          "answerTopic": "qualities of God"
        }
      ]
    }

- `paper`: Required. Must match a paper `id` in `pack.json`.
- `module`: Required. The specification code, e.g. `1.1`.
- `moduleTitle`: Required.
- `note.summary`: Recommended. The revision note. Without it, validation shows a warning.
- `questions[].question`: Required.
- `questions[].marks`: Required. A whole number from 1 to 100.
- `questions[].command`: Required. The command word, e.g. `Explain`.
- `questions[].modelAnswer`: Required. An example answer, not an official mark scheme.
- `questions[].evidence`, `examTip`, `answerTopic`: Optional.
- `questions[].audio`: Optional. Private packs only: a file in the pack's `audio/` folder.

## Question ids

The build gives every question an id from its position, for example `aqa-rs-a-sikhism-paper1-1-1-0`: the
subject, then paper 1, module 1.1 and question 0. Which edits are safe is explained in
[Protect students' progress](../develop/subjects.md#protect-students-progress).

## Limits for imported packs

- 32 MB per import.
- Zips: at most 2,000 files, and 128 MB in total.
- Audio: `.mp3`, `.ogg`, `.oga`, `.wav`, `.m4a` or `.webm`, and 64 MB in total.
