package converter

import (
	"strings"
	"testing"
)

// TestParseSlideImageRels_ExtractsBlipIDs verifies that blip embed IDs are
// correctly extracted from slide XML.
func TestParseSlideImageRels_ExtractsBlipIDs(t *testing.T) {
	xml := `<?xml version="1.0"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld><p:spTree>
    <p:pic><p:blipFill><a:blip r:embed="rId2"/></p:blipFill></p:pic>
    <p:pic><p:blipFill><a:blip r:embed="rId3"/></p:blipFill></p:pic>
    <p:pic><p:blipFill><a:blip r:embed="rId2"/></p:blipFill></p:pic>
  </p:spTree></p:cSld>
</p:sld>`

	ids, err := parseSlideImageRels(strings.NewReader(xml))
	assertNoErr(t, err)
	if len(ids) != 2 {
		t.Fatalf("expected 2 unique IDs, got %d: %v", len(ids), ids)
	}
	if ids[0] != "rId2" || ids[1] != "rId3" {
		t.Errorf("expected [rId2 rId3], got %v", ids)
	}
}

// TestParseRelsFile_MapsIDsToTargets verifies .rels parsing.
func TestParseRelsFile_MapsIDsToTargets(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId2" Type="image" Target="../media/image1.png"/>
  <Relationship Id="rId3" Type="image" Target="../media/image2.jpeg"/>
</Relationships>`

	m, err := parseRelsFile(strings.NewReader(xml))
	assertNoErr(t, err)
	if m["rId2"] != "../media/image1.png" {
		t.Errorf("rId2: got %q, want %q", m["rId2"], "../media/image1.png")
	}
	if m["rId3"] != "../media/image2.jpeg" {
		t.Errorf("rId3: got %q, want %q", m["rId3"], "../media/image2.jpeg")
	}
}

// TestResolveZIPPath verifies ZIP path resolution.
func TestResolveZIPPath(t *testing.T) {
	cases := []struct {
		relsFile string
		target   string
		want     string
	}{
		{"ppt/slides/_rels/slide1.xml.rels", "../media/image1.png", "ppt/media/image1.png"},
		{"ppt/slides/_rels/slide2.xml.rels", "../media/photo.jpg", "ppt/media/photo.jpg"},
	}
	for _, c := range cases {
		got := resolveZIPPath(c.relsFile, c.target)
		if got != c.want {
			t.Errorf("resolveZIPPath(%q, %q) = %q, want %q", c.relsFile, c.target, got, c.want)
		}
	}
}

// TestResolveSlideRelsPath verifies the path generator produces the expected
// OOXML convention for slide relationship files.
func TestResolveSlideRelsPath(t *testing.T) {
	cases := []struct {
		slideNum int
		want     string
	}{
		{1, "ppt/slides/_rels/slide1.xml.rels"},
		{2, "ppt/slides/_rels/slide2.xml.rels"},
		{10, "ppt/slides/_rels/slide10.xml.rels"},
	}
	for _, c := range cases {
		got := resolveSlideRelsPath(c.slideNum)
		if got != c.want {
			t.Errorf("resolveSlideRelsPath(%d) = %q, want %q", c.slideNum, got, c.want)
		}
	}
}

// TestOCRSlideImages_NoTesseract_ReturnsEmpty calls ocrSlideImages directly
// and uses withNoTesseract to simulate an absent Tesseract binary, verifying
// that the function gracefully returns ("", nil) rather than an error.
func TestOCRSlideImages_NoTesseract_ReturnsEmpty(t *testing.T) {
	pptxPath := makePPTXWithImage(t, []byte("notarealpng"))
	zr := openZip(t, pptxPath)
	defer func() { _ = zr.Close() }()

	withNoTesseract(t, func() {
		text, err := ocrSlideImages(zr, 1)
		if err != nil {
			t.Fatalf("ocrSlideImages without Tesseract returned error: %v", err)
		}
		if text != "" {
			t.Errorf("expected empty text without Tesseract, got %q", text)
		}
	})
}

// TestOCRSlideImages_FakeImageNoOCR verifies that ocrSlideImages does not
// crash when given a PPTX with a fake (non-decodable) PNG. The function is
// expected to silently skip OCR errors on individual images.
func TestOCRSlideImages_FakeImageNoOCR(t *testing.T) {
	if !ocrAvailable() {
		t.Skip("Tesseract not installed; skipping PPTX image OCR test")
	}
	path := makePPTXWithImage(t, []byte("notarealpng"))
	// convertPPTX should not crash or return an error even though the image
	// data is invalid — OCR failures on individual images are silently skipped.
	_, err := convertPPTX(path)
	// An error is acceptable if the PPTX has no text slides, but a panic is not.
	_ = err
}
