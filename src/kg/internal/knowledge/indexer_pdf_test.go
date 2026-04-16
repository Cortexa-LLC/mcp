package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pure unit tests – no filesystem or database required
// ---------------------------------------------------------------------------

func TestNormaliseWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "collapses excess blank lines",
			input: "Line A\n\n\n\n\nLine B",
			want:  "Line A\n\n\nLine B",
		},
		{
			name:  "trims trailing spaces on each line",
			input: "Line A   \nLine B\t",
			want:  "Line A\nLine B",
		},
		{
			name:  "trims leading and trailing whitespace",
			input: "\n\nHello\n\n",
			want:  "Hello",
		},
		{
			name:  "preserves two consecutive blank lines",
			input: "Paragraph A\n\nParagraph B",
			want:  "Paragraph A\n\nParagraph B",
		},
		{
			name:  "empty input stays empty",
			input: "",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normaliseWhitespace(tc.input)
			if got != tc.want {
				t.Errorf("normaliseWhitespace(%q)\n  got  %q\n  want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestChunkText_ShortText(t *testing.T) {
	text := "Hello, world!"
	chunks := chunkText(text, 2000)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("chunk content mismatch: got %q, want %q", chunks[0], text)
	}
}

func TestChunkText_SplitsOnNewline(t *testing.T) {
	// Build a string that is just over 100 bytes with a newline near the middle.
	line := strings.Repeat("a", 40) + "\n" + strings.Repeat("b", 70)
	chunks := chunkText(line, 100)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Each chunk must not exceed the raw byte limit (prefix aside).
	for i, chunk := range chunks {
		if len(chunk) > 200 {
			t.Errorf("chunk[%d] suspiciously large (len=%d)", i, len(chunk))
		}
	}
	// The reconstructed content (including chunk-number prefix) should
	// contain all the original letters.
	combined := strings.Join(chunks, " ")
	if !strings.Contains(combined, strings.Repeat("a", 20)) {
		t.Error("first segment of 'a's missing from chunks")
	}
	if !strings.Contains(combined, strings.Repeat("b", 20)) {
		t.Error("second segment of 'b's missing from chunks")
	}
}

func TestChunkText_NeverSplitsRune(t *testing.T) {
	// Multi-byte rune (Japanese) that should never be cut mid-codepoint.
	s := strings.Repeat("あ", 100) // each 'あ' is 3 bytes → 300 bytes total
	chunks := chunkText(s, 100)
	for _, chunk := range chunks {
		// All chunks must be valid UTF-8.
		for _, r := range chunk {
			if r == '\uFFFD' {
				t.Error("invalid UTF-8 rune (replacement character) found in chunk")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Integration tests – require pdftotext on PATH (or Go library fallback)
// ---------------------------------------------------------------------------

// TestPDFIndexer_GracefulSkip verifies that processPDFFile does not return
// an error when pdftotext is unavailable.  With the Go-library fallback the
// fake "%PDF-1.4 fake" file has no extractable text, so no entity is produced,
// but the call must still succeed.
func TestPDFIndexer_GracefulSkip(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err == nil {
		// pdftotext is installed – skip because this test covers the absent-binary
		// path and the fake file would also be handed off to pdftotext directly.
		t.Skip("pdftotext is installed; skipping graceful-skip test")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err := NewIndexer(store, "test-project", tmpDir)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}

	var entities []entityRecord
	seenEntities := make(map[string]bool)
	var relations []relationRecord
	var observations []obsRecord
	stats := &IndexStats{}

	// Write a fake PDF file (minimal content; Go library will find no text).
	fakePath := filepath.Join(tmpDir, "empty.pdf")
	if err := os.WriteFile(fakePath, []byte("%PDF-1.4 fake"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Must not return an error even when pdftotext is absent.
	err = idx.processPDFFile(fakePath, "empty.pdf", &entities, seenEntities, &relations, &observations, stats)
	if err != nil {
		t.Errorf("processPDFFile returned error when pdftotext missing: %v", err)
	}
	// The fake file has no extractable text, so no entity should be created.
	if len(entities) != 0 {
		t.Errorf("expected 0 entities for unreadable PDF, got %d", len(entities))
	}
	if len(observations) != 0 {
		t.Errorf("expected 0 observations for unreadable PDF, got %d", len(observations))
	}
}

// TestExtractTextGoLib_ReturnsTextFromRealPDF verifies that extractTextGoLib
// can pull text out of a valid minimal PDF created by buildMinimalPDF.
// This test exercises the pure-Go fallback in isolation.
func TestExtractTextGoLib_ReturnsTextFromRealPDF(t *testing.T) {
	const want = "Hello GoLib World"
	pdfBytes := buildMinimalPDF(want)

	tmpDir := t.TempDir()
	pdfPath := filepath.Join(tmpDir, "golib.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}

	text, err := extractTextGoLib(pdfPath)
	if err != nil {
		t.Fatalf("extractTextGoLib: %v", err)
	}
	// The Go library returns text; it may have extra spaces/newlines, so we
	// check containment after normalisation.
	norm := normaliseWhitespace(text)
	if !strings.Contains(norm, "Hello") || !strings.Contains(norm, "GoLib") {
		t.Errorf("expected extracted text to contain %q, got %q", want, norm)
	}
}

// TestExtractTextGoLib_FakePDF verifies that an unreadable/corrupt PDF does
// not cause an error (returns empty string gracefully).
func TestExtractTextGoLib_FakePDF(t *testing.T) {
	tmpDir := t.TempDir()
	fakePath := filepath.Join(tmpDir, "fake.pdf")
	if err := os.WriteFile(fakePath, []byte("%PDF-1.4 not a real pdf"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	text, err := extractTextGoLib(fakePath)
	if err != nil {
		t.Errorf("extractTextGoLib returned error for corrupt PDF: %v", err)
	}
	// Should return empty string without crashing.
	norm := normaliseWhitespace(text)
	_ = norm // may be empty – that is acceptable
}

// TestPDFIndexer_EndToEnd verifies the full pipeline using a real PDF when
// pdftotext is available.  Skipped in short mode or when pdftotext is absent.
func TestPDFIndexer_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not on PATH – skipping PDF end-to-end test")
	}

	// Build a minimal but valid single-page PDF with readable text.
	pdfContent := buildMinimalPDF("Hello PDF World  This is a KG indexer test document.")
	tmpDir := t.TempDir()
	pdfPath := filepath.Join(tmpDir, "test.pdf")
	if err := os.WriteFile(pdfPath, pdfContent, 0644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}

	// Use runIndexer to exercise the full end-to-end path through Index().
	store, stats := runIndexer(t, tmpDir, filepath.Join(tmpDir, "test.db"))

	// The PDF file should have been scanned.
	if stats.FilesScanned < 1 {
		t.Errorf("expected FilesScanned >= 1, got %d", stats.FilesScanned)
	}

	// A "file" entity keyed by the relative path should exist.
	if !entityExistsByName(t, store, "test.pdf", "file") {
		t.Error("expected 'test.pdf' file entity to be created")
	}
}

// ---------------------------------------------------------------------------
// Minimal PDF builder
// ---------------------------------------------------------------------------

// buildMinimalPDF builds the smallest valid PDF containing text that pdftotext
// can extract.  It is sufficient for integration tests; it is NOT a
// production-quality PDF generator.
//
// Structure:
//   - 1 page, 8.5×11 inch page size
//   - Type1 Helvetica font (built-in; no font embedding required)
//   - Inline text string via BT/ET operators
func buildMinimalPDF(text string) []byte {
	// Escape parentheses in the text string to avoid PDF syntax errors.
	escaped := strings.NewReplacer("(", "\\(", ")", "\\)").Replace(text)

	stream := "BT /F1 12 Tf 50 750 Td (" + escaped + ") Tj ET"
	streamLen := len(stream)

	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")

	// Object 1: Catalog
	off1 := sb.Len()
	sb.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: Pages
	off2 := sb.Len()
	sb.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	// Object 3: Page
	off3 := sb.Len()
	sb.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]")
	sb.WriteString(" /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	// Object 4: Content stream
	off4 := sb.Len()
	sb.WriteString("4 0 obj\n")
	sb.WriteString("<< /Length ")
	lenStr := itoa(streamLen)
	sb.WriteString(lenStr)
	sb.WriteString(" >>\nstream\n")
	sb.WriteString(stream)
	sb.WriteString("\nendstream\nendobj\n")

	// Object 5: Font
	off5 := sb.Len()
	sb.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	// Cross-reference table
	xrefOff := sb.Len()
	sb.WriteString("xref\n0 6\n")
	sb.WriteString("0000000000 65535 f \n")
	sb.WriteString(formatXrefEntry(off1) + "\n")
	sb.WriteString(formatXrefEntry(off2) + "\n")
	sb.WriteString(formatXrefEntry(off3) + "\n")
	sb.WriteString(formatXrefEntry(off4) + "\n")
	sb.WriteString(formatXrefEntry(off5) + "\n")

	sb.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	sb.WriteString("startxref\n")
	sb.WriteString(itoa(xrefOff))
	sb.WriteString("\n%%EOF\n")

	return []byte(sb.String())
}

func formatXrefEntry(offset int) string {
	s := itoa(offset)
	for len(s) < 10 {
		s = "0" + s
	}
	return s + " 00000 n "
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
