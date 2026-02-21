package converter

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Cortexa-LLC/mcp/src/markitdown/config"
)

// Converter routes all conversions through the native Go backend.
// HTTP/HTTPS URIs are fetched and converted as HTML.
// file:// URIs are resolved to local paths.
type Converter struct {
	native *formatConverter
	cfg    *config.Config
}

// NewConverter creates a Converter using environment-driven config.
func NewConverter() *Converter {
	return &Converter{
		native: newFormatConverter(),
		cfg:    config.Load(),
	}
}

// ConvertFile converts a local file path to Markdown.
func (c *Converter) ConvertFile(_ context.Context, filePath string, _ bool) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", filePath)
	}
	if info.Size() > c.cfg.MaxFileSizeBytes {
		return "", fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), c.cfg.MaxFileSizeBytes)
	}
	if !c.native.CanConvert(filePath) {
		return "", fmt.Errorf("unsupported format: %s", filePath)
	}
	return c.native.ConvertFile(filePath)
}

// ConvertURI converts a URI to Markdown.
// Supported schemes: file://, http://, https://
func (c *Converter) ConvertURI(ctx context.Context, uri string, enablePlugins bool) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid URI: %s", uri)
	}

	switch u.Scheme {
	case "file":
		return c.ConvertFile(ctx, u.Path, enablePlugins)
	case "http", "https":
		return c.native.ConvertURL(uri)
	default:
		return "", fmt.Errorf("unsupported URI scheme: %q (expected file, http, or https)", u.Scheme)
	}
}

// GetConversionInfo returns a Markdown summary of supported formats and config.
func (c *Converter) GetConversionInfo(_ context.Context) string {
	fmts := c.native.SupportedFormats()
	sort.Strings(fmts)

	return fmt.Sprintf(`# MarkItDown Conversion Info

## Supported Formats (native Go)
%s

## Configuration
- Max file size: %d MB
- Timeout: not applicable (no subprocess)`,
		"- "+strings.Join(fmts, "\n- "),
		c.cfg.MaxFileSizeBytes>>20,
	)
}
