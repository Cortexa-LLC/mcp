package knowledge

// Assembly line-based extractor for xasm++ dialects.
//
// # Extensibility
//
// All dialect-specific keywords live in the maps below. Adding support for
// a new assembler dialect is typically a one-line change per keyword:
//
//   - New EQU-like constant directives → add to asmEquOpcodes
//   - New macro-start keywords        → add to asmMacroStartOpcodes
//   - New macro-end keywords          → add to asmMacroEndOpcodes
//   - New include/use keywords        → add to asmIncludeOpcodes
//   - New file extension              → add to asmExtensions AND
//                                       the Walk check in indexer.go
//
// The parser is intentionally dialect-agnostic: it checks all maps
// simultaneously so a single source file can mix keyword styles.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// asmExtensions is the set of lowercase file extensions routed to the
// line-based assembly parser instead of the tree-sitter pipeline.
var asmExtensions = map[string]bool{
	".asm": true,
	".s":   true, // covers both .s and .S (Walk lowercases the extension)
}

// asmCompoundSuffixes handles filenames like "foo.S.txt" or "bar.asm.txt"
// where the real content type is indicated by the inner extension.
// filepath.Ext only returns the last extension, so these need special handling.
// To add support for a new compound suffix, add an entry here.
var asmCompoundSuffixes = map[string]bool{
	".s.txt":   true,
	".asm.txt": true,
}

// asmMatchesPath returns true if the file at path should be handled by
// the assembly line parser (either a simple or compound assembly extension).
func asmMatchesPath(path string) bool {
	lower := strings.ToLower(path)
	for suffix := range asmCompoundSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return asmExtensions[strings.ToLower(filepath.Ext(path))]
}

// asmEquOpcodes are directives that bind a constant value to a label
// (C-style #define equivalent).  All dialects covered by xasm++:
//
//	SCMASM:  .EQ, .EQU  →  stored after dot-strip as "EQ", "EQU"
//	Merlin:  EQU, =
//	FLEX/EDTASM/Z80:  EQU, SET, DEFL
var asmEquOpcodes = map[string]bool{
	"EQ": true, "EQU": true, "=": true,
	"SET": true, "DEFL": true,
}

// asmMacroStartOpcodes are directives that begin a macro definition.
//
//	SCMASM:  .MA  →  stored as "MA"
//	Merlin:  MAC
//	FLEX:    MACRO
var asmMacroStartOpcodes = map[string]bool{
	"MA": true, "MAC": true, "MACRO": true,
}

// asmMacroEndOpcodes are directives (or tokens) that close a macro body.
//
//	SCMASM:  .EM  →  stored as "EM"
//	Merlin:  <<<  (as the only token on a line)
//	FLEX:    ENDM
var asmMacroEndOpcodes = map[string]bool{
	"EM": true, "ENDM": true, "<<<": true,
}

// asmIncludeOpcodes are directives that pull in another source file.
//
//	SCMASM:  .IN, .INB  →  stored as "IN", "INB"
//	Merlin:  PUT, USE
//	Z80 universal:  INCLUDE, GET
var asmIncludeOpcodes = map[string]bool{
	"IN": true, "INB": true, "PUT": true, "USE": true,
	"INCLUDE": true, "GET": true,
}

// processAsmFile parses one assembly source file with a dialect-neutral
// line scanner and emits entities/relations into the shared CSV writers.
func (idx *Indexer) processAsmFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seenEntities map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
) error {
	f, err := os.Open(absPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", absPath, err)
	}
	defer f.Close()

	now := time.Now().UTC()

	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seenEntities, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	inMacro := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		cleaned := asmStripComment(line)
		if cleaned == "" {
			continue
		}

		label, opcode, operand := asmSplitLine(cleaned)

		// Detect macro end (exit before any further processing)
		if asmMacroEndOpcodes[opcode] {
			inMacro = false
			continue
		}
		// Skip bodies of macro definitions — their local labels are not public symbols
		if inMacro {
			continue
		}

		switch {
		case asmMacroStartOpcodes[opcode]:
			// Macro name is either the label (Merlin/FLEX style) or the operand (SCMASM style)
			mname := label
			if mname == "" {
				mname = operand
			}
			if mname != "" && asmIsSignificant(mname) {
				eid := fmt.Sprintf("function:%s:%s", relPath, mname)
				if writeEntity(entities, seenEntities, eid, mname, EntityTypeFunction, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}
			inMacro = true

		case asmEquOpcodes[opcode]:
			// Constant definition: label is the symbol name
			if label != "" && asmIsSignificant(label) {
				eid := fmt.Sprintf("type:%s:%s", relPath, label)
				if writeEntity(entities, seenEntities, eid, label, EntityTypeType, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}

		case asmIncludeOpcodes[opcode]:
			// File include / dependency
			path := operand
			if path == "" {
				path = label // defensive: some syntaxes put path as first token
			}
			path = strings.Trim(path, `"'<>`)
			if path != "" {
				importID := fmt.Sprintf("import:%s", path)
				if writeEntity(entities, seenEntities, importID, path, EntityTypeImport, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: importID, Type: RelImports})
				stats.RelationsCreated++
			}

		default:
			// General label (entry point / subroutine / data symbol)
			if label != "" && asmIsSignificant(label) {
				eid := fmt.Sprintf("function:%s:%s", relPath, label)
				if writeEntity(entities, seenEntities, eid, label, EntityTypeFunction, idx.projectID, now) {
					stats.EntitiesCreated++
				}
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}
		}
	}

	return scanner.Err()
}

// asmStripComment removes inline `;` comments and trailing whitespace.
// Leading whitespace is intentionally preserved so that asmSplitLine can
// detect column-1 labels (non-indented tokens are labels in SCMASM/Merlin).
// Lines that are purely comments are returned as empty strings.
func asmStripComment(line string) string {
	// Column-1 `*` is a full-line comment in SCMASM and Merlin
	if len(line) > 0 && line[0] == '*' {
		return ""
	}
	// `;` comment anywhere
	if idx := strings.IndexByte(line, ';'); idx >= 0 {
		line = line[:idx]
	}
	// Trim trailing whitespace only — leading whitespace is significant for
	// column-1 label detection; strings.TrimRight handles the pure-whitespace
	// case (returns "").
	return strings.TrimRight(line, " \t\r")
}

// asmSplitLine parses a cleaned (comment-stripped) assembly line into its
// three logical fields: label, opcode, operand.
//
// Column-1 rule: if the first character is not whitespace the first token is a
// label; otherwise the label is empty and the first token is the opcode.
//
// The opcode is normalised: the leading dot (`.EQ` → `EQ`) is stripped and
// the result is uppercased, making the comparison maps dialect-neutral.
// The trailing colon is stripped from labels (Z80 style: `START:` → `START`).
func asmSplitLine(line string) (label, opcode, operand string) {
	if line == "" {
		return
	}

	hasLabel := line[0] != ' ' && line[0] != '\t'

	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return
	}

	idx := 0
	if hasLabel {
		label = strings.TrimSuffix(tokens[0], ":")
		idx = 1
	}

	if idx < len(tokens) {
		raw := tokens[idx]
		opcode = strings.ToUpper(strings.TrimPrefix(raw, "."))
		idx++
	}

	if idx < len(tokens) {
		operand = tokens[idx]
	}

	return
}

// asmIsSignificant returns true for labels worth indexing in the KG.
// Filtered out: very short names, SCMASM local (`.foo`) and parameter (`]N`)
// labels, and purely numeric tokens.
func asmIsSignificant(name string) bool {
	// Already colon-stripped by asmSplitLine, but be defensive
	name = strings.TrimSuffix(name, ":")
	if len(name) < 3 {
		return false
	}
	// SCMASM local labels start with `.` (e.g. `.1`, `.DONE`)
	// SCMASM macro parameters start with `]`
	if name[0] == '.' || name[0] == ']' {
		return false
	}
	// Skip purely numeric tokens (rare but possible in some dialects)
	for _, r := range name {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
