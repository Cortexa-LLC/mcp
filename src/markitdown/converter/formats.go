package converter

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
)

// nativeExts are all formats handled entirely in Go — no subprocess needed.
var nativeExts = map[string]bool{
	".html": true,
	".htm":  true,
	".csv":  true,
	".json": true,
	".xml":  true,
	".txt":  true,
	".md":   true,
	".docx": true,
	".xlsx": true,
	".xls":  true,
}

// formatConverter converts files and URLs using pure Go libraries.
type formatConverter struct {
	htmlConverter *md.Converter
}

func newFormatConverter() *formatConverter {
	return &formatConverter{
		htmlConverter: md.NewConverter("", true, nil),
	}
}

// CanConvert returns true when the file extension is handled natively.
func (c *formatConverter) CanConvert(filePath string) bool {
	return nativeExts[strings.ToLower(filepath.Ext(filePath))]
}

// SupportedFormats returns supported extensions without the leading dot.
func (c *formatConverter) SupportedFormats() []string {
	out := make([]string, 0, len(nativeExts))
	for ext := range nativeExts {
		out = append(out, strings.TrimPrefix(ext, "."))
	}
	return out
}

// ConvertFile reads filePath and returns Markdown.
func (c *formatConverter) ConvertFile(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if !nativeExts[ext] {
		return "", fmt.Errorf("unsupported format: %s", ext)
	}

	switch ext {
	case ".docx":
		return convertDOCX(filePath)
	case ".xlsx", ".xls":
		return convertXLSX(filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	switch ext {
	case ".html", ".htm":
		return c.convertHTML(string(data))
	case ".csv":
		return convertCSV(data)
	case ".json":
		return convertJSON(data)
	case ".xml":
		return convertXML(string(data))
	case ".txt", ".md":
		return string(data), nil
	default:
		return "", fmt.Errorf("unhandled extension: %s", ext)
	}
}

// ConvertURL fetches an HTTP/HTTPS URL and converts the response body to Markdown.
func (c *formatConverter) ConvertURL(url string) (string, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") || ct == "" {
		return c.convertHTML(string(body))
	}
	// Plain text / markdown served over HTTP
	return string(body), nil
}

// --- format converters -------------------------------------------------------

func (c *formatConverter) convertHTML(html string) (string, error) {
	result, err := c.htmlConverter.ConvertString(html)
	if err != nil {
		return fmt.Sprintf("```html\n%s\n```", html), nil
	}
	return result, nil
}

func convertCSV(data []byte) (string, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	records, err := r.ReadAll()
	if err != nil {
		return fmt.Sprintf("```csv\n%s\n```", string(data)), nil
	}
	if len(records) == 0 {
		return "", nil
	}

	var sb strings.Builder

	header := escapeRow(records[0])
	sb.WriteString("| " + strings.Join(header, " | ") + " |\n")

	seps := make([]string, len(header))
	for i, h := range header {
		seps[i] = strings.Repeat("-", max(len(h), 3))
	}
	sb.WriteString("| " + strings.Join(seps, " | ") + " |\n")

	for _, row := range records[1:] {
		sb.WriteString("| " + strings.Join(escapeRow(row), " | ") + " |\n")
	}
	return sb.String(), nil
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

func convertXML(xml string) (string, error) {
	return fmt.Sprintf("```xml\n%s\n```", xml), nil
}

func escapeRow(row []string) []string {
	out := make([]string, len(row))
	for i, v := range row {
		out[i] = strings.ReplaceAll(v, "|", "\\|")
	}
	return out
}
