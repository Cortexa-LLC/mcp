package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"
)

// alwaysSkipDirs is the set of directory base-names that are never worth
// indexing, regardless of what .gitignore says. Pruning these early avoids
// walking into directories that can have thousands of files (node_modules) or
// that contain binary/runtime data rather than source (.git, .claude).
var alwaysSkipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	".claude":       true,
	".ai":           true,
	"dist":          true,
	"build":         true,
	".build":        true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".next":         true,
	".nuxt":         true,
	"target":        true, // Rust/Maven build output
	"coverage":      true,
}

// Indexer scans source files and populates the knowledge graph
type Indexer struct {
	store       *Store
	projectID   string
	root        string
	ignorer     *gitignore.GitIgnore
	scopeFilter *ScopeConfig // Optional scope filter for multi-DB indexing
}

// IndexStats tracks indexing progress
type IndexStats struct {
	FilesScanned     int
	EntitiesCreated  int
	RelationsCreated int
	Errors           int
}

// entityRecord holds entity data before batch insert
type entityRecord struct {
	ID        string
	Name      string
	Type      string
	ProjectID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// relationRecord holds relation data before batch insert
type relationRecord struct {
	FromID string
	ToID   string
	Type   string
}

// pendingCall is an unresolved call site: the function containing it, the bare
// name being called, and the file it appeared in. Resolution needs the whole
// entity set, so it happens after the walk (see resolveCalls).
type pendingCall struct {
	FromID     string // function entity containing the call site
	CalleeName string // bare callee name, e.g. "helper" from repo.helper(x)
	RelPath    string // file the call site is in, for same-file resolution
}

// NewIndexer creates a new indexer
func NewIndexer(store *Store, projectID, root string) (*Indexer, error) {
	// Load ignore patterns from .gitignore and .claudeignore
	var ignorer *gitignore.GitIgnore
	gitignorePath := filepath.Join(root, ".gitignore")
	claudeignorePath := filepath.Join(root, ".claudeignore")

	// Try .gitignore first
	if _, err := os.Stat(gitignorePath); err == nil {
		ignorer, err = gitignore.CompileIgnoreFile(gitignorePath)
		if err != nil {
			return nil, fmt.Errorf("load .gitignore: %w", err)
		}
	}

	// Merge with .claudeignore if it exists
	if _, err := os.Stat(claudeignorePath); err == nil {
		if ignorer != nil {
			// Both files exist - merge them
			combined, err := gitignore.CompileIgnoreFileAndLines(gitignorePath, readLinesFromFile(claudeignorePath)...)
			if err != nil {
				return nil, fmt.Errorf("load .claudeignore: %w", err)
			}
			ignorer = combined
		} else {
			// Only .claudeignore exists
			ignorer, err = gitignore.CompileIgnoreFile(claudeignorePath)
			if err != nil {
				return nil, fmt.Errorf("load .claudeignore: %w", err)
			}
		}
	}

	// If no ignore files, create empty ignorer
	if ignorer == nil {
		ignorer = gitignore.CompileIgnoreLines()
	}

	return &Indexer{
		store:       store,
		projectID:   projectID,
		root:        root,
		ignorer:     ignorer,
		scopeFilter: nil,
	}, nil
}

// SetScopeFilter sets the scope filter for this indexer.
// When set, only files matching the scope's patterns will be indexed.
func (idx *Indexer) SetScopeFilter(scope *ScopeConfig) {
	idx.scopeFilter = scope
}

// readLinesFromFile reads all lines from a file
func readLinesFromFile(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	lines := strings.Split(string(content), "\n")
	// Filter out empty lines and comments
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			result = append(result, line)
		}
	}
	return result
}

// clearProjectData removes all entities, their observations, and all relations
// for this project so the index can be rebuilt from scratch.
func (idx *Indexer) clearProjectData() error {
	// Delete all typed entity-to-entity relations for this project.
	for _, relType := range AllowedRelTypes {
		query := fmt.Sprintf(`
			MATCH (from:Entity {project_id: $project_id})-[r:%s]->(to:Entity {project_id: $project_id})
			DELETE r
		`, relType) // relType is from a hardcoded whitelist — safe to interpolate
		result, err := idx.store.QueryParams(query, map[string]any{"project_id": idx.projectID})
		if err != nil {
			return fmt.Errorf("delete %s relations: %w", relType, err)
		}
		result.Close()
	}

	// Delete HAS_OBSERVATION edges and their Observation nodes for this project.
	result, err := idx.store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})-[r:HAS_OBSERVATION]->(o:Observation)
		DELETE r, o
	`, map[string]any{"project_id": idx.projectID})
	if err != nil {
		return fmt.Errorf("delete observations: %w", err)
	}
	result.Close()

	// Finally delete the entities themselves (DETACH DELETE handles any remaining edges).
	result, err = idx.store.QueryParams(`
		MATCH (e:Entity {project_id: $project_id})
		DETACH DELETE e
	`, map[string]any{"project_id": idx.projectID})
	if err != nil {
		return fmt.Errorf("delete entities: %w", err)
	}
	result.Close()

	return nil
}

// Index scans the project and populates the knowledge graph
func (idx *Indexer) Index() (*IndexStats, error) {
	stats := &IndexStats{}

	// Clear existing data for this project (rebuild from scratch)
	if err := idx.clearProjectData(); err != nil {
		return nil, fmt.Errorf("clear existing data: %w", err)
	}

	// Collect entities, relations, and observations in memory; insert via
	// parameterized Cypher to avoid all CSV quoting/delimiter issues with
	// complex entity names.
	var entities []entityRecord
	seenEntities := make(map[string]bool)
	var relations []relationRecord
	var observations []obsRecord
	// Call sites cannot be resolved while walking, because a callee may be
	// defined in a file not yet visited. They are collected here and resolved
	// against the complete entity set once the walk finishes.
	var calls []pendingCall

	// Walk the project directory
	err := filepath.Walk(idx.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path (needed for both directory and file decisions)
		relPath, err := filepath.Rel(idx.root, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			// Skip the root itself
			if relPath == "." {
				return nil
			}
			// Always-skip directories that are never useful to index
			base := info.Name()
			if alwaysSkipDirs[base] {
				return filepath.SkipDir
			}
			// Apply gitignore / claudeignore to directories so we prune entire subtrees
			// (MatchesPath on a dir path skips the whole subtree via SkipDir)
			if idx.ignorer.MatchesPath(relPath) {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if ignored
		if idx.ignorer.MatchesPath(relPath) {
			return nil
		}

		// Check scope filter if set
		if idx.scopeFilter != nil && !idx.scopeFilter.ShouldIncludePath(relPath) {
			return nil
		}

		// Process based on file type
		ext := strings.ToLower(filepath.Ext(path))
		if cfg, ok := langRegistry[ext]; ok {
			if err := idx.processWithTreeSitter(path, relPath, cfg, &entities, seenEntities, &relations, &calls, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if asmMatchesPath(path) {
			if err := idx.processAsmFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if ext == ".md" {
			if err := idx.processMarkdownFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if ext == ".yaml" || ext == ".yml" {
			if err := idx.processYAMLFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if ext == ".html" || ext == ".htm" {
			if err := idx.processHTMLFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if ext == ".pdf" {
			if err := idx.processPDFFile(path, relPath, &entities, seenEntities, &relations, &observations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if jsonSchemaMatchesPath(path) || (ext == ".json" && jsonHasSchemaKey(path)) {
			if err := idx.processJSONSchemaFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if graphqlMatchesPath(path) {
			if err := idx.processGraphQLFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if makefileMatchesPath(path) {
			if err := idx.processMakefileFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		} else if cmakeMatchesPath(path) {
			if err := idx.processCMakeFile(path, relPath, &entities, seenEntities, &relations, stats); err != nil {
				fmt.Printf("Warning: Failed to process %s: %v\n", relPath, err)
				stats.Errors++
			}
			stats.FilesScanned++
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	// Resolve call sites now that every function entity in the scope is known.
	if len(calls) > 0 {
		callRels, unresolved, ambiguous := resolveCalls(calls, entities, seenEntities)
		relations = append(relations, callRels...)
		fmt.Printf("   Call sites:        %d (%d resolved, %d ambiguous, %d external/unresolved)\n",
			len(calls), len(callRels), ambiguous, unresolved)
	}

	// Batch create entities via parameterized Cypher (immune to special characters in names)
	if err := idx.batchCreateEntities(entities); err != nil {
		return nil, fmt.Errorf("batch create entities: %w", err)
	}

	// Batch create relations
	if err := idx.batchCreateRelations(relations, seenEntities, stats); err != nil {
		return nil, fmt.Errorf("batch create relations: %w", err)
	}

	// Batch create observations (PDF text chunks, etc.)
	if err := idx.batchCreateObservations(observations); err != nil {
		return nil, fmt.Errorf("batch create observations: %w", err)
	}

	return stats, nil
}

// writeEntity appends an entity to the slice if not already seen.
// ts is the timestamp to use for both created_at and updated_at.
func writeEntity(entities *[]entityRecord, seen map[string]bool, id, name, typ, projectID string, ts time.Time) bool {
	if seen[id] {
		return false
	}
	seen[id] = true
	*entities = append(*entities, entityRecord{
		ID:        id,
		Name:      name,
		Type:      typ,
		ProjectID: projectID,
		CreatedAt: ts,
		UpdatedAt: ts,
	})
	return true
}

// batchCreateEntities bulk-loads entities via a temporary NDJSON file.
//
// NDJSON (newline-delimited JSON) is used instead of CSV because:
//   - encoding/json correctly escapes all special characters (commas, quotes,
//     CSS selectors, Unicode, etc.) without any delimiter/quoting edge cases.
//   - Kuzu's COPY FROM bulk-loader is O(N) and far faster than individual
//     parameterized CREATE statements (~4x in practice).
//
// Falls back to parameterized Cypher inserts if COPY FROM fails (e.g. on a
// Kuzu version that does not support JSON import).
// Note: EntitiesCreated is counted by callers of writeEntity, not here.
func (idx *Indexer) batchCreateEntities(entities []entityRecord) error {
	if len(entities) == 0 {
		return nil
	}

	// Write entities to a temporary NDJSON file
	ndjsonPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("kg-entities-%d.json", time.Now().UnixNano()))
	defer os.Remove(ndjsonPath)

	f, err := os.Create(ndjsonPath)
	if err != nil {
		return fmt.Errorf("create ndjson temp file: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, ent := range entities {
		row := map[string]string{
			"id":         ent.ID,
			"name":       ent.Name,
			"type":       ent.Type,
			"project_id": ent.ProjectID,
			"created_at": ent.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": ent.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if err := enc.Encode(row); err != nil {
			f.Close()
			return fmt.Errorf("encode entity json: %w", err)
		}
	}
	f.Close()

	// Bulk load via COPY FROM JSON
	query := fmt.Sprintf(
		`COPY Entity(id, name, type, project_id, created_at, updated_at) FROM '%s'`,
		ndjsonPath,
	)
	result, err := idx.store.Query(query)
	if err == nil {
		result.Close()
		return nil
	}

	// COPY FROM JSON not supported by this Kuzu version — fall back to
	// individual parameterized inserts (slower but universally compatible).
	fmt.Printf("Note: COPY FROM JSON unavailable (%v); falling back to row-by-row insert\n", err)
	for _, ent := range entities {
		r, err := idx.store.QueryParams(`
			CREATE (e:Entity {
				id: $id,
				name: $name,
				type: $type,
				project_id: $project_id,
				created_at: $created_at,
				updated_at: $updated_at
			})
		`, map[string]any{
			"id":         ent.ID,
			"name":       ent.Name,
			"type":       ent.Type,
			"project_id": ent.ProjectID,
			"created_at": ent.CreatedAt,
			"updated_at": ent.UpdatedAt,
		})
		if err != nil {
			fmt.Printf("Warning: insert entity %s: %v\n", ent.ID, err)
			continue
		}
		r.Close()
	}
	return nil
}

// resolveCalls turns collected call sites into CALLS relations.
//
// The indexer has no type information, so a callee is only a bare name. Two
// resolution rules are applied, both chosen to favour precision over recall —
// a wrong CALLS edge is worse than a missing one, because it invents a
// dependency that does not exist:
//
//  1. Same file: if the enclosing file defines a function of that name, link to
//     it. Safe, and covers the common helper-in-the-same-file case.
//  2. Globally unique: if exactly one function of that name exists anywhere in
//     the scope, link to it. A name with two or more definitions is ambiguous
//     and is dropped rather than guessed.
//
// Everything else is left unresolved: standard-library and third-party calls,
// and common method names like `apply`, `get` or `map` that are defined many
// times over. Unresolved counts are reported so the ratio stays visible.
func resolveCalls(calls []pendingCall, entities []entityRecord, seen map[string]bool) (rels []relationRecord, unresolved, ambiguous int) {
	// Index function entity IDs by bare name.
	byName := make(map[string][]string)
	for _, e := range entities {
		if e.Type == EntityTypeFunction {
			byName[e.Name] = append(byName[e.Name], e.ID)
		}
	}

	for _, c := range calls {
		sameFile := fmt.Sprintf("function:%s:%s", c.RelPath, c.CalleeName)
		if seen[sameFile] {
			rels = append(rels, relationRecord{FromID: c.FromID, ToID: sameFile, Type: RelCalls})
			continue
		}
		candidates := byName[c.CalleeName]
		switch {
		case len(candidates) == 1:
			rels = append(rels, relationRecord{FromID: c.FromID, ToID: candidates[0], Type: RelCalls})
		case len(candidates) > 1:
			ambiguous++
		default:
			unresolved++
		}
	}
	return rels, unresolved, ambiguous
}

// batchCreateRelations bulk-loads relations via NDJSON COPY FROM, grouped by
// relation type (Kuzu requires one table per relation type).
// Falls back to individual parameterized inserts if COPY FROM is unsupported.
//
// known is the set of entity IDs written by this index run. Relations with an
// endpoint outside it are dropped up front: COPY FROM discards such rows
// silently, so keeping them only inflated RelationsCreated above what the graph
// actually holds. Some extractors do emit them — the YAML indexer, for one,
// references nested keys it never creates as entities.
func (idx *Indexer) batchCreateRelations(relations []relationRecord, known map[string]bool, stats *IndexStats) error {
	if len(relations) == 0 {
		return nil
	}

	// Group by relation type so we can COPY each type's table separately,
	// discarding records that cannot become a distinct edge.
	byType := make(map[string][]relationRecord)
	seen := make(map[relationRecord]bool, len(relations))
	dangling := 0
	for _, r := range relations {
		if !known[r.FromID] || !known[r.ToID] {
			dangling++
			continue
		}
		// Extractors can emit the same edge twice — a Scala class and its
		// companion object share one entity ID, for instance, so the file yields
		// two identical CONTAINS records. The graph holds one edge either way.
		if seen[r] {
			continue
		}
		seen[r] = true
		byType[r.Type] = append(byType[r.Type], r)
	}
	if dangling > 0 {
		fmt.Printf("Note: skipped %d relation(s) referencing entities that were not indexed\n", dangling)
	}
	if len(byType) == 0 {
		return nil
	}

	usedFallback := false
	for relType, rels := range byType {
		if err := idx.bulkLoadRelations(relType, rels); err != nil {
			if !usedFallback {
				fmt.Printf("Note: COPY FROM JSON for relations unavailable (%v); falling back to row-by-row\n", err)
				usedFallback = true
			}
			// Fallback: individual parameterized inserts
			for _, rel := range rels {
				if err2 := idx.store.CreateRelation(rel.FromID, rel.ToID, rel.Type, idx.projectID); err2 != nil {
					continue // skip duplicates / missing endpoints
				}
				stats.RelationsCreated++
			}
			continue
		}
		stats.RelationsCreated += len(rels)
	}
	return nil
}

// Note: RelationsCreated is accumulated here, at persist time, and nowhere else.
// The per-file extractors append to the relations slice without counting — they
// used to do both, which double-counted every relation and made the indexer
// report exactly 2x what `kg stats` found in the database.

// bulkLoadRelations writes relations of one type to a temp NDJSON file and
// uses Kuzu's COPY FROM to bulk-load them into the corresponding edge table.
func (idx *Indexer) bulkLoadRelations(relType string, rels []relationRecord) error {
	ndjsonPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("kg-rels-%s-%d.json", relType, time.Now().UnixNano()))
	defer os.Remove(ndjsonPath)

	f, err := os.Create(ndjsonPath)
	if err != nil {
		return fmt.Errorf("create ndjson: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, r := range rels {
		if err := enc.Encode(map[string]string{"from": r.FromID, "to": r.ToID}); err != nil {
			f.Close()
			return fmt.Errorf("encode relation json: %w", err)
		}
	}
	f.Close()

	// relType is from AllowedRelTypes whitelist — safe to interpolate
	query := fmt.Sprintf(`COPY %s FROM '%s'`, relType, ndjsonPath)
	result, err := idx.store.Query(query)
	if err != nil {
		return err
	}
	result.Close()
	return nil
}
