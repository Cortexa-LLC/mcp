# markitdown — Claude Integration Guide

The `markitdown-mcp` server gives Claude the ability to read documents that are
otherwise opaque: PDFs, Word files, spreadsheets, PowerPoint decks, and images.
This document covers how to configure Claude to use it and the patterns that work best.

---

## CLAUDE.md Configuration

```markdown
## Document Reading

A document converter is available via the `markitdown-mcp` MCP server.

Use `convert_to_markdown` to read any document Claude cannot natively open:
- PDFs, Word (.docx), Excel (.xlsx/.xls), PowerPoint (.pptx)
- Images (.png, .jpg) — requires Tesseract for OCR
- HTML pages and URLs

Pass an absolute file path or an `http://`/`https://` URL:
  `convert_to_markdown({source: "/path/to/document.pdf"})`
  `convert_to_markdown({source: "https://example.com/spec.pdf"})`

Prefer this over asking the user to copy-paste document contents.
```

---

## Use Cases

### 1. Reading Specs and Design Documents

Technical specifications are often PDFs or Word files sitting in the repo. Rather
than asking the user to paste content, read them directly:

```
convert_to_markdown({source: "/path/to/project/docs/api-spec-v2.pdf"})
```

**CLAUDE.md instruction:**
```markdown
When a task references a specification, design document, or RFC:
1. Check if the file exists locally with `glob` or `find`.
2. If it's a PDF, DOCX, or other binary format, use `convert_to_markdown` to read it.
3. Do not ask the user to copy-paste content from documents you can read directly.
```

---

### 2. Extracting Data from Spreadsheets

Excel files are common for configuration tables, test matrices, and data exports.
`convert_to_markdown` renders all sheets as Markdown tables:

```
convert_to_markdown({source: "/path/to/test-matrix.xlsx"})
```

---

### 3. Reading Slide Decks

Architecture review decks and design presentations often contain key decisions in
a format Claude can't read natively. PPTX slides are extracted as structured
Markdown with headings, lists, and tables:

```
convert_to_markdown({source: "/path/to/architecture-review.pptx"})
```

Embedded images in slides are OCR'd if Tesseract is installed.

---

### 4. Fetching Remote Documentation

Read a URL directly — useful for API documentation, changelogs, or RFCs:

```
convert_to_markdown({source: "https://pkg.go.dev/net/http"})
convert_to_markdown({source: "https://example.com/docs/changelog.html"})
```

---

### 5. Combined with the Knowledge Graph

After reading a document, persist the key findings to the KG so future sessions
don't need to re-read it:

```
# 1. Read the document
convert_to_markdown({source: "/docs/auth-design.pdf"})

# 2. Extract key decisions and write them to the KG
kg__add_entity({name: "auth-design-v2", type: "topic"})
kg__add_observation({entity_id: "<id>", content:
  "[DECISION] Per auth-design.pdf §3.2: sessions use JWT with 15-min expiry.
   Refresh tokens are opaque, stored in Redis with 7-day TTL.
   Rejected: session cookies (cross-subdomain incompatibility)."})
```

Future sessions can find these constraints with `kg__search_knowledge("auth session")`
without re-reading the PDF.

---

## Supported Formats Reference

| Format | Extensions | Notes |
|--------|-----------|-------|
| HTML | `.html`, `.htm`, URLs | Full conversion to Markdown |
| CSV | `.csv` | Rendered as Markdown table |
| JSON | `.json` | Pretty-printed in fenced code block |
| XML | `.xml` | Fenced code block |
| Plain text / Markdown | `.txt`, `.md` | Passed through as-is |
| Word | `.docx` | Headings, bold/italic, lists, tables |
| Excel | `.xlsx`, `.xls` | All sheets as Markdown tables |
| PowerPoint | `.pptx` | Slides with headings, lists, tables; embedded image OCR |
| PDF | `.pdf` | Text-layer extraction (no OCR) |
| Images | `.png`, `.jpg`, `.jpeg` | OCR via Tesseract (must be installed) |

OCR is enabled automatically when `tesseract` is on PATH. Without it, image files
and image-heavy PPTX slides return a descriptive error; all other formats are unaffected.
