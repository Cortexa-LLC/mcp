package knowledge

// JSON Schema extractor for the knowledge graph.
//
// JSON Schema is a vocabulary for annotating and validating JSON documents.
// This indexer supports all drafts (Draft 4, 6, 7, 2019-09, 2020-12).
//
// What gets indexed
// -----------------
//   Root schema title (from "title", "$id", or filename) → "type" entity
//   "$defs" entries        (Draft 2019-09 / 2020-12)     → "type" entities
//   "definitions" entries  (Draft 4 / 6 / 7)             → "type" entities
//
// Properties within schemas are not indexed — they are too granular and
// produce too much noise for a knowledge graph.
//
// File matching
// -------------
//   *.schema.json          — explicit naming convention (preferred)
//   schema.json            — standalone schema file
//   *.json containing a top-level "$schema" key — content-detected
//     (only the first 512 bytes are peeked; cost is one extra open+read
//     per .json file, but the OS page cache makes this negligible in practice)
//
// Intentionally excluded
// ----------------------
//   *.jsonc / *.schema.jsonc — JSON-with-comments requires a pre-pass comment
//     stripper; encoding/json would fail on the comment syntax.  Support can
//     be added later by stripping // and /* */ lines before unmarshalling.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (idx *Indexer) processJSONSchemaFile(
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

	// Must be a JSON object at the top level (not an array or scalar).
	var root map[string]json.RawMessage
	if err := json.Unmarshal(src, &root); err != nil {
		return nil // not a valid JSON object — skip gracefully
	}

	now := time.Now().UTC()
	fileID := fmt.Sprintf("file:%s", relPath)
	if writeEntity(entities, seen, fileID, relPath, EntityTypeFile, idx.projectID, now) {
		stats.EntitiesCreated++
	}

	emit := func(name string) {
		if name == "" {
			return
		}
		eid := fmt.Sprintf("type:%s:%s", relPath, name)
		if writeEntity(entities, seen, eid, name, EntityTypeType, idx.projectID, now) {
			stats.EntitiesCreated++
			*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
			stats.RelationsCreated++
		}
	}

	// Root schema name: title > $id > filename stem.
	emit(jsonSchemaRootName(root, relPath))

	// $defs (Draft 2019-09+) and definitions (Draft 4/6/7) — named sub-schemas.
	for _, key := range []string{"$defs", "definitions"} {
		raw, ok := root[key]
		if !ok {
			continue
		}
		var defs map[string]json.RawMessage
		if err := json.Unmarshal(raw, &defs); err != nil {
			continue
		}
		for name := range defs {
			emit(name)
		}
	}

	return nil
}

// jsonSchemaRootName derives a display name for the root schema object.
// Priority: "title" → last path segment of "$id" (minus extension) → filename stem.
func jsonSchemaRootName(root map[string]json.RawMessage, relPath string) string {
	// 1. "title"
	if raw, ok := root["title"]; ok {
		var title string
		if json.Unmarshal(raw, &title) == nil && strings.TrimSpace(title) != "" {
			return strings.TrimSpace(title)
		}
	}

	// 2. "$id" — extract the last path segment without extension.
	if raw, ok := root["$id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) == nil && id != "" {
			// Strip fragment (#foo)
			if i := strings.IndexByte(id, '#'); i >= 0 {
				id = id[:i]
			}
			// Take the last path segment
			if i := strings.LastIndexAny(id, "/\\"); i >= 0 {
				id = id[i+1:]
			}
			// Strip extension
			if i := strings.LastIndexByte(id, '.'); i >= 0 {
				id = id[:i]
			}
			if id != "" {
				return id
			}
		}
	}

	// 3. Filename stem: strip .schema.json and similar compound suffixes.
	base := filepath.Base(relPath)
	lower := strings.ToLower(base)
	for _, suffix := range []string{".schema.json", ".json"} {
		if strings.HasSuffix(lower, suffix) {
			return base[:len(base)-len(suffix)]
		}
	}
	return base
}

// jsonSchemaMatchesPath returns true for files whose name unambiguously
// identifies them as JSON Schema files.
func jsonSchemaMatchesPath(path string) bool {
	lower := strings.ToLower(path)
	base := lower
	if i := strings.LastIndexByte(lower, '/'); i >= 0 {
		base = lower[i+1:]
	}
	if strings.HasSuffix(base, ".schema.json") {
		return true
	}
	return base == "schema.json"
}

// jsonHasSchemaKey peeks at the first 512 bytes of a .json file and returns
// true if the top-level object appears to contain a "$schema" key.  This
// avoids a full parse of every JSON file in the project.
func jsonHasSchemaKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return bytes.Contains(buf[:n], []byte(`"$schema"`))
}
