package knowledge

// Makefile / CMake extractor for the knowledge graph.
//
// Neither Makefile nor CMake has a tree-sitter grammar bundled in
// go-tree-sitter, so these use simple line-based regex extraction.
//
// Makefile — what gets indexed
// ----------------------------
//   Phony/real targets (lines like "build:" or "build-agent:")
//     → "function" entities (targets are the callable units of a Makefile)
//   Variable assignments ("CC := gcc", "VERSION ?= dev")
//     → "type" entities (build-system variables)
//
// CMakeLists.txt / *.cmake — what gets indexed
// ---------------------------------------------
//   project(Name ...) calls → "type" entity for the project name
//   add_executable(name ...)  → "type" entity
//   add_library(name ...)     → "type" entity
//   add_subdirectory(dir)     → "type" entity
//   find_package(pkg ...)     → "import" entity
//   include(module)           → "import" entity

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Makefile
// ---------------------------------------------------------------------------

// makeTargetRe matches rule targets at column 0: "target-name:" or "target_name:"
// Excludes lines starting with '.' (.PHONY, .SILENT, etc.) and tab-indented
// recipe lines.
var makeTargetRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_./-]*)\s*:[^=]`)

// makeVarRe matches variable assignments: VAR := value / VAR = value / VAR ?= value
var makeVarRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?::=|::=|\?=|!=|=)`)

func (idx *Indexer) processMakefileFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seen map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
) error {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	now := time.Now().UTC()
	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seen, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	scanner := bufio.NewScanner(strings.NewReader(string(src)))
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and recipe lines (tab-indented)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\t") {
			continue
		}

		if m := makeTargetRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			eid := fmt.Sprintf("function:%s:%s", relPath, name)
			if writeEntity(entities, seen, eid, name, EntityTypeFunction, idx.projectID, now) {
				stats.EntitiesCreated++
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}
			continue
		}

		if m := makeVarRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			eid := fmt.Sprintf("type:%s:%s", relPath, name)
			if writeEntity(entities, seen, eid, name, EntityTypeType, idx.projectID, now) {
				stats.EntitiesCreated++
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}
		}
	}
	return scanner.Err()
}

// ---------------------------------------------------------------------------
// CMake
// ---------------------------------------------------------------------------

// cmakeCallRe matches CMake function calls: funcname(args)
// Case-insensitive because cmake is case-insensitive for commands.
var cmakeCallRe = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)`)

// cmakeFirstArg extracts the first whitespace-delimited token from a CMake
// argument list (e.g. "MyLib STATIC src/a.cpp" → "MyLib").
func cmakeFirstArg(args string) string {
	args = strings.TrimSpace(args)
	if idx := strings.IndexAny(args, " \t\n\r;"); idx >= 0 {
		args = args[:idx]
	}
	return args
}

func (idx *Indexer) processCMakeFile(
	absPath, relPath string,
	entities *[]entityRecord,
	seen map[string]bool,
	relations *[]relationRecord,
	stats *IndexStats,
) error {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", absPath, err)
	}

	now := time.Now().UTC()
	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seen, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	scanner := bufio.NewScanner(strings.NewReader(string(src)))
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "#") {
			continue
		}

		m := cmakeCallRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cmd := strings.ToLower(m[1])
		args := m[2]
		first := cmakeFirstArg(args)
		if first == "" {
			continue
		}

		switch cmd {
		case "project":
			eid := fmt.Sprintf("type:%s:%s", relPath, first)
			if writeEntity(entities, seen, eid, first, EntityTypeType, idx.projectID, now) {
				stats.EntitiesCreated++
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}

		case "add_executable", "add_library", "add_subdirectory":
			eid := fmt.Sprintf("type:%s:%s", relPath, first)
			if writeEntity(entities, seen, eid, first, EntityTypeType, idx.projectID, now) {
				stats.EntitiesCreated++
				*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
				stats.RelationsCreated++
			}

		case "find_package", "include":
			eid := fmt.Sprintf("import:%s", first)
			if writeEntity(entities, seen, eid, first, EntityTypeImport, idx.projectID, now) {
				stats.EntitiesCreated++
			}
			*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelImports})
			stats.RelationsCreated++
		}
	}
	return scanner.Err()
}

// makefileMatchesPath returns true if the path looks like a Makefile.
func makefileMatchesPath(path string) bool {
	base := strings.ToLower(path)
	// Strip directory
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	switch base {
	case "makefile", "gnumakefile", "bsdmakefile":
		return true
	}
	// MAKE*.TXT pattern (e.g. Apple II A2osX build files: MAKEFILE.TXT, MAKE.TXT)
	if strings.HasPrefix(base, "make") && strings.HasSuffix(base, ".txt") {
		return true
	}
	return strings.HasSuffix(base, ".mk") || strings.HasSuffix(base, ".make")
}

// cmakeMatchesPath returns true if the path looks like a CMake file.
func cmakeMatchesPath(path string) bool {
	lower := strings.ToLower(path)
	base := lower
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	return base == "cmakelists.txt" ||
		strings.HasSuffix(lower, ".cmake")
}
