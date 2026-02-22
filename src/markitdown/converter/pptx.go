package converter

// pptx.go — PPTX → Markdown via streaming OOXML parser.
//
// PPTX files are ZIP archives. Slides live at ppt/slides/slideN.xml.
// We sort slides numerically, then stream-parse each with a state machine
// that handles shapes (text bodies, paragraphs, runs) and tables.
//
// Key XML namespaces used in PPTX slides:
//   p: — presentationml (sp, txBody at shape level)
//   a: — drawingml     (p, r, t, tbl, tr, tc, rPr, pPr)
//
// Because we compare t.Name.Local (strips namespace prefix), both namespaces
// work correctly without explicit namespace registration.

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pptxSlideRE matches the canonical slide paths inside a PPTX ZIP archive,
// capturing the slide number for numeric sort.
var pptxSlideRE = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

// pptxSlideSep is the horizontal rule placed between non-empty slides.
const pptxSlideSep = "\n---\n\n"

func convertPPTX(filePath string) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open pptx %s: %w", filePath, err)
	}
	defer func() { _ = zr.Close() }()

	type slideEntry struct {
		num  int
		file *zip.File
	}

	var entries []slideEntry
	for _, f := range zr.File {
		m := pptxSlideRE.FindStringSubmatch(f.Name)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		entries = append(entries, slideEntry{n, f})
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("no slides found in %s", filePath)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })

	var parts []string
	for _, e := range entries {
		rc, openErr := e.file.Open()
		if openErr != nil {
			return "", fmt.Errorf("open slide %d: %w", e.num, openErr)
		}
		text, parseErr := parseSlideXML(rc)
		_ = rc.Close()
		if parseErr != nil {
			return "", fmt.Errorf("parse slide %d: %w", e.num, parseErr)
		}
		if ocrAvailable() {
			if ocrText, _ := ocrSlideImages(zr, e.num); ocrText != "" {
				if text != "" {
					text += "\n\n" + ocrText
				} else {
					text = ocrText
				}
			}
		}
		// Use TrimSpace only for the emptiness check; preserve the original
		// text so leading whitespace (list indentation) is not stripped.
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}

	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, pptxSlideSep) + "\n", nil
}

// ---------------------------------------------------------------------------
// Streaming XML state machine
// ---------------------------------------------------------------------------

type pptxParser struct {
	stack []string

	// shape-level state
	inShape  bool
	isTitle  bool
	inTxBody bool

	// paragraph-level state
	inPara    bool
	paraLevel int // from <a:pPr lvl="N"/>
	paraText  strings.Builder

	// run-level state
	inRun   bool
	runBold bool
	runItal bool
	runText strings.Builder

	// table-level state
	inTable  bool
	rows     [][]string
	currRow  []string
	inCell   bool
	cellText strings.Builder

	// collected slide output (title first, then body)
	titleParts []string
	bodyParts  []string
}

func (p *pptxParser) push(name string) { p.stack = append(p.stack, name) }
func (p *pptxParser) pop() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
}
func (p *pptxParser) inCtx(name string) bool {
	for _, s := range p.stack {
		if s == name {
			return true
		}
	}
	return false
}

func parseSlideXML(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	p := &pptxParser{}

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse slide xml: %w", err)
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

	return p.render(), nil
}

func (p *pptxParser) handleStart(t xml.StartElement) {
	switch t.Name.Local {
	case "sp":
		p.handleShapeStart()
	case "ph":
		p.handlePlaceholderStart(t)
	case "txBody":
		p.handleTxBodyStart()
	case "tbl", "tr", "tc":
		p.handleTableStart(t)
	case "p":
		p.handleParaStart()
	case "pPr":
		p.handleParaPropsStart(t)
	case "r":
		p.handleRunStart()
	case "rPr":
		p.handleRunPropsStart(t)
	case "br":
		if p.inPara {
			p.paraText.WriteByte('\n')
		}
	}
}

func (p *pptxParser) handleShapeStart() {
	p.inShape = true
	p.isTitle = false
}

func (p *pptxParser) handlePlaceholderStart(t xml.StartElement) {
	if p.inShape && p.inCtx("nvPr") {
		typ := attrVal(t, "type")
		if typ == "title" || typ == "ctrTitle" {
			p.isTitle = true
		}
	}
}

func (p *pptxParser) handleTxBodyStart() {
	if p.inShape {
		p.inTxBody = true
	}
}

func (p *pptxParser) handleTableStart(t xml.StartElement) {
	switch t.Name.Local {
	case "tbl":
		p.inTable = true
		p.rows = nil
	case "tr":
		p.currRow = nil
	case "tc":
		if p.inTable {
			p.inCell = true
			p.cellText.Reset()
		}
	}
}

func (p *pptxParser) handleParaStart() {
	if p.inTxBody || p.inCell {
		p.inPara = true
		p.paraLevel = 0
		p.paraText.Reset()
	}
}

func (p *pptxParser) handleParaPropsStart(t xml.StartElement) {
	if p.inPara {
		if lvl, err := strconv.Atoi(attrVal(t, "lvl")); err == nil && lvl > 0 {
			p.paraLevel = lvl
		}
	}
}

func (p *pptxParser) handleRunStart() {
	if p.inPara {
		p.inRun = true
		p.runBold = false
		p.runItal = false
		p.runText.Reset()
	}
}

func (p *pptxParser) handleRunPropsStart(t xml.StartElement) {
	if p.inRun {
		if attrVal(t, "b") == "1" {
			p.runBold = true
		}
		if attrVal(t, "i") == "1" {
			p.runItal = true
		}
	}
}

func (p *pptxParser) handleEnd(local string) {
	switch local {
	case "r":
		p.endRun()
	case "p":
		p.endPara()
	case "tc":
		p.endCell()
	case "tr":
		p.endRow()
	case "tbl":
		p.endTable()
	case "txBody":
		p.inTxBody = false
		p.inPara = false // safety reset for malformed XML
	case "sp":
		p.inShape = false
		p.isTitle = false
	}
}

func (p *pptxParser) endRun() {
	if !p.inRun {
		return
	}
	text := applyInlineFormat(p.runText.String(), p.runBold, p.runItal)
	if p.inCell {
		p.cellText.WriteString(text)
	} else {
		p.paraText.WriteString(text)
	}
	p.inRun = false
}

func (p *pptxParser) endPara() {
	if !p.inPara {
		return
	}
	// Table cell paragraphs are handled entirely via cellText — skip here.
	if !p.inCell {
		if text := strings.TrimSpace(p.paraText.String()); text != "" {
			line := p.renderParaLine(text)
			if p.isTitle {
				p.titleParts = append(p.titleParts, line)
			} else if p.inTxBody {
				p.bodyParts = append(p.bodyParts, line)
			}
		}
	}
	p.inPara = false
	p.paraText.Reset()
}

func (p *pptxParser) renderParaLine(text string) string {
	if p.paraLevel > 0 {
		return strings.Repeat("  ", p.paraLevel-1) + "- " + text
	}
	return text
}

func (p *pptxParser) endCell() {
	if p.inTable {
		p.currRow = append(p.currRow, strings.TrimSpace(p.cellText.String()))
		p.inCell = false
	}
}

func (p *pptxParser) endRow() {
	if p.inTable {
		p.rows = append(p.rows, p.currRow)
		p.currRow = nil
	}
}

func (p *pptxParser) endTable() {
	if p.inTable {
		p.bodyParts = append(p.bodyParts, renderMarkdownTable(p.rows))
		p.inTable = false
	}
}

func (p *pptxParser) handleText(text string) {
	// inRun takes priority: accumulate in runText so formatting can be applied.
	if p.inRun {
		p.runText.WriteString(text)
	} else if p.inCell {
		p.cellText.WriteString(text)
	}
}

// render assembles the final Markdown for a single slide: title heading(s)
// first, then body content.
func (p *pptxParser) render() string {
	var sb strings.Builder
	for _, t := range p.titleParts {
		sb.WriteString("## ")
		sb.WriteString(t)
		sb.WriteString("\n\n")
	}
	for _, b := range p.bodyParts {
		sb.WriteString(b)
		sb.WriteString("\n\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ---------------------------------------------------------------------------
// PPTX image OCR helpers
// ---------------------------------------------------------------------------

// parseSlideImageRels parses a slide XML and returns the r:embed IDs of all
// <a:blip> elements (image references), deduplicated.
func parseSlideImageRels(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	seen := make(map[string]bool)
	var ids []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse slide xml for image rels: %w", err)
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "blip" {
			if id := attrVal(t, "embed"); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// parseRelsFile parses an OOXML .rels file and returns a map of
// relationship ID → Target path.
func parseRelsFile(r io.Reader) (map[string]string, error) {
	dec := xml.NewDecoder(r)
	m := make(map[string]string)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse rels xml: %w", err)
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "Relationship" {
			id := attrVal(t, "Id")
			target := attrVal(t, "Target")
			if id != "" && target != "" {
				m[id] = target
			}
		}
	}
	return m, nil
}

// resolveSlideRelsPath returns the path of the .rels file for a given slide number.
func resolveSlideRelsPath(slideNum int) string {
	return fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", slideNum)
}

// resolveZIPPath resolves a Target path from a .rels file.
// In OOXML, Target paths are relative to the owning part's directory, which
// is the parent of the _rels/ directory containing the rels file.
// ZIP paths always use forward slashes.
func resolveZIPPath(relsFilePath, target string) string {
	return path.Join(path.Dir(path.Dir(relsFilePath)), target)
}

// ocrSlideImages finds all image references in slideNum, reads those images
// from the ZIP, and returns the combined OCR text.
// Errors on individual images are silently skipped (corrupt images are common).
func ocrSlideImages(zr *zip.ReadCloser, slideNum int) (string, error) {
	// Open the slide XML to extract blip embed IDs.
	slideFile := fmt.Sprintf("ppt/slides/slide%d.xml", slideNum)
	var embedIDs []string
	for _, f := range zr.File {
		if f.Name != slideFile {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open slide %d for OCR: %w", slideNum, err)
		}
		embedIDs, err = parseSlideImageRels(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		break
	}
	if len(embedIDs) == 0 {
		return "", nil
	}

	// Open the .rels file to resolve embed IDs to ZIP paths.
	relsPath := resolveSlideRelsPath(slideNum)
	idToTarget := make(map[string]string)
	for _, f := range zr.File {
		if f.Name != relsPath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open rels %s: %w", relsPath, err)
		}
		idToTarget, err = parseRelsFile(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		break
	}

	// OCR each referenced image.
	var ocrParts []string
	for _, id := range embedIDs {
		target, ok := idToTarget[id]
		if !ok {
			continue
		}
		imgPath := resolveZIPPath(relsPath, target)
		for _, f := range zr.File {
			if f.Name != imgPath {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				break // skip corrupt entry
			}
			var buf strings.Builder
			_, _ = io.Copy(&buf, rc)
			_ = rc.Close()

			ext := path.Ext(imgPath)
			text, _ := ocrImageData([]byte(buf.String()), ext)
			if text != "" {
				ocrParts = append(ocrParts, text)
			}
			break
		}
	}

	return strings.Join(ocrParts, "\n\n"), nil
}
