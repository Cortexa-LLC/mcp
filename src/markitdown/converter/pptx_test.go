package converter

import (
	"testing"
)

func TestConvertPPTX_TitleShape(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: `<a:r><a:t>My Title</a:t></a:r>`, bodyXML: ""},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "## My Title")
}

func TestConvertPPTX_BodyShape(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:r><a:t>Body content here</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Body content here")
}

func TestConvertPPTX_TitleAndBody(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{
			titleXML: `<a:r><a:t>Slide Title</a:t></a:r>`,
			bodyXML:  `<a:r><a:t>Slide body.</a:t></a:r>`,
		},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "## Slide Title")
	assertContains(t, out, "Slide body.")
}

func TestConvertPPTX_MultipleSlides(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: `<a:r><a:t>First Slide</a:t></a:r>`, bodyXML: ""},
		{titleXML: `<a:r><a:t>Second Slide</a:t></a:r>`, bodyXML: ""},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "## First Slide")
	assertContains(t, out, "## Second Slide")
	assertContains(t, out, "---")
}

func TestConvertPPTX_Bold(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:r><a:rPr b="1"/><a:t>bold text</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "**bold text**")
}

func TestConvertPPTX_Italic(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:r><a:rPr i="1"/><a:t>italic text</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "*italic text*")
}

func TestConvertPPTX_BoldItalic(t *testing.T) {
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:r><a:rPr b="1" i="1"/><a:t>both</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "***both***")
}

func TestConvertPPTX_ListItem(t *testing.T) {
	// lvl="1" → first-level bullet (rendered without extra indent)
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:pPr lvl="1"/><a:r><a:t>Bullet</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "- Bullet")
}

func TestConvertPPTX_NestedListItem(t *testing.T) {
	// lvl="2" → second-level bullet (two-space indent)
	path := makePPTX(t, []pptxTestSlide{
		{titleXML: "", bodyXML: `<a:pPr lvl="2"/><a:r><a:t>Nested</a:t></a:r>`},
	})
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "  - Nested")
}

func TestConvertPPTX_Table(t *testing.T) {
	tableXML := `<p:graphicFrame>` +
		`<a:graphic><a:graphicData>` +
		`<a:tbl>` +
		`<a:tr>` +
		`<a:tc><a:txBody><a:p><a:r><a:t>Header1</a:t></a:r></a:p></a:txBody></a:tc>` +
		`<a:tc><a:txBody><a:p><a:r><a:t>Header2</a:t></a:r></a:p></a:txBody></a:tc>` +
		`</a:tr>` +
		`<a:tr>` +
		`<a:tc><a:txBody><a:p><a:r><a:t>Data1</a:t></a:r></a:p></a:txBody></a:tc>` +
		`<a:tc><a:txBody><a:p><a:r><a:t>Data2</a:t></a:r></a:p></a:txBody></a:tc>` +
		`</a:tr>` +
		`</a:tbl>` +
		`</a:graphicData></a:graphic>` +
		`</p:graphicFrame>`
	path := makePPTXRaw(t, tableXML)
	out, err := convertPPTX(path)
	assertNoErr(t, err)
	assertContains(t, out, "Header1")
	assertContains(t, out, "Data2")
	assertContains(t, out, "|")
	assertContains(t, out, "---")
}

func TestConvertPPTX_FileNotFound(t *testing.T) {
	_, err := convertPPTX("/nonexistent/path/file.pptx")
	assertErr(t, err)
}

func TestConvertPPTX_NotAPPTX(t *testing.T) {
	path := writeTempFile(t, "bad.pptx", "this is not a zip archive")
	_, err := convertPPTX(path)
	assertErr(t, err)
}

func TestConvertPPTX_NoSlides(t *testing.T) {
	// Valid ZIP but no ppt/slides/slideN.xml entries.
	path := makePPTXRaw(t, "")
	_, err := convertPPTX(path)
	// makePPTXRaw with empty XML writes no slide entries → error expected.
	assertErr(t, err)
}
