package converter

// converter.go — public API for the converter package.
//
// FileConverter is the interface callers (including main.go) depend on.
// Converter is the concrete implementation; it composes a formatConverter
// for all file I/O and delegates URI routing to the appropriate method.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Cortexa-LLC/mcp/src/markitdown/config"
)

// URI scheme constants — the single source of truth for supported schemes.
const (
	schemeFile  = "file"
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// FileConverter is the minimal interface for converting files and URIs to
// Markdown. Accepting this interface (rather than *Converter) allows callers
// to inject test doubles and respects the Dependency Inversion principle.
type FileConverter interface {
	ConvertFile(ctx context.Context, filePath string) (string, error)
	ConvertURI(ctx context.Context, uri string) (string, error)
	GetConversionInfo(ctx context.Context) string
}

// Converter implements FileConverter using pure Go format converters.
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
func (c *Converter) ConvertFile(_ context.Context, filePath string) (string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", filePath, err)
	}
	if info.Size() > c.cfg.MaxFileSizeBytes {
		return "", fmt.Errorf("file too large: %d bytes (limit %d bytes / %d MB)",
			info.Size(), c.cfg.MaxFileSizeBytes, c.cfg.MaxFileSizeMB())
	}
	if !c.native.CanConvert(filePath) {
		return "", fmt.Errorf("unsupported format: %s", filePath)
	}
	return c.native.ConvertFile(filePath)
}

// ConvertURI converts a URI to Markdown.
// Supported schemes: file://, http://, https://
func (c *Converter) ConvertURI(ctx context.Context, uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid URI %q: %w", uri, err)
	}

	switch u.Scheme {
	case schemeFile:
		return c.ConvertFile(ctx, u.Path)
	case schemeHTTP, schemeHTTPS:
		return c.native.ConvertURL(ctx, uri)
	default:
		return "", fmt.Errorf("unsupported URI scheme %q (supported: file, http, https)", u.Scheme)
	}
}

// GetConversionInfo returns a Markdown summary of supported formats and config.
func (c *Converter) GetConversionInfo(_ context.Context) string {
	fmts := c.native.SupportedFormats()
	sort.Strings(fmts)

	return fmt.Sprintf(`# MarkItDown Conversion Info

## Supported Formats (native Go — no subprocess required)
- %s

## Configuration
- Max file size: %d MB (override with %s)`,
		strings.Join(fmts, "\n- "),
		c.cfg.MaxFileSizeMB(),
		config.EnvMaxFileBytes,
	)
}
