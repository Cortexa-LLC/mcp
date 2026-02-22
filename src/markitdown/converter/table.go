package converter

// table.go — shared Markdown table renderer used by all format converters.
//
// Having a single implementation ensures consistent output across CSV, DOCX,
// and XLSX conversions (Single Responsibility: rendering is one concern owned
// by one place).

import "strings"

const minColWidth = 3 // minimum separator width for a valid Markdown table (---)

// renderMarkdownTable converts a [][]string into a GitHub-Flavored Markdown
// table. The first row is treated as the header. Each column is padded to the
// width of its widest cell (minimum minColWidth).
func renderMarkdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return ""
	}

	// Compute per-column display widths.
	widths := make([]int, maxCols)
	for i := range widths {
		widths[i] = minColWidth
	}
	for _, row := range rows {
		for i, raw := range row {
			if i < maxCols {
				if w := len(escapePipes(raw)); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	cell := func(row []string, col int) string {
		if col < len(row) {
			return escapePipes(row[col])
		}
		return ""
	}
	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}

	var sb strings.Builder

	// Header row
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" " + pad(cell(rows[0], i), widths[i]) + " |")
	}
	sb.WriteByte('\n')

	// Separator row
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" " + strings.Repeat("-", widths[i]) + " |")
	}
	sb.WriteByte('\n')

	// Data rows
	for _, row := range rows[1:] {
		sb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			sb.WriteString(" " + pad(cell(row, i), widths[i]) + " |")
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// escapePipes replaces | characters in a cell value so they do not break the
// Markdown table syntax.
func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
