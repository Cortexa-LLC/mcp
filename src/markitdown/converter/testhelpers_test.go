package converter

// Shared test helpers for the converter package.

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// openZip opens a ZIP file for reading and registers cleanup via t.Cleanup.
func openZip(t *testing.T, path string) *zip.ReadCloser {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("openZip %s: %v", path, err)
	}
	return zr
}

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
	if !containsStr(got, want) {
		t.Errorf("expected output to contain %q\ngot:\n%s", want, got)
	}
}

func assertNotEmpty(t *testing.T, got string) {
	t.Helper()
	if trimSpace(got) == "" {
		t.Error("expected non-empty output, got empty string")
	}
}

// ---- file factories --------------------------------------------------------

// writeTempFile writes content to a named temp file and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}
	return path
}

// makeDocx builds a minimal .docx file containing the given OOXML body XML
// fragment and returns its path.
func makeDocx(t *testing.T, bodyXML string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("makeDocx create: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("makeDocx close file: %v", cerr)
		}
	}()

	zw := zip.NewWriter(f)
	defer func() {
		if cerr := zw.Close(); cerr != nil {
			t.Errorf("makeDocx close zip: %v", cerr)
		}
	}()

	w, err := zw.Create(docxMainDocument)
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
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("makeXLSX close: %v", err)
		}
	}()

	// Rename the default sheet first so SetCellValue targets the right name.
	if sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			t.Fatalf("makeXLSX SetSheetName: %v", err)
		}
	}

	for r, row := range rows {
		for c, val := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := f.SetCellValue(sheet, cell, val); err != nil {
				t.Fatalf("makeXLSX SetCellValue: %v", err)
			}
		}
	}

	path := filepath.Join(t.TempDir(), "test.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("makeXLSX SaveAs: %v", err)
	}
	return path
}

// ---- PPTX factory ----------------------------------------------------------

// pptxTestSlide describes the XML content for a single slide in makePPTX.
// titleXML and bodyXML are raw XML fragments placed inside <a:p> elements
// within their respective placeholder shapes.
type pptxTestSlide struct {
	titleXML string // run/para XML for the title shape
	bodyXML  string // run/para XML for the body shape
}

const pptxNS = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
	`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`

// makePPTX builds a minimal .pptx file containing the given slides and
// returns its path.
func makePPTX(t *testing.T, slides []pptxTestSlide) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.pptx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("makePPTX create: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("makePPTX close file: %v", cerr)
		}
	}()

	zw := zip.NewWriter(f)
	defer func() {
		if cerr := zw.Close(); cerr != nil {
			t.Errorf("makePPTX close zip: %v", cerr)
		}
	}()

	for i, s := range slides {
		w, zerr := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i+1))
		if zerr != nil {
			t.Fatalf("makePPTX create entry: %v", zerr)
		}
		if _, werr := w.Write([]byte(buildPPTXSlideXML(s))); werr != nil {
			t.Fatalf("makePPTX write entry: %v", werr)
		}
	}
	return path
}

// makePPTXRaw builds a .pptx ZIP with a single raw spTree fragment (or no
// slides when shapeXML is empty). Used to test tables and edge cases.
func makePPTXRaw(t *testing.T, shapeXML string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "raw.pptx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("makePPTXRaw create: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("makePPTXRaw close file: %v", cerr)
		}
	}()

	zw := zip.NewWriter(f)
	defer func() {
		if cerr := zw.Close(); cerr != nil {
			t.Errorf("makePPTXRaw close zip: %v", cerr)
		}
	}()

	if shapeXML == "" {
		// No slide entries — convertPPTX should return an error.
		return path
	}

	w, zerr := zw.Create("ppt/slides/slide1.xml")
	if zerr != nil {
		t.Fatalf("makePPTXRaw create entry: %v", zerr)
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?><p:sld ` + pptxNS +
		`><p:cSld><p:spTree>` + shapeXML + `</p:spTree></p:cSld></p:sld>`
	if _, werr := w.Write([]byte(xml)); werr != nil {
		t.Fatalf("makePPTXRaw write entry: %v", werr)
	}
	return path
}

func buildPPTXSlideXML(s pptxTestSlide) string {
	var shapes string
	if s.titleXML != "" {
		shapes += `<p:sp>` +
			`<p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr>` +
			`<p:txBody><a:p>` + s.titleXML + `</a:p></p:txBody>` +
			`</p:sp>`
	}
	if s.bodyXML != "" {
		shapes += `<p:sp>` +
			`<p:nvSpPr><p:nvPr><p:ph/></p:nvPr></p:nvSpPr>` +
			`<p:txBody><a:p>` + s.bodyXML + `</a:p></p:txBody>` +
			`</p:sp>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?><p:sld ` + pptxNS +
		`><p:cSld><p:spTree>` + shapes + `</p:spTree></p:cSld></p:sld>`
}

// makePPTXWithImage creates a PPTX ZIP containing a single slide with an
// embedded image and returns its path. The image bytes are placed at
// ppt/media/image1.png and referenced from the slide via rId2.
func makePPTXWithImage(t *testing.T, imgData []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "with_image.pptx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("makePPTXWithImage create: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("makePPTXWithImage close file: %v", cerr)
		}
	}()

	zw := zip.NewWriter(f)
	defer func() {
		if cerr := zw.Close(); cerr != nil {
			t.Errorf("makePPTXWithImage close zip: %v", cerr)
		}
	}()

	// slide1.xml — references image via r:embed="rId2"
	const slideXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"` +
		` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"` +
		` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<p:cSld><p:spTree>` +
		`<p:pic><p:blipFill><a:blip r:embed="rId2"/></p:blipFill></p:pic>` +
		`</p:spTree></p:cSld></p:sld>`

	// slide1.xml.rels — maps rId2 → ../media/image1.png
	const relsXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"` +
		` Target="../media/image1.png"/>` +
		`</Relationships>`

	entries := []struct {
		name string
		data []byte
	}{
		{"ppt/slides/slide1.xml", []byte(slideXML)},
		{"ppt/slides/_rels/slide1.xml.rels", []byte(relsXML)},
		{"ppt/media/image1.png", imgData},
	}

	for _, e := range entries {
		w, zerr := zw.Create(e.name)
		if zerr != nil {
			t.Fatalf("makePPTXWithImage create entry %s: %v", e.name, zerr)
		}
		if _, werr := w.Write(e.data); werr != nil {
			t.Fatalf("makePPTXWithImage write entry %s: %v", e.name, werr)
		}
	}
	return path
}

// ---- stdlib shims (avoid importing strings in this file) -------------------

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (sub == "" || findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
