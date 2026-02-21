package converter

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestConvertXLSX_Basic(t *testing.T) {
	path := makeXLSX(t, "Sheet1", [][]string{
		{"Name", "Age", "City"},
		{"Alice", "30", "London"},
		{"Bob", "25", "Paris"},
	})
	out, err := convertXLSX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Name")
	assertContains(t, out, "Age")
	assertContains(t, out, "Alice")
	assertContains(t, out, "Bob")
	assertContains(t, out, "|")
	assertContains(t, out, "---")
}

func TestConvertXLSX_SheetHeading(t *testing.T) {
	path := makeXLSX(t, "Summary", [][]string{
		{"Col1", "Col2"},
		{"a", "b"},
	})
	out, err := convertXLSX(path)
	assertNoErr(t, err)
	assertContains(t, out, "## Summary")
}

func TestConvertXLSX_MultipleSheets(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()

	f.SetCellValue("Sheet1", "A1", "S1-Header")
	f.SetCellValue("Sheet1", "A2", "s1-value")
	f.NewSheet("Sheet2")
	f.SetCellValue("Sheet2", "A1", "S2-Header")
	f.SetCellValue("Sheet2", "A2", "s2-value")

	path := filepath.Join(t.TempDir(), "multi.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}

	out, err := convertXLSX(path)
	assertNoErr(t, err)
	assertContains(t, out, "S1-Header")
	assertContains(t, out, "S2-Header")
}

func TestConvertXLSX_PipeEscaping(t *testing.T) {
	path := makeXLSX(t, "Sheet1", [][]string{
		{"Formula", "Result"},
		{"a|b", "1"},
	})
	out, err := convertXLSX(path)
	assertNoErr(t, err)
	assertContains(t, out, `a\|b`)
}

func TestConvertXLSX_HasSeparatorRow(t *testing.T) {
	path := makeXLSX(t, "Sheet1", [][]string{
		{"X", "Y"},
		{"1", "2"},
	})
	out, err := convertXLSX(path)
	assertNoErr(t, err)
	if !strings.Contains(out, "---") {
		t.Error("expected markdown table separator (---)")
	}
}

func TestConvertXLSX_FileNotFound(t *testing.T) {
	_, err := convertXLSX("/nonexistent/path/file.xlsx")
	assertErr(t, err)
}

func TestConvertXLSX_NotAnXLSX(t *testing.T) {
	path := writeTempFile(t, "bad.xlsx", "this is not a spreadsheet")
	_, err := convertXLSX(path)
	assertErr(t, err)
}
