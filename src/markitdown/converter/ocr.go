package converter

// ocr.go — Image-to-text conversion with Tesseract OCR and optional OpenAI Vision API.
//
// ocrAvailable() probes for the "tesseract" binary at call time using
// exec.LookPath, so the server degrades gracefully when Tesseract is absent.
//
// When OpenAI integration is enabled via config, convertImage first attempts
// AI-powered image description, falling back to Tesseract OCR if needed.
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

// openaiClient is set by setOpenAIClient and used by convertImageData.
// It's a package-level variable so format functions can access it.
var openaiClient *OpenAIClient

// setOpenAIClient configures the OpenAI client for image enhancement.
// Pass nil to disable OpenAI integration.
func setOpenAIClient(client *OpenAIClient) {
	openaiClient = client
}

// lookPath is the exec.LookPath implementation used by ocrAvailable.
// Tests may replace it to simulate a missing Tesseract binary.
var lookPath = exec.LookPath

// ocrAvailable returns true when the "tesseract" binary is on PATH.
func ocrAvailable() bool {
	_, err := lookPath("tesseract")
	return err == nil
}

// convertImage is the formatFn for image files (.png, .jpg, .jpeg).
// It uses OpenAI Vision API if enabled, otherwise falls back to Tesseract OCR.
func convertImage(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filePath, err)
	}
	return convertImageData(data, filepath.Ext(filePath))
}

// convertImageData converts image bytes to markdown text.
// It tries OpenAI first (if enabled), then Tesseract, providing the best available method.
func convertImageData(data []byte, ext string) (string, error) {
	// Try OpenAI Vision API if configured
	if openaiClient != nil {
		description, err := openaiClient.DescribeImage(data, ext)
		if err == nil {
			return fmt.Sprintf("**Image Description (AI-generated):**\n\n%s", description), nil
		}
		// Log the error but continue to fallback
		fmt.Fprintf(os.Stderr, "Warning: OpenAI image description failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Falling back to Tesseract OCR...\n")
	}

	// Fall back to Tesseract OCR
	if !ocrAvailable() {
		return "", fmt.Errorf("tesseract is not installed or not on PATH; cannot OCR image")
	}
	return ocrImageData(data, ext)
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
