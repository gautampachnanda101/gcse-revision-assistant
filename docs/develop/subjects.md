# Subjects

Each subject is a pack. You edit its source in `content/<subject>/`, then build it into `data/<subject>.js`,
which the app loads.

    content/aqa-rs-a-sikhism/
    ├── pack.json          subject settings: name, board, papers
    └── modules/
        ├── 1.1.json       one file per specification module
        └── 1.2.json

## Edit an existing subject

1. Edit the files in `content/<subject>/`.
2. Run `GOWORK=off go run . build content/<subject>`.
3. Run `GOWORK=off go run . validate`.
4. Commit the `content/` files together with the rebuilt `data/<subject>.js` and `subjects.js`.

### Protect students' progress

Students' ticks and bookmarks are stored against question ids. The ids come from each question's position, so:

- **Do** edit question text, answers and tips.
- **Do** add new questions at the end of a module.
- **Don't** insert, delete or reorder questions in the middle of a module.
- **Don't** change a module's code (for example `1.1`), `config.storageId` or `meta.idPrefix`.

## Add a new subject

### 1. Create `pack.json`

Create `content/<subject>/pack.json`. The easiest way is to copy one from a similar subject in `content/` and
change the fields. These are the required ones:

    {
      "config": {
        "storageId": "aqa_geography",
        "examBoard": "AQA GCSE",
        "subjectArea": "Geography",
        "subjectName": "Geography",
        "brandTitle": "Geography (AQA)",
        "papers": [
          {
            "id": "Paper 1",
            "label": "Physical geography",
            "title": "Paper 1: Physical geography"
          }
        ]
      },
      "meta": {
        "provenance": "Original questions written to the AQA specification."
      }
    }

`storageId` can never change once the subject is published.

### 2. Add the modules

Add one file per specification module in `content/<subject>/modules/`:

    {
      "paper": "Paper 1",
      "module": "1.1",
      "moduleTitle": "The challenge of natural hazards",
      "note": { "summary": "The revision note students read." },
      "questions": [
        {
          "question": "Outline two effects of a tectonic hazard.",
          "marks": 4,
          "command": "Outline",
          "modelAnswer": "…"
        }
      ]
    }

### 3. Build and check

Build and validate it as above, then check it in the app.

Every field is listed in the [pack format reference](../reference/pack-format.md).

## Content rules

This is a public repository, so:

- Write original questions based on the exam board's published specification.
- Never copy past papers, mark schemes, textbooks or paid revision guides.
- Model answers are examples, not official mark schemes.
- Short quotations from religious texts are fine. Cite them in `evidence`.
- Say where the content came from in `meta.provenance`.
- Published packs can't include audio recordings. Use a private pack instead.

## Private material

Families sometimes have material they may use but not share, such as school booklets. Keep it in a private
pack. Private packs stay on one computer and are never committed or published.

    gcse-revision import ./my-pack/

The pack can be a folder, a `.zip` or a single `.pack.json` file.

The pack appears in **Switch subject** after a reload. Private packs can include audio: put the files in an
`audio/` folder and set `"audio": "clip.mp3"` on the question.

## Common validation errors

- `out of date: run go run . build content/<subject>`: Rebuild. You edited `content/` without building, or edited `data/` by hand.
- `paper "Paper 2" isn't one of config.papers`: The module's `paper` must match a paper `id` in `pack.json`.
- `missing required property "marks"`: Add the field. The text before the colon says which file.
- `audio recordings are only supported for imported (private) packs`: Remove `audio` from public content.
