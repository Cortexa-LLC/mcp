package knowledge

// PDF extractor for the knowledge graph.
//
// What gets indexed
// -----------------
//   Each .pdf file    → "file" entity (keyed by relative path)
//   Extracted text    → one or more observations on that entity
//                       chunked at ≤ 2 000 characters so each chunk
//                       stays within embedding-model context limits.
//
// Text extraction strategy (two-tier)
// ------------------------------------
//  1. pdftotext (poppler) – preferred; better layout/encoding fidelity.
//     Install: macOS: brew install poppler
//              Linux: apt-get install poppler-utils  (Debian/Ubuntu)
//                     dnf install poppler-utils      (Fedora/RHEL)
//  2. github.com/ledongthuc/pdf – pure-Go fallback, zero system deps.
//     Used automatically when pdftotext is not on PATH.
//
// Both extractors silently return empty text for encrypted / image-only PDFs;
// such files are skipped (no entity created) rather than producing empty noise.

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	ledpdf "github.com/ledongthuc/pdf"

	"github.com/google/uuid"
)

const (
	// pdfChunkMaxBytes is the maximum UTF-8 byte length of a single observation.
	// Chosen to stay comfortably inside typical embedding-model token limits
	// (~512 tokens ≈ ~2000 bytes of dense prose).
	pdfChunkMaxBytes = 2000

	// pdfMinChunkLen discards chunks that are almost entirely whitespace / noise.
	pdfMinChunkLen = 20
)

// extractTextPdftotext tries to extract text from the given PDF using the
// pdftotext binary (part of poppler-utils).  Returns ("", false, nil) when
// the binary is not on PATH so the caller can fall back to the Go library.
func extractTextPdftotext(absPath string) (text string, ok bool, err error) {
	bin, lookErr := exec.LookPath("pdftotext")
	if lookErr != nil {
		return "", false, nil // not installed – caller should try fallback
	}

	// Run: pdftotext -layout <file> -
	// The trailing "-" sends output to stdout instead of a sidecar file.
	cmd := exec.Command(bin, "-layout", absPath, "-") //nolint:gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		// pdftotext exits non-zero for encrypted/corrupt PDFs; treat as empty.
		return "", true, nil
	}
	return stdout.String(), true, nil
}

// extractTextGoLib extracts text from the given PDF using the pure-Go
// github.com/ledongthuc/pdf library.  Returns ("", nil) when the file has no
// extractable text (e.g. image-only or encrypted).
func extractTextGoLib(absPath string) (string, error) {
	f, r, err := ledpdf.Open(absPath)
	if err != nil {
		return "", nil // treat unreadable PDF as no content
	}
	defer f.Close()

	var sb strings.Builder
	for pageIdx := 1; pageIdx <= r.NumPage(); pageIdx++ {
		page := r.Page(pageIdx)
		if page.V.IsNull() {
			continue
		}
		rows, pageErr := page.GetTextByRow()
		if pageErr != nil {
			continue
		}
		for _, row := range rows {
			for _, word := range row.Content {
				sb.WriteString(word.S)
				sb.WriteByte(' ')
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String(), nil
}

// processPDFFile extracts text from a PDF, creates a "file" entity for it,
// and appends one observation per text chunk to *observations.
//
// Extraction strategy:
//  1. pdftotext (poppler) – preferred; better layout fidelity.
//  2. github.com/ledongthuc/pdf – pure-Go fallback when pdftotext absent.
//
// Returns nil (no error) when:
//   - the file is password-protected or contains no extractable text
func (idx *Indexer) processPDFFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	observations *[]obsRecord,
	stats *IndexStats,
) error {
	var rawText string

	// Tier 1: pdftotext
	extracted, available, err := extractTextPdftotext(absPath)
	if err != nil {
		return err
	}
	if available {
		rawText = extracted
	} else {
		// Tier 2: pure-Go fallback
		goText, goErr := extractTextGoLib(absPath)
		if goErr != nil {
			return goErr
		}
		rawText = goText
	}

	// Normalise whitespace: collapse long blank-line runs, trim leading/trailing.
	text := normaliseWhitespace(rawText)

	// If nothing useful was extracted (image-only PDF, etc.) skip.
	if utf8.RuneCountInString(text) < pdfMinChunkLen {
		return nil
	}

	// --- Create the "file" entity for this PDF ---------------------------------
	now := time.Now().UTC()
	entityID := uuid.NewString()

	if !writeEntity(entities, seenEntities, entityID, relPath, "file", idx.projectID, now) {
		// Already seen – shouldn't happen in a fresh walk but handle gracefully.
		return nil
	}
	stats.EntitiesCreated++

	// --- Chunk the text and add one observation per chunk ----------------------
	chunks := chunkText(text, pdfChunkMaxBytes)
	for _, chunk := range chunks {
		if utf8.RuneCountInString(chunk) < pdfMinChunkLen {
			continue
		}
		*observations = append(*observations, obsRecord{
			id:       uuid.NewString(),
			entityID: entityID,
			content:  chunk,
			created:  now,
		})
	}

	_ = relations // reserved for future inter-document CONTAINS relations

	return nil
}

// batchCreateObservations bulk-loads observations via NDJSON COPY FROM, falling
// back to the per-row Store.CreateObservation path when the bulk path fails.
func (idx *Indexer) batchCreateObservations(observations []obsRecord) error {
	if len(observations) == 0 {
		return nil
	}
	return bulkLoadExecObservations(idx.store, observations)
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

// normaliseWhitespace collapses consecutive blank lines (≥ 3 in a row)
// into at most two, strips trailing spaces from lines, and trims the whole
// string.
func normaliseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blanks := 0
	for _, l := range lines {
		trimmed := strings.TrimRight(l, " \t\r")
		if trimmed == "" {
			blanks++
			if blanks <= 2 {
				out = append(out, "")
			}
		} else {
			blanks = 0
			out = append(out, trimmed)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// chunkText splits text into slices of at most maxBytes UTF-8 bytes.
// Splits are always made at a newline boundary where possible, and never
// in the middle of a multi-byte rune.
func chunkText(text string, maxBytes int) []string {
	if len(text) <= maxBytes {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxBytes {
			chunks = append(chunks, remaining)
			break
		}

		// Find split point: prefer last newline within the limit.
		seg := remaining[:maxBytes]

		// Ensure we don't cut a multi-byte rune.
		for !utf8.ValidString(seg) && len(seg) > 0 {
			seg = seg[:len(seg)-1]
		}

		// Walk back to the last newline so we don't split mid-sentence.
		splitAt := strings.LastIndexByte(seg, '\n')
		if splitAt < maxBytes/2 {
			// No good newline in the second half – just split at max.
			splitAt = len(seg)
		} else {
			splitAt++ // include the newline in the chunk
		}

		chunk := strings.TrimSpace(remaining[:splitAt])
		if chunk != "" {
			chunks = append(chunks, fmt.Sprintf("[PDF chunk %d] %s", len(chunks)+1, chunk))
		}
		remaining = strings.TrimSpace(remaining[splitAt:])
	}

	return chunks
}
