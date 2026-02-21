package converter

// Shared test helpers for the converter package.

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// ---- assertion helpers -----------------------------------------------------

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected output to contain %q\ngot: %s", want, got)
	}
}

func assertNotEmpty(t *testing.T, got string) {
	t.Helper()
	if strings.TrimSpace(got) == "" {
		t.Error("expected non-empty output, got empty string")
	}
}

// ---- file factories --------------------------------------------------------

// writeTempFile writes content to a temp file with the given name and returns
// its path. The file is cleaned up automatically when the test ends.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	return path
}

// makeDocx builds a minimal .docx file containing the given OOXML body
// fragment and returns its path.
func makeDocx(t *testing.T, bodyXML string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("makeDocx create: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("makeDocx zip entry: %v", err)
	}

	const ns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	doc := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document ` + ns + `><w:body>` + bodyXML + `</w:body></w:document>`

	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatalf("makeDocx write: %v", err)
	}
	return path
}

// makeXLSX builds a minimal .xlsx file with one sheet and returns its path.
func makeXLSX(t *testing.T, sheet string, rows [][]string) string {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	// Rename the default sheet first so SetCellValue writes to the right name.
	if sheet != "Sheet1" {
		f.SetSheetName("Sheet1", sheet)
	}

	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			f.SetCellValue(sheet, cell, val)
		}
	}

	path := filepath.Join(t.TempDir(), "test.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("makeXLSX SaveAs: %v", err)
	}
	return path
}
