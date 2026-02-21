package converter

import (
	"strings"
	"testing"
	"time"
)

// ---- CanConvert / format detection -----------------------------------------

func TestCanConvert_NativeFormats(t *testing.T) {
	c := newFormatConverter()
	native := []string{
		"doc.html", "doc.htm", "doc.csv", "doc.json",
		"doc.xml", "doc.txt", "doc.md", "doc.docx", "doc.xlsx", "doc.xls",
	}
	for _, name := range native {
		if !c.CanConvert(name) {
			t.Errorf("CanConvert(%q) = false, want true", name)
		}
	}
}

func TestCanConvert_NonNativeFormats(t *testing.T) {
	c := newFormatConverter()
	nonNative := []string{"doc.pdf", "doc.pptx", "doc.ppt", "doc.mp3", "doc.wav", "doc.jpg"}
	for _, name := range nonNative {
		if c.CanConvert(name) {
			t.Errorf("CanConvert(%q) = true, want false", name)
		}
	}
}

func TestCanConvert_CaseInsensitive(t *testing.T) {
	c := newFormatConverter()
	for _, name := range []string{"DOC.HTML", "doc.CSV", "doc.JSON", "doc.DOCX"} {
		if !c.CanConvert(name) {
			t.Errorf("CanConvert(%q) = false, want true (should be case-insensitive)", name)
		}
	}
}

func TestCanConvert_NoExtension(t *testing.T) {
	c := newFormatConverter()
	if c.CanConvert("README") {
		t.Error("CanConvert(\"README\") = true, want false")
	}
	if c.CanConvert("") {
		t.Error("CanConvert(\"\") = true, want false")
	}
}

func TestSupportedFormats_ContainsExpected(t *testing.T) {
	c := newFormatConverter()
	fmts := c.SupportedFormats()

	want := []string{"html", "csv", "json", "xml", "txt", "md", "docx", "xlsx"}
	set := make(map[string]bool, len(fmts))
	for _, f := range fmts {
		set[f] = true
	}
	for _, f := range want {
		if !set[f] {
			t.Errorf("SupportedFormats() missing %q", f)
		}
	}
}

// ---- CSV -------------------------------------------------------------------

func TestConvertCSV_Basic(t *testing.T) {
	path := writeTempFile(t, "data.csv", "Name,Age,City\nAlice,30,London\nBob,25,Paris\n")
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "Name")
	assertContains(t, out, "Age")
	assertContains(t, out, "Alice")
	assertContains(t, out, "Bob")
	// Must be a Markdown table
	assertContains(t, out, "|")
	assertContains(t, out, "---")
}

func TestConvertCSV_PipeEscaping(t *testing.T) {
	path := writeTempFile(t, "pipes.csv", "Formula,Value\na|b,1\n")
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, `a\|b`)
}

func TestConvertCSV_Empty(t *testing.T) {
	path := writeTempFile(t, "empty.csv", "")
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty output for empty CSV, got %q", out)
	}
}

// ---- JSON ------------------------------------------------------------------

func TestConvertJSON_Valid(t *testing.T) {
	path := writeTempFile(t, "data.json", `{"name":"Alice","age":30}`)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "```json")
	assertContains(t, out, `"name"`)
	assertContains(t, out, `"Alice"`)
}

func TestConvertJSON_Array(t *testing.T) {
	path := writeTempFile(t, "arr.json", `[1,2,3]`)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "```json")
}

func TestConvertJSON_Invalid(t *testing.T) {
	// Invalid JSON should still return a code block, not an error.
	path := writeTempFile(t, "bad.json", `{not valid json}`)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "```json")
}

// ---- XML -------------------------------------------------------------------

func TestConvertXML_WrapsInCodeBlock(t *testing.T) {
	path := writeTempFile(t, "data.xml", `<root><item>hello</item></root>`)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "```xml")
	assertContains(t, out, "<item>hello</item>")
}

// ---- TXT / MD --------------------------------------------------------------

func TestConvertTXT_Passthrough(t *testing.T) {
	content := "Hello, world!\nLine two."
	path := writeTempFile(t, "note.txt", content)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	if out != content {
		t.Errorf("got %q, want %q", out, content)
	}
}

func TestConvertMD_Passthrough(t *testing.T) {
	content := "# Heading\n\nParagraph."
	path := writeTempFile(t, "readme.md", content)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	if out != content {
		t.Errorf("got %q, want %q", out, content)
	}
}

// ---- HTML ------------------------------------------------------------------

func TestConvertHTML_Basic(t *testing.T) {
	path := writeTempFile(t, "page.html",
		`<html><body><h1>Title</h1><p>Hello world.</p></body></html>`)
	c := newFormatConverter()
	out, err := c.ConvertFile(path)
	assertNoErr(t, err)
	assertContains(t, out, "Title")
	assertContains(t, out, "Hello world")
}

// ---- unsupported -----------------------------------------------------------

func TestConvertFile_UnsupportedFormat(t *testing.T) {
	path := writeTempFile(t, "doc.pdf", "not a real pdf")
	c := newFormatConverter()
	_, err := c.ConvertFile(path)
	assertErr(t, err)
}

// ---- performance -----------------------------------------------------------

func TestConvertNativeFormats_Performance(t *testing.T) {
	// Native formats should convert a small file well under 100 ms.
	c := newFormatConverter()
	path := writeTempFile(t, "perf.csv", "A,B,C\n1,2,3\n4,5,6\n")

	start := time.Now()
	_, err := c.ConvertFile(path)
	elapsed := time.Since(start)

	assertNoErr(t, err)
	if elapsed > 100*time.Millisecond {
		t.Errorf("native conversion took %v, want < 100ms", elapsed)
	}
}
