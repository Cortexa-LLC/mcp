package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Ensure unused imports are not flagged — filepath used in TestMakeDocx_CreatesValidZip.
var _ = filepath.Ext

func newTestConverter() *Converter {
	return NewConverter()
}

// ---- ConvertFile -----------------------------------------------------------

func TestConverter_ConvertFile_HTML(t *testing.T) {
	path := writeTempFile(t, "page.html",
		`<html><body><h1>Hello</h1><p>World</p></body></html>`)
	out, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertNoErr(t, err)
	assertContains(t, out, "Hello")
}

func TestConverter_ConvertFile_CSV(t *testing.T) {
	path := writeTempFile(t, "data.csv", "A,B\n1,2\n")
	out, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertNoErr(t, err)
	assertContains(t, out, "A")
	assertContains(t, out, "|")
}

func TestConverter_ConvertFile_JSON(t *testing.T) {
	path := writeTempFile(t, "data.json", `{"key":"value"}`)
	out, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertNoErr(t, err)
	assertContains(t, out, "```json")
}

func TestConverter_ConvertFile_DOCX(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>`+
			`<w:r><w:t>Document Title</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Body text.</w:t></w:r></w:p>`)
	out, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertNoErr(t, err)
	assertContains(t, out, "# Document Title")
	assertContains(t, out, "Body text.")
}

func TestConverter_ConvertFile_XLSX(t *testing.T) {
	path := makeXLSX(t, "Sheet1", [][]string{
		{"Product", "Price"},
		{"Widget", "9.99"},
	})
	out, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertNoErr(t, err)
	assertContains(t, out, "Product")
	assertContains(t, out, "Widget")
}

func TestConverter_ConvertFile_NotFound(t *testing.T) {
	_, err := newTestConverter().ConvertFile(
		context.Background(), "/no/such/file.html", false)
	assertErr(t, err)
}

func TestConverter_ConvertFile_UnsupportedFormat(t *testing.T) {
	path := writeTempFile(t, "deck.pptx", "not a real pptx")
	_, err := newTestConverter().ConvertFile(context.Background(), path, false)
	assertErr(t, err)
}

func TestConverter_ConvertFile_TooLarge(t *testing.T) {
	path := writeTempFile(t, "big.txt", "x")

	// Override the limit to 0 so any non-empty file triggers the check.
	conv := NewConverter()
	conv.cfg.MaxFileSizeBytes = 0

	_, err := conv.ConvertFile(context.Background(), path, false)
	assertErr(t, err)
}

// ---- ConvertURI ------------------------------------------------------------

func TestConverter_ConvertURI_FileScheme(t *testing.T) {
	path := writeTempFile(t, "page.html",
		`<html><body><p>via file URI</p></body></html>`)
	uri := fmt.Sprintf("file://%s", path)
	out, err := newTestConverter().ConvertURI(context.Background(), uri, false)
	assertNoErr(t, err)
	assertContains(t, out, "via file URI")
}

func TestConverter_ConvertURI_UnsupportedScheme(t *testing.T) {
	_, err := newTestConverter().ConvertURI(
		context.Background(), "ftp://example.com/file.txt", false)
	assertErr(t, err)
}

func TestConverter_ConvertURI_InvalidURI(t *testing.T) {
	// url.Parse is permissive; a truly invalid URI still has no scheme
	_, err := newTestConverter().ConvertURI(
		context.Background(), "://bad", false)
	assertErr(t, err)
}

// ---- GetConversionInfo -----------------------------------------------------

func TestConverter_GetConversionInfo_ContainsFormats(t *testing.T) {
	out := newTestConverter().GetConversionInfo(context.Background())
	for _, fmt := range []string{"html", "csv", "json", "xml", "docx", "xlsx"} {
		assertContains(t, out, fmt)
	}
}

func TestConverter_GetConversionInfo_NotEmpty(t *testing.T) {
	out := newTestConverter().GetConversionInfo(context.Background())
	assertNotEmpty(t, out)
}

// ---- helpers ---------------------------------------------------------------

// Ensure temp dir helper is exercised (sanity check for test helpers).
func TestWriteTempFile(t *testing.T) {
	path := writeTempFile(t, "hello.txt", "hello")
	data, err := os.ReadFile(path)
	assertNoErr(t, err)
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", string(data), "hello")
	}
}

func TestMakeDocx_CreatesValidZip(t *testing.T) {
	path := makeDocx(t, `<w:p><w:r><w:t>test</w:t></w:r></w:p>`)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
	if filepath.Ext(path) != ".docx" {
		t.Errorf("expected .docx extension, got %s", filepath.Ext(path))
	}
}
