package converter

// pdf.go — PDF → Markdown via pure-Go text-layer extraction.
//
// Uses github.com/ledongthuc/pdf for parsing. Only the embedded text layer
// is extracted; scanned (image-only) PDFs require OCR and are not handled here.

import (
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// convertPDF is the formatFn for .pdf files.
func convertPDF(filePath string) (string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open pdf %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	numPages := r.NumPage()
	fonts := make(map[string]*pdf.Font)
	var parts []string

	for i := 1; i <= numPages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}

		for _, name := range p.Fonts() {
			if _, ok := fonts[name]; !ok {
				f2 := p.Font(name)
				fonts[name] = &f2
			}
		}

		text, pageErr := p.GetPlainText(fonts)
		if pageErr != nil {
			return "", fmt.Errorf("read pdf page %d: %w", i, pageErr)
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}
