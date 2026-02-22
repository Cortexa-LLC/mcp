package converter

// xlsx.go — XLSX/XLS → Markdown using the excelize library.
// Each sheet becomes a level-2 heading followed by a Markdown table
// rendered by the shared renderMarkdownTable function from table.go.

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

const xlsxSheetHeading = "## " // Markdown heading level for each sheet name

func convertXLSX(filePath string) (string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return "", fmt.Errorf("open xlsx %s: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", nil
	}

	var sb strings.Builder

	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return sb.String(), fmt.Errorf("read sheet %q in %s: %w", sheet, filePath, err)
		}
		if len(rows) == 0 {
			continue
		}

		sb.WriteString(xlsxSheetHeading + sheet + "\n\n")
		sb.WriteString(renderMarkdownTable(rows))
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}
