# MarkItDown - Go Implementation

A high-performance document-to-Markdown converter written in Go, supporting both CLI and MCP server modes.

## Features

- **Native Go implementation** - No Python dependencies, fast startup
- **Dual mode operation** - Works as both CLI tool and MCP server
- **Comprehensive format support** - Documents, images, data files, web pages
- **Optional AI enhancement** - OpenAI Vision API integration for intelligent image description
- **Graceful fallbacks** - Degrades gracefully when optional dependencies are missing

## Supported Formats

- **Documents**: DOCX, PDF, PPTX, XLSX, XLS
- **Web**: HTML, HTM
- **Data**: CSV, JSON, XML
- **Text**: TXT, MD
- **Images**: PNG, JPG, JPEG (with OCR or AI description)

## Installation

```bash
cd src/markitdown
make install
```

This installs `markitdown` to `/usr/local/bin/markitdown`.

## Usage

### CLI Mode

```bash
# Convert a document
markitdown document.pdf

# Convert with output to file
markitdown document.docx -o output.md

# Convert URL
markitdown https://example.com/page.html

# Show help
markitdown --help
```

### MCP Server Mode

The binary automatically runs as an MCP server when invoked via stdio (e.g., by Claude Code).

Configure in `~/.claude.json`:

```json
{
  "mcpServers": {
    "markitdown": {
      "command": "/usr/local/bin/markitdown",
      "env": {
        "MARKITDOWN_ENABLE_OPENAI": "true",
        "OPENAI_API_KEY": "sk-your-key-here"
      }
    }
  }
}
```

## Configuration

All configuration is via environment variables:

### File Size Limit

```bash
export MARKITDOWN_MAX_FILE_BYTES=104857600  # 100 MB
```

Default: 50 MB (52428800 bytes)

### OpenAI Vision API Integration

Enable AI-powered image descriptions instead of basic OCR:

```bash
export MARKITDOWN_ENABLE_OPENAI=true
export OPENAI_API_KEY=sk-your-api-key-here
export MARKITDOWN_OPENAI_MODEL=gpt-4o  # optional, defaults to gpt-4o
```

**When enabled:**
- Images (PNG, JPG, JPEG) are sent to OpenAI Vision API for semantic description
- Provides better understanding of diagrams, charts, screenshots, and visual content
- Falls back to Tesseract OCR if OpenAI fails

**Cost considerations:**
- Each image costs approximately $0.01-0.05 depending on size and model
- Recommended for enterprise users with OpenAI accounts
- Use only when semantic understanding is needed (not just text extraction)

**When disabled (default):**
- Images are processed with Tesseract OCR (free, local)
- Extracts text from images but doesn't provide semantic understanding
- Requires Tesseract to be installed (`brew install tesseract` on macOS)

## MCP Tools

The server exposes three MCP tools:

### 1. convert_to_markdown

Convert a file or URL to Markdown.

**Parameters:**
- `uri` (string, required): File path or http(s):// URL

**Example:**
```json
{
  "uri": "/path/to/document.pdf"
}
```

### 2. convert_file_to_markdown

Convert a local file to Markdown (compatibility with Node.js implementation).

**Parameters:**
- `filePath` (string, required): Path to the file

**Example:**
```json
{
  "filePath": "/path/to/document.docx"
}
```

### 3. get_conversion_info

Get information about supported formats, configuration, and feature status.

**Parameters:** None

**Returns:** Markdown-formatted info including:
- Supported file formats
- Current configuration
- OpenAI status
- Tesseract OCR availability

## Dependencies

### Required
- Go 1.21+ (for building)

### Optional
- **Tesseract** - For OCR of images without OpenAI
  ```bash
  brew install tesseract  # macOS
  apt-get install tesseract-ocr  # Ubuntu/Debian
  ```

- **OpenAI API key** - For AI-enhanced image descriptions
  - Sign up at https://platform.openai.com/
  - Enterprise users: Use your corporate OpenAI account

## Examples

### Basic Usage

```bash
# Convert a PDF
markitdown report.pdf > report.md

# Convert Excel spreadsheet
markitdown data.xlsx -o data.md

# Convert PowerPoint presentation
markitdown slides.pptx -o slides.md
```

### With OpenAI Enhancement

```bash
# Export API key
export MARKITDOWN_ENABLE_OPENAI=true
export OPENAI_API_KEY=sk-your-key

# Now images will get AI descriptions
markitdown diagram.png
```

Output with OpenAI:
```markdown
**Image Description (AI-generated):**

This is a system architecture diagram showing three microservices 
connected via a message queue. The frontend service communicates 
with the API gateway, which routes requests to the authentication 
and data processing services...
```

Output without OpenAI (Tesseract OCR):
```
Frontend Service
API Gateway
Message Queue
Auth Service
Data Processor
```

## Building from Source

```bash
# Build
cd src/markitdown
make build

# Run tests
make test

# Install
make install

# Clean
make clean
```

## Architecture

- **converter/** - Format-specific converters
  - `docx.go` - Microsoft Word documents
  - `pdf.go` - PDF documents
  - `pptx.go` - PowerPoint presentations
  - `xlsx.go` - Excel spreadsheets
  - `ocr.go` - Tesseract OCR integration
  - `openai.go` - OpenAI Vision API integration
  - `formats.go` - Format dispatch and URL fetching
- **config/** - Configuration management
- **main.go** - CLI and MCP server entry point

## Comparison with Node.js Version

| Feature | Go Version | Node.js Version |
|---------|------------|-----------------|
| **Performance** | ⚡ Faster startup, no Node.js overhead | Slower startup |
| **Dependencies** | Zero runtime dependencies (except optional Tesseract/OpenAI) | Requires Node.js + npm packages |
| **Format Support** | All major formats natively | Hybrid (TypeScript + Python fallback) |
| **OpenAI Integration** | ✅ Native | ✅ Via Python |
| **Deployment** | Single binary | Node.js + dependencies |
| **Memory** | Lower memory footprint | Higher memory usage |

## Contributing

When adding new formats:
1. Add the format converter in `converter/`
2. Register it in the dispatch table in `formats.go`
3. Add tests
4. Update this README

## License

[Your license here]
