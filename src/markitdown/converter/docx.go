package converter

// docx.go — DOCX → Markdown via streaming OOXML XML parser.
//
// DOCX files are ZIP archives. The main document is at docxMainDocument.
// We stream-parse its XML, maintaining a lightweight state machine for
// paragraphs, runs, and tables, then emit Markdown.

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// docxMainDocument is the path of the primary document XML inside the ZIP.
const docxMainDocument = "word/document.xml"

// headingPrefixes maps OOXML paragraph style names to Markdown heading prefixes.
// Using a map eliminates the repetitive switch and makes adding heading levels trivial.
var headingPrefixes = map[string]string{
	"Heading1": "# ",
	"Heading2": "## ",
	"Heading3": "### ",
	"Heading4": "#### ",
	"Heading5": "##### ",
	"Heading6": "###### ",
}

// paragraphBreak is appended after every non-list paragraph.
const paragraphBreak = "\n\n"

func convertDOCX(filePath string) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open docx %s: %w", filePath, err)
	}
	defer func() { _ = zr.Close() }()

	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == docxMainDocument {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("%s not found in %s", docxMainDocument, filePath)
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("open %s: %w", docxMainDocument, err)
	}
	defer func() { _ = rc.Close() }()

	return parseDocumentXML(rc)
}

// ---------------------------------------------------------------------------
// Streaming XML state machine
// ---------------------------------------------------------------------------

type docxParser struct {
	out strings.Builder

	// element name stack — used for ancestor queries (e.g. "are we in pPr?")
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
		if errors.Is(err, io.EOF) {
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

// handleStart is the top-level dispatcher; it delegates to focused helpers to
// keep each XML context's concerns separated.
func (p *docxParser) handleStart(t xml.StartElement) {
	switch t.Name.Local {
	case "tbl", "tr", "tc":
		p.handleTableStart(t)
	case "p", "pStyle", "numPr", "ilvl":
		p.handleParaStart(t)
	case "r", "b", "i", "br":
		p.handleRunStart(t)
	}
}

func (p *docxParser) handleTableStart(t xml.StartElement) {
	switch t.Name.Local {
	case "tbl":
		p.inTable = true
		p.rows = nil
	case "tr":
		p.currRow = nil
	case "tc":
		p.inCell = true
		p.cellText.Reset()
	}
}

func (p *docxParser) handleParaStart(t xml.StartElement) {
	switch t.Name.Local {
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
			if lvl, err := strconv.Atoi(attrVal(t, "val")); err == nil {
				p.listLevel = lvl
			}
		}
	}
}

func (p *docxParser) handleRunStart(t xml.StartElement) {
	switch t.Name.Local {
	case "r":
		if p.inPara {
			p.inRun = true
			p.runBold = false
			p.runItal = false
			p.runText.Reset()
		}
	case "b":
		// w:b with val="0" explicitly turns bold off.
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
			if !p.inCell {
				p.paraText.WriteString(applyInlineFormat(p.runText.String(), p.runBold, p.runItal))
			}
			// When inCell, CharData was written directly to cellText.
			p.inRun = false
		}

	case "p":
		if p.inPara {
			if text := strings.TrimSpace(p.paraText.String()); text != "" && !p.inCell {
				p.out.WriteString(renderParagraph(text, p.paraStyle, p.isList, p.listLevel))
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
			p.out.WriteString(renderMarkdownTable(p.rows))
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
	if prefix, ok := headingPrefixes[style]; ok {
		return prefix + text + paragraphBreak
	}
	if isList {
		return strings.Repeat("  ", listLvl) + "- " + text + "\n"
	}
	return text + paragraphBreak
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
