package converter

import (
	_ "embed"
	"errors"
	"strings"
	"testing"
)

//go:embed testdata/white1x1.png
var white1x1PNG []byte

// withNoTesseract overrides lookPath for the duration of f so tests can
// exercise the Tesseract-absent code paths even when Tesseract is installed.
func withNoTesseract(t *testing.T, f func()) {
	t.Helper()
	orig := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = orig }()
	f()
}

func TestOCRAvailable_NoPanic(t *testing.T) {
	// Must not panic regardless of whether Tesseract is installed.
	_ = ocrAvailable()
}

func TestConvertImage_NoTesseract(t *testing.T) {
	withNoTesseract(t, func() {
		path := writeTempFile(t, "img.png", "fakepng")
		_, err := convertImage(path)
		if err == nil {
			t.Fatal("expected error when Tesseract absent, got nil")
		}
		if !strings.Contains(err.Error(), "tesseract") {
			t.Errorf("error should mention tesseract, got: %v", err)
		}
	})
}

func TestOCRImageData_NoTesseract_ReturnsEmpty(t *testing.T) {
	withNoTesseract(t, func() {
		text, err := ocrImageData([]byte("fakepng"), ".png")
		if err != nil {
			t.Fatalf("ocrImageData without Tesseract should return nil error, got: %v", err)
		}
		if text != "" {
			t.Errorf("ocrImageData without Tesseract should return empty string, got: %q", text)
		}
	})
}

func TestConvertImage_FileNotFound(t *testing.T) {
	if !ocrAvailable() {
		t.Skip("Tesseract not installed; skipping file-not-found test")
	}
	_, err := convertImage("/no/such/image.png")
	assertErr(t, err)
}

// TestOCRImageData_CreateTempFailure covers the os.CreateTemp error path by
// redirecting TMPDIR to a nonexistent directory for the duration of the call.
func TestOCRImageData_CreateTempFailure(t *testing.T) {
	if !ocrAvailable() {
		t.Skip("Tesseract not installed; skipping CreateTemp failure test")
	}
	t.Setenv("TMPDIR", t.TempDir()+"/nonexistent")
	_, err := ocrImageData([]byte("data"), ".png")
	assertErr(t, err)
}

// TestConvertImage_ValidFile exercises the full convertImage happy path:
// reads the file, calls ocrImageData, and returns a string (possibly empty
// for a blank image — Tesseract produces no text from a 1×1 white pixel).
func TestConvertImage_ValidFile(t *testing.T) {
	if !ocrAvailable() {
		t.Skip("Tesseract not installed; skipping valid-image test")
	}
	path := writeTempFile(t, "white.png", string(white1x1PNG))
	out, err := convertImage(path)
	assertNoErr(t, err)
	// A blank image yields empty OCR output — that is the expected result.
	_ = out
}

// TestOCRImageData_ValidImage exercises the ocrImageData success path
// (creates temp file, runs tesseract, trims and returns output).
func TestOCRImageData_ValidImage(t *testing.T) {
	if !ocrAvailable() {
		t.Skip("Tesseract not installed; skipping valid-image test")
	}
	text, err := ocrImageData(white1x1PNG, ".png")
	assertNoErr(t, err)
	// Blank image → empty or whitespace-only OCR output.
	if strings.TrimSpace(text) != text {
		t.Errorf("ocrImageData should TrimSpace the result, got %q", text)
	}
}
