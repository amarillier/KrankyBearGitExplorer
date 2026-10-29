# Contributor language packs

Extra UI languages are loaded from `assets/i18n` at runtime. **English** is provided by an **`en.json` catalog embedded in the binary** so the app always has a baseline. You can still drop an **`en.json`** into `assets/i18n`: if present, it **overrides** that embedded English (and remains the fallback catalog when another locale is missing a key). Every other locale is loaded only from disk packs unless you change the build.

Ideal contributors are native or fluent speakers who create or review translations (including AI-assisted drafts) for accuracy and tone.

## What to add

| File | Required | Purpose |
|------|----------|---------|
| `xx.json` | Yes | UI strings (same key structure as `en.json`). |
| `xx.TESTING.md` | No | Localized migration testing guide (checklist window). If missing, the app falls back to embedded English `TESTING.md`. |
| `help_xx.txt` | No | Localized in-app help. If missing, help stays English. |

Replace `xx` with your **two-letter ISO 639-1** code in lowercase (e.g. Greek: `el.json`, `el.TESTING.md`, `help_el.txt`). Only names matching `^[a-z]{2}\.json$` are loaded as UI catalogs.

## Where to put files

The app resolves `assets/i18n` in this order:

1. Next to the executable  
2. On macOS, under `Contents/Resources/assets/i18n` inside the `.app` bundle  
3. Current working directory (`./assets/i18n`)

Ship or copy your files into that folder and restart the app (or use `-language xx` once to set the preference).

## Display name in Preferences

The language picker shows a **native** label from **your** pack, not from English. In `xx.json`, include **either**:

- `locale_labels.xx` — e.g. for `el.json`: `"el": "Ελληνικά"`, or  
- `locale_labels.self` — e.g. `"self": "Ελληνικά"` (same effect for that pack).

You do **not** need a full `locale_labels` matrix for every language inside each file; a single entry for your locale is enough. Optional `_comment` keys under `locale_labels` are ignored by the app.

## Verify

From a terminal, next to a build that can see your `assets/i18n`:

```bash
<application> -language list
# or: <application> -language '?'
```

You should see your locale code and the display name you defined. Use `-language xx` to set the UI language for that run and persist it in preferences.

---

## AI prompt: translate `en.json` into `xx.json`

Attach **`en.json`** (or your fork of it) and adapt the target language in the prompt.

**Guidelines**

You are a technical translator for a desktop application. Target language: **(your language name)**; ISO 639-1 code: **(two letters, e.g. el)**.

**Task:** Produce a complete JSON message catalog for the app. Translate every string value; keep every key path identical to the source. Do not remove keys or sections.

**Hard rules**

- Output: **one** valid JSON object only (no preamble, no markdown fences unless the user asked for a paste-ready block).
- Preserve key order and structure exactly (nested objects, same dot-path semantics as the source).
- Preserve placeholders such as `{{name}}` and any `%s`-style patterns exactly.
- Preserve product tokens: file names, CLI flags, URLs, `MIGRATION_ORDER.md`, `HAR_CAPTURE_GUIDE.md`, `ReleaseNotes.txt`, and appropriate terminology where it is a proper noun.
- Set `locale_labels.XX` (where `XX` is your file’s code) or `locale_labels.self` to the **endonym** for your language (how native speakers name the language).
- Use UTF-8. Escape JSON as required.

**Quality bar:** If the output is dramatically shorter than the source (e.g. far fewer keys), you have likely dropped content—fix before returning.

---

## AI prompt: translate `TESTING.md` into `xx.TESTING.md`

Attach **`TESTING.md`** (from the same repo / release).

**Guidelines**

You are a technical translator for a desktop application. Target language: **(your language name)**.

**Task:** Translate the attached `TESTING.md` in full. Do not summarize, shorten, or merge sections.

**Hard rules**

- Preserve structure exactly: same `#` / `##` / `###` / `####` headings, blank lines, and fenced code blocks (triple backticks).
- Preserve every checklist line: every `[ ]`, every numbered step, every arrow/note markers, every bold marker. Only translate human-readable prose; keep identifiers and component names aligned with the English source unless the locale uses a standard appropriate UI term (then add a short glossary at the end).
- Do not drop major sections (e.g. phase headings, “After Each Migration”, “Testing Notes”, “Test Scenarios”, “Common Issues & Solutions”, “Validation Checklist”, “Getting Help”, “Document Maintenance”) unless the source truly has no equivalent.
- Product/API tokens: keep `MIGRATION_ORDER.md`, `HAR_CAPTURE_GUIDE.md`, `ReleaseNotes.txt`, CLI flags, file names, and URLs unchanged.
- Output: single Markdown document. Filename convention: **`xx.TESTING.md`** (same two-letter code as your JSON). No preamble like “Here is the translation…”.

**Quality bar:** If the output is shorter than ~85% of the source line count, you have almost certainly omitted content—fix before returning.
