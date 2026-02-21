package converter

// XLSX / XLS → Markdown converter using the excelize library.
// Each sheet becomes a level-2 heading followed by a Markdown table.

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

func convertXLSX(filePath string) (string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", nil
	}

	var sb strings.Builder

	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil || len(rows) == 0 {
			continue
		}

		sb.WriteString("## " + sheet + "\n\n")
		sb.WriteString(rowsToMarkdown(rows))
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

func rowsToMarkdown(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	// Determine column count from the widest row.
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	get := func(row []string, i int) string {
		if i < len(row) {
			return strings.ReplaceAll(row[i], "|", "\\|")
		}
		return ""
	}

	colWidth := make([]int, maxCols)
	for _, row := range rows {
		for i := 0; i < maxCols; i++ {
			if w := len(get(row, i)); w > colWidth[i] {
				colWidth[i] = w
			}
		}
	}
	for i, w := range colWidth {
		if w < 3 {
			colWidth[i] = 3
		}
	}

	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	var sb strings.Builder

	writeRow := func(row []string) {
		sb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			sb.WriteString(" " + pad(get(row, i), colWidth[i]) + " |")
		}
		sb.WriteByte('\n')
	}

	// Header
	writeRow(rows[0])

	// Separator
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" " + strings.Repeat("-", colWidth[i]) + " |")
	}
	sb.WriteByte('\n')

	// Data rows
	for _, row := range rows[1:] {
		writeRow(row)
	}

	return sb.String()
}
