# markitdown MCP

A [Model Context Protocol](https://modelcontextprotocol.io) server that converts documents to Markdown. All core formats are handled natively in Go with no external processes required. OCR support for images and image-heavy PPTX slides requires [Tesseract](https://github.com/tesseract-ocr/tesseract).

## Supported Formats

| Format | Extension(s) | Notes |
|--------|-------------|-------|
| HTML | `.html`, `.htm` | Full conversion to Markdown |
| CSV | `.csv` | Rendered as Markdown table |
| JSON | `.json` | Pretty-printed in fenced code block |
| XML | `.xml` | Fenced code block |
| Plain text | `.txt`, `.md` | Passed through as-is |
| Word | `.docx` | Headings, bold/italic, lists, tables |
| Excel | `.xlsx`, `.xls` | All sheets as Markdown tables |
| PowerPoint | `.pptx` | Slides with headings, lists, tables; embedded images via OCR |
| PDF | `.pdf` | Text-layer extraction (no OCR) |
| Images | `.png`, `.jpg`, `.jpeg` | OCR via Tesseract (required) |

## Prerequisites

- **Go 1.24+** — [install](https://go.dev/dl/)
- **Tesseract 5+** *(optional)* — required only for `.png`/`.jpg`/`.jpeg` and PPTX embedded-image OCR

## Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/Cortexa-LLC/mcp/main/install.sh | bash
```

Or clone and run manually:

```bash
git clone https://github.com/Cortexa-LLC/mcp.git
cd mcp
./install.sh
```

## Manual Build

```bash
cd src/markitdown
go build -o markitdown-mcp .
```

The binary has no runtime dependencies beyond an optional `tesseract` on PATH.

## MCP Configuration

After building, add the server to your MCP client configuration.

### Claude Desktop (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "markitdown": {
      "command": "/absolute/path/to/markitdown-mcp"
    }
  }
}
```

Config file locations:
- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Linux**: `~/.config/Claude/claude_desktop_config.json`
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

### Claude Code (`.mcp.json` in your project)

```json
{
  "mcpServers": {
    "markitdown": {
      "command": "/absolute/path/to/markitdown-mcp"
    }
  }
}
```

## Available Tools

| Tool | Description |
|------|-------------|
| `convert_file_to_markdown` | Convert a local file by absolute path |
| `convert_to_markdown` | Convert a URI (`file://`, `http://`, `https://`) |
| `get_conversion_info` | List supported formats and active configuration |

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `MARKITDOWN_MAX_FILE_BYTES` | `52428800` (50 MiB) | Maximum accepted file size in bytes |

Example — raise limit to 200 MiB:

```json
{
  "mcpServers": {
    "markitdown": {
      "command": "/path/to/markitdown-mcp",
      "env": {
        "MARKITDOWN_MAX_FILE_BYTES": "209715200"
      }
    }
  }
}
```

## Tesseract Installation

OCR is enabled automatically when `tesseract` is on PATH. The server degrades gracefully when it is absent — image files return a descriptive error, all other formats are unaffected.

| Platform | Command |
|----------|---------|
| macOS | `brew install tesseract` |
| Ubuntu / Debian | `sudo apt-get install tesseract-ocr` |
| Fedora / RHEL | `sudo dnf install tesseract` |
| Windows | `choco install tesseract` or [download installer](https://github.com/UB-Mannheim/tesseract/wiki) |

For languages other than English, install the language packs (e.g. `brew install tesseract-lang` on macOS).

## Development

```bash
cd src/markitdown

# Run tests
make test

# Run tests with coverage
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out

# Lint
golangci-lint run ./...

# Build
make build
```
