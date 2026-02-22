package converter

// ocr.go — Tesseract OCR integration.
//
// ocrAvailable() probes for the "tesseract" binary at call time using
// exec.LookPath, so the server degrades gracefully when Tesseract is absent.
//
// convertImage is the formatFn for .png/.jpg/.jpeg files.
// ocrImageData is a lower-level helper used by the PPTX post-pass.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lookPath is the exec.LookPath implementation used by ocrAvailable.
// Tests may replace it to simulate a missing Tesseract binary.
var lookPath = exec.LookPath

// ocrAvailable returns true when the "tesseract" binary is on PATH.
func ocrAvailable() bool {
	_, err := lookPath("tesseract")
	return err == nil
}

// convertImage is the formatFn for image files (.png, .jpg, .jpeg).
// It returns a descriptive error when Tesseract is not installed.
func convertImage(filePath string) (string, error) {
	if !ocrAvailable() {
		return "", fmt.Errorf("tesseract is not installed or not on PATH; cannot OCR %s", filepath.Base(filePath))
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return ocrImageData(data, filepath.Ext(filePath))
}

// ocrImageData runs Tesseract on raw image bytes.
// The suffix is the file extension (e.g. ".png") used when naming the temp file
// so Tesseract can detect the image format.
// If Tesseract is absent it returns ("", nil) — callers that want a hard error
// should call ocrAvailable() first.
func ocrImageData(data []byte, suffix string) (string, error) {
	if !ocrAvailable() {
		return "", nil
	}

	tmp, err := os.CreateTemp("", "markitdown-ocr-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("create temp file for OCR: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp file for OCR: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file for OCR: %w", err)
	}

	out, err := exec.Command("tesseract", tmpPath, "stdout").Output()
	if err != nil {
		return "", fmt.Errorf("tesseract: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(out)
	return strings.TrimSpace(buf.String()), nil
}
