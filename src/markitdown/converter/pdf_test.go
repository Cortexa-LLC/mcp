package converter

import (
	_ "embed"
	"testing"
)

//go:embed testdata/minimal.pdf
var minimalPDF []byte

func TestConvertPDF_BasicText(t *testing.T) {
	path := writeTempFile(t, "test.pdf", string(minimalPDF))
	out, err := convertPDF(path)
	assertNoErr(t, err)
	assertContains(t, out, "Hello PDF")
}

func TestConvertPDF_FileNotFound(t *testing.T) {
	_, err := convertPDF("/no/such/file.pdf")
	assertErr(t, err)
}

func TestConvertPDF_NotAPDF(t *testing.T) {
	path := writeTempFile(t, "fake.pdf", "this is not a PDF")
	_, err := convertPDF(path)
	assertErr(t, err)
}
