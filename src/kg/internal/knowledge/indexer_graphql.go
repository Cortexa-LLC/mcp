package knowledge

// GraphQL Schema Definition Language (SDL) extractor for the knowledge graph.
//
// Supports:
//   - All SDL constructs: type, interface, union, enum, scalar, input, directive
//   - extend variants (extend type, extend interface, extend union, ...)
//   - Apollo Federation v1: extend type @key, @external, @requires, @provides, @extends
//   - Apollo Federation v2: extend schema @link, @shareable, @inaccessible,
//     @override, @composeDirective, @interfaceObject, @authenticated, @requiresScopes
//   - Standard directives: @deprecated, @specifiedBy
//
// What gets indexed
// -----------------
//   type / interface / union / enum / scalar / input definitions → "type" entities
//   directive @name definitions                                  → "type" entities
//   Fields inside Query, Mutation, Subscription root types       → "function" entities
//
// Apollo Federation notes
// -----------------------
//   extend type Foo @key(fields: "id") is indexed as a regular type entity — the
//   name "Foo" is what matters for graph search.  Federation-specific directives
//   (@key, @external, @link, etc.) are annotations on existing definitions and do
//   not produce separate entities.
//
//   extend schema @link(...) introduces no new named entity; it is recognised and
//   skipped cleanly (schema is not in the keyword list for gqlDefRe).
//
// File extensions: .graphql, .graphqls, .gql
//
// State machine
// -------------
//   braceDepth tracks { } nesting.  inRootOp becomes true while the parser is
//   inside a Query, Mutation, or Subscription type body (at depth 1), causing
//   field names on those lines to be emitted as function entities.

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// gqlDefRe matches any top-level GraphQL named type definition, with or without
// an optional leading "extend".  Captured groups:
//
//	[1] keyword (type|interface|union|enum|scalar|input)
//	[2] type name
var gqlDefRe = regexp.MustCompile(
	`(?i)^\s*(?:extend\s+)?(type|interface|union|enum|scalar|input)\s+([A-Za-z_][A-Za-z0-9_]*)`,
)

// gqlDirectiveDefRe matches top-level directive definitions.
// Captured group [1]: directive name (without the @ sigil).
var gqlDirectiveDefRe = regexp.MustCompile(
	`(?i)^\s*directive\s+@([A-Za-z_][A-Za-z0-9_]*)`,
)

// gqlRootTypeRe matches the definition (or extension) of the three root
// operation types: Query, Mutation, Subscription.  Matching triggers the
// inRootOp state so their fields are indexed as function entities.
var gqlRootTypeRe = regexp.MustCompile(
	`(?i)^\s*(?:extend\s+)?type\s+(Query|Mutation|Subscription)\b`,
)

// gqlFieldRe matches a field declaration at the first level of indentation
// inside a root operation type.  Accepts camelCase, PascalCase, and
// underscore-prefixed names (e.g. _service for federation introspection).
// Anchored to an opening paren (argument list) or colon (return type).
var gqlFieldRe = regexp.MustCompile(
	`^\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(|:)`,
)

func (idx *Indexer) processGraphQLFile(
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

	// braceDepth tracks { } nesting level.
	// inRootOp is true while inside a Query / Mutation / Subscription block.
	braceDepth := 0
	inRootOp := false

	scanner := bufio.NewScanner(strings.NewReader(string(src)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip blank lines and GraphQL comment lines (# ...)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Strip trailing inline comments before counting braces so that
		// a line like:  type Foo { # opens block  counts as depth +1 only.
		forBraces := line
		if ci := strings.IndexByte(line, '#'); ci >= 0 {
			forBraces = line[:ci]
		}

		// Capture brace depth BEFORE processing this line's braces so we
		// can determine the context the line was written in.
		depthBefore := braceDepth
		for _, ch := range forBraces {
			switch ch {
			case '{':
				braceDepth++
			case '}':
				if braceDepth > 0 {
					braceDepth--
				}
			}
		}

		// Leaving a root-op block: reset when depth returns to 0.
		if inRootOp && braceDepth == 0 {
			inRootOp = false
		}

		// ── Top-level definitions (written at depth 0) ───────────────────

		if depthBefore == 0 {
			// Detect root operation types first (sets inRootOp for field indexing).
			if gqlRootTypeRe.MatchString(line) {
				inRootOp = true
			}

			// All named type definitions → "type" entity.
			if m := gqlDefRe.FindStringSubmatch(line); m != nil {
				name := m[2]
				// Skip GraphQL introspection built-ins (__Schema, __Type, …)
				if !strings.HasPrefix(name, "__") {
					eid := fmt.Sprintf("type:%s:%s", relPath, name)
					if writeEntity(entities, seen, eid, name, EntityTypeType, idx.projectID, now) {
						stats.EntitiesCreated++
						*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
						stats.RelationsCreated++
					}
				}
				continue
			}

			// directive @name definitions → "type" entity (prefixed with @).
			if m := gqlDirectiveDefRe.FindStringSubmatch(line); m != nil {
				name := "@" + m[1]
				eid := fmt.Sprintf("type:%s:%s", relPath, name)
				if writeEntity(entities, seen, eid, name, EntityTypeType, idx.projectID, now) {
					stats.EntitiesCreated++
					*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
					stats.RelationsCreated++
				}
				continue
			}
		}

		// ── Root operation fields (written at depth 1) ───────────────────

		if inRootOp && depthBefore == 1 {
			if m := gqlFieldRe.FindStringSubmatch(line); m != nil {
				name := m[1]
				eid := fmt.Sprintf("function:%s:%s", relPath, name)
				if writeEntity(entities, seen, eid, name, EntityTypeFunction, idx.projectID, now) {
					stats.EntitiesCreated++
					*relations = append(*relations, relationRecord{FromID: fileID, ToID: eid, Type: RelContains})
					stats.RelationsCreated++
				}
			}
		}
	}
	return scanner.Err()
}

// graphqlMatchesPath returns true when the path looks like a GraphQL SDL file.
// Recognised extensions: .graphql (standard), .graphqls (schema-only convention),
// .gql (common shorthand).
func graphqlMatchesPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".graphql") ||
		strings.HasSuffix(lower, ".graphqls") ||
		strings.HasSuffix(lower, ".gql")
}
