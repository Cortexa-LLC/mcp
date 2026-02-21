package converter

import (
	"testing"
)

func TestConvertDOCX_Heading1(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr>`+
			`<w:r><w:t>Main Title</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "# Main Title")
}

func TestConvertDOCX_Headings(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>H1</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>H2</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="Heading3"/></w:pPr><w:r><w:t>H3</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "# H1")
	assertContains(t, out, "## H2")
	assertContains(t, out, "### H3")
}

func TestConvertDOCX_Paragraph(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:r><w:t>Hello, world.</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Hello, world.")
}

func TestConvertDOCX_BoldRun(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>bold text</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "**bold text**")
}

func TestConvertDOCX_ItalicRun(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:r><w:rPr><w:i/></w:rPr><w:t>italic text</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "*italic text*")
}

func TestConvertDOCX_BoldItalicRun(t *testing.T) {
	path := makeDocx(t,
		`<w:p><w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>both</w:t></w:r></w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "***both***")
}

func TestConvertDOCX_ListItem(t *testing.T) {
	path := makeDocx(t,
		`<w:p>`+
			`<w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr>`+
			`<w:r><w:t>list item</w:t></w:r>`+
			`</w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "- list item")
}

func TestConvertDOCX_NestedListItem(t *testing.T) {
	path := makeDocx(t,
		`<w:p>`+
			`<w:pPr><w:numPr><w:ilvl w:val="1"/></w:numPr></w:pPr>`+
			`<w:r><w:t>nested</w:t></w:r>`+
			`</w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "  - nested")
}

func TestConvertDOCX_Table(t *testing.T) {
	path := makeDocx(t,
		`<w:tbl>`+
			`<w:tr>`+
			`<w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc>`+
			`<w:tc><w:p><w:r><w:t>Value</w:t></w:r></w:p></w:tc>`+
			`</w:tr>`+
			`<w:tr>`+
			`<w:tc><w:p><w:r><w:t>Alice</w:t></w:r></w:p></w:tc>`+
			`<w:tc><w:p><w:r><w:t>42</w:t></w:r></w:p></w:tc>`+
			`</w:tr>`+
			`</w:tbl>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Name")
	assertContains(t, out, "Value")
	assertContains(t, out, "Alice")
	assertContains(t, out, "42")
	assertContains(t, out, "|")
	assertContains(t, out, "---")
}

func TestConvertDOCX_MultipleRuns(t *testing.T) {
	path := makeDocx(t,
		`<w:p>`+
			`<w:r><w:t xml:space="preserve">Hello </w:t></w:r>`+
			`<w:r><w:rPr><w:b/></w:rPr><w:t>world</w:t></w:r>`+
			`</w:p>`)
	out, err := convertDOCX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Hello")
	assertContains(t, out, "**world**")
}

func TestConvertDOCX_NotAZip(t *testing.T) {
	path := writeTempFile(t, "bad.docx", "this is not a zip file")
	_, err := convertDOCX(path)
	assertErr(t, err)
}

func TestConvertDOCX_MissingDocumentXML(t *testing.T) {
	// Valid zip archive but contains no word/document.xml entry.
	path := writeTempFile(t, "empty.docx", "PK\x05\x06"+string(make([]byte, 18)))
	_, err := convertDOCX(path)
	assertErr(t, err)
}
