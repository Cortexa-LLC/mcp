package converter

// DOCX → Markdown converter.
//
// DOCX files are ZIP archives containing OOXML. The main document lives at
// word/document.xml. We stream-parse that XML, tracking paragraph/run/table
// context to produce clean Markdown without any external dependencies.

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

func convertDOCX(filePath string) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()

	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in %s", filePath)
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	return parseDocumentXML(rc)
}

// ---------------------------------------------------------------------------
// Streaming XML parser
// ---------------------------------------------------------------------------

type docxParser struct {
	out strings.Builder

	// element name stack for context queries
	stack []string

	// paragraph state
	inPara    bool
	paraStyle string
	isList    bool
	listLevel int
	paraText  strings.Builder

	// run state
	inRun   bool
	runBold bool
	runItal bool
	runText strings.Builder

	// table state
	inTable  bool
	rows     [][]string
	currRow  []string
	inCell   bool
	cellText strings.Builder
}

func (p *docxParser) push(name string) { p.stack = append(p.stack, name) }
func (p *docxParser) pop() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
}
func (p *docxParser) inCtx(name string) bool {
	for _, s := range p.stack {
		if s == name {
			return true
		}
	}
	return false
}

func parseDocumentXML(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	p := &docxParser{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			p.push(t.Name.Local)
			p.handleStart(t)
		case xml.EndElement:
			p.handleEnd(t.Name.Local)
			p.pop()
		case xml.CharData:
			p.handleText(string(t))
		}
	}

	return p.out.String(), nil
}

func (p *docxParser) handleStart(t xml.StartElement) {
	switch t.Name.Local {

	// --- table ---
	case "tbl":
		p.inTable = true
		p.rows = nil
	case "tr":
		p.currRow = nil
	case "tc":
		p.inCell = true
		p.cellText.Reset()

	// --- paragraph ---
	case "p":
		p.inPara = true
		p.paraStyle = ""
		p.isList = false
		p.listLevel = 0
		p.paraText.Reset()
	case "pStyle":
		if p.inPara && p.inCtx("pPr") {
			p.paraStyle = attrVal(t, "val")
		}
	case "numPr":
		if p.inPara {
			p.isList = true
		}
	case "ilvl":
		if p.inPara && p.inCtx("numPr") {
			fmt.Sscanf(attrVal(t, "val"), "%d", &p.listLevel)
		}

	// --- run ---
	case "r":
		if p.inPara {
			p.inRun = true
			p.runBold = false
			p.runItal = false
			p.runText.Reset()
		}
	case "b":
		if p.inRun && p.inCtx("rPr") && attrVal(t, "val") != "0" {
			p.runBold = true
		}
	case "i":
		if p.inRun && p.inCtx("rPr") && attrVal(t, "val") != "0" {
			p.runItal = true
		}
	case "br":
		if p.inRun {
			p.runText.WriteByte('\n')
		}
	}
}

func (p *docxParser) handleEnd(local string) {
	switch local {

	case "r":
		if p.inRun {
			text := p.runText.String()
			if !p.inCell {
				text = applyInlineFormat(text, p.runBold, p.runItal)
				p.paraText.WriteString(text)
			}
			// When inCell, CharData was already written directly to cellText.
			p.inRun = false
		}

	case "p":
		if p.inPara {
			paraText := strings.TrimSpace(p.paraText.String())
			if paraText != "" && !p.inCell {
				p.out.WriteString(renderParagraph(paraText, p.paraStyle, p.isList, p.listLevel))
			}
			p.inPara = false
		}

	case "tc":
		if p.inTable {
			p.currRow = append(p.currRow, strings.TrimSpace(p.cellText.String()))
			p.inCell = false
			p.cellText.Reset()
		}

	case "tr":
		if p.inTable {
			p.rows = append(p.rows, p.currRow)
			p.currRow = nil
		}

	case "tbl":
		if p.inTable {
			p.out.WriteString(renderTable(p.rows))
			p.out.WriteByte('\n')
			p.inTable = false
			p.rows = nil
		}
	}
}

func (p *docxParser) handleText(text string) {
	switch {
	case p.inCell:
		p.cellText.WriteString(text)
	case p.inRun:
		p.runText.WriteString(text)
	}
}

// ---------------------------------------------------------------------------
// Rendering helpers
// ---------------------------------------------------------------------------

func renderParagraph(text, style string, isList bool, listLvl int) string {
	switch style {
	case "Heading1":
		return "# " + text + "\n\n"
	case "Heading2":
		return "## " + text + "\n\n"
	case "Heading3":
		return "### " + text + "\n\n"
	case "Heading4":
		return "#### " + text + "\n\n"
	case "Heading5":
		return "##### " + text + "\n\n"
	case "Heading6":
		return "###### " + text + "\n\n"
	}
	if isList {
		return strings.Repeat("  ", listLvl) + "- " + text + "\n"
	}
	return text + "\n\n"
}

func renderTable(rows [][]string) string {
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

	cell := func(row []string, i int) string {
		if i < len(row) {
			return strings.ReplaceAll(row[i], "|", "\\|")
		}
		return ""
	}

	var sb strings.Builder

	// Header
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" " + cell(rows[0], i) + " |")
	}
	sb.WriteByte('\n')

	// Separator
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteByte('\n')

	// Data rows
	for _, row := range rows[1:] {
		sb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			sb.WriteString(" " + cell(row, i) + " |")
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

func applyInlineFormat(text string, bold, italic bool) string {
	if text == "" {
		return text
	}
	switch {
	case bold && italic:
		return "***" + text + "***"
	case bold:
		return "**" + text + "**"
	case italic:
		return "*" + text + "*"
	}
	return text
}

func attrVal(t xml.StartElement, localName string) string {
	for _, a := range t.Attr {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}
