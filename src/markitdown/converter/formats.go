package converter

// formats.go — formatConverter: dispatches files to per-format converters and
// fetches remote URLs.
//
// Each format function owns its own I/O so that ConvertFile is a pure router
// (Single Responsibility). The dispatch table makes adding or removing a
// format a one-line change with no switch-statement sprawl.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
)

// File extension constants — the single source of truth for supported formats.
const (
	extHTML = ".html"
	extHTM  = ".htm"
	extCSV  = ".csv"
	extJSON = ".json"
	extXML  = ".xml"
	extTXT  = ".txt"
	extMD   = ".md"
	extDOCX = ".docx"
	extXLSX = ".xlsx"
	extXLS  = ".xls"
	extPPTX = ".pptx"
	extPDF  = ".pdf"
	extPNG  = ".png"
	extJPG  = ".jpg"
	extJPEG = ".jpeg"

	defaultHTTPTimeout = 30 * time.Second
)

// formatFn is the signature every per-format converter must satisfy.
type formatFn func(filePath string) (string, error)

// formatConverter dispatches files to per-format converters and fetches URLs.
// It owns the HTML converter and the HTTP client; all other format functions
// are stateless and referenced by the dispatch table.
type formatConverter struct {
	htmlConverter *md.Converter
	httpClient    *http.Client
	dispatch      map[string]formatFn
}

func newFormatConverter() *formatConverter {
	c := &formatConverter{
		htmlConverter: md.NewConverter("", true, nil),
		httpClient:    &http.Client{Timeout: defaultHTTPTimeout},
	}
	// Build the dispatch table after construction so HTML entries can close
	// over the receiver's htmlConverter.
	c.dispatch = map[string]formatFn{
		extHTML: c.convertHTMLFile,
		extHTM:  c.convertHTMLFile,
		extCSV:  convertCSVFile,
		extJSON: convertJSONFile,
		extXML:  convertXMLFile,
		extTXT:  readFileAsText,
		extMD:   readFileAsText,
		extDOCX: convertDOCX,
		extXLSX: convertXLSX,
		extXLS:  convertXLSX,
		extPPTX: convertPPTX,
		extPDF:  convertPDF,
		extPNG:  convertImage,
		extJPG:  convertImage,
		extJPEG: convertImage,
	}
	return c
}

// CanConvert reports whether filePath's extension is supported.
func (c *formatConverter) CanConvert(filePath string) bool {
	_, ok := c.dispatch[strings.ToLower(filepath.Ext(filePath))]
	return ok
}

// SupportedFormats returns supported extensions without the leading dot.
func (c *formatConverter) SupportedFormats() []string {
	out := make([]string, 0, len(c.dispatch))
	for ext := range c.dispatch {
		out = append(out, strings.TrimPrefix(ext, "."))
	}
	return out
}

// ConvertFile routes filePath to its registered format converter.
func (c *formatConverter) ConvertFile(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	fn, ok := c.dispatch[ext]
	if !ok {
		return "", fmt.Errorf("unsupported format: %s", ext)
	}
	return fn(filePath)
}

// ConvertURL fetches rawURL and converts the response to Markdown.
// The context controls request lifetime (cancellation, deadline).
func (c *formatConverter) ConvertURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request for %s: %w", rawURL, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body for %s: %w", rawURL, err)
	}

	ct := resp.Header.Get("Content-Type")
	// Treat HTML and missing Content-Type as HTML. An absent header is common
	// for test/local servers that serve HTML without content negotiation.
	if strings.Contains(ct, "text/html") || ct == "" {
		return c.convertHTMLString(string(body))
	}
	return string(body), nil
}

// --- per-format converters --------------------------------------------------
// Each function is responsible for its own file I/O so ConvertFile stays a
// pure dispatcher.

func (c *formatConverter) convertHTMLFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return c.convertHTMLString(string(data))
}

// convertHTMLString converts an HTML string to Markdown. On failure it
// returns the raw HTML in a fenced block so callers always receive usable
// content (degraded output is better than an error for a best-effort tool).
func (c *formatConverter) convertHTMLString(html string) (string, error) {
	result, err := c.htmlConverter.ConvertString(html)
	if err != nil {
		return fmt.Sprintf("```html\n%s\n```", html), nil
	}
	return result, nil
}

func convertCSVFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return convertCSV(data)
}

// convertCSV is exported at package level for direct use in tests.
func convertCSV(data []byte) (string, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	records, err := r.ReadAll()
	if err != nil {
		// Degraded output: wrap unparseable CSV in a fenced block.
		return fmt.Sprintf("```csv\n%s\n```", string(data)), nil
	}
	if len(records) == 0 {
		return "", nil
	}
	return renderMarkdownTable(records), nil
}

func convertJSONFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return convertJSON(data)
}

func convertJSON(data []byte) (string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Sprintf("```json\n%s\n```", string(data)), nil
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("```json\n%s\n```", string(data)), nil
	}
	return fmt.Sprintf("```json\n%s\n```", string(pretty)), nil
}

func convertXMLFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return fmt.Sprintf("```xml\n%s\n```", string(data)), nil
}

func readFileAsText(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return string(data), nil
}
