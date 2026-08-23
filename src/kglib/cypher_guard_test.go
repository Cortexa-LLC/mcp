package kglib

import (
	"strings"
	"testing"
)

func TestIsReadOnlyCypher_AllowsReadQueries(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{
			name:  "simple MATCH RETURN",
			query: "MATCH (e:Entity) RETURN e",
		},
		{
			name:  "MATCH with WHERE",
			query: "MATCH (e:Entity) WHERE e.name = 'Alice' RETURN e",
		},
		{
			name:  "MATCH with ORDER BY and LIMIT",
			query: "MATCH (e:Entity) RETURN e ORDER BY e.name LIMIT 10",
		},
		{
			name:  "OPTIONAL MATCH",
			query: "OPTIONAL MATCH (e:Entity) RETURN e",
		},
		{
			name:  "WITH clause",
			query: "MATCH (e:Entity) WITH e RETURN e",
		},
		{
			name:  "UNWIND",
			query: "UNWIND [1,2,3] AS x RETURN x",
		},
		{
			name:  "count aggregation",
			query: "MATCH (e:Entity) RETURN count(e)",
		},
		{
			name:  "multi-hop relationship",
			query: "MATCH (a:Entity)-[r]->(b:Entity) RETURN a, r, b",
		},
		{
			name:  "SKIP and LIMIT",
			query: "MATCH (e:Entity) RETURN e SKIP 5 LIMIT 5",
		},
		{
			name:  "lowercase match return",
			query: "match (e:Entity) return e",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := IsReadOnlyCypher(tc.query); err != nil {
				t.Errorf("expected nil error for read-only query %q, got: %v", tc.query, err)
			}
		})
	}
}

func TestIsReadOnlyCypher_RejectsWriteQueries(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		keyword string
	}{
		{
			name:    "CREATE node",
			query:   "CREATE (e:Entity {name: 'Bob'})",
			keyword: "CREATE",
		},
		{
			name:    "MERGE node",
			query:   "MERGE (e:Entity {name: 'Alice'}) RETURN e",
			keyword: "MERGE",
		},
		{
			name:    "DELETE node",
			query:   "MATCH (e:Entity) DELETE e",
			keyword: "DELETE",
		},
		{
			name:    "DETACH DELETE",
			query:   "MATCH (e:Entity) DETACH DELETE e",
			keyword: "DETACH",
		},
		{
			name:    "SET property",
			query:   "MATCH (e:Entity) SET e.name = 'Carol'",
			keyword: "SET",
		},
		{
			name:    "REMOVE property",
			query:   "MATCH (e:Entity) REMOVE e.name",
			keyword: "REMOVE",
		},
		{
			name:    "DROP index",
			query:   "DROP INDEX idx",
			keyword: "DROP",
		},
		{
			name:    "CALL procedure",
			query:   "CALL db.labels()",
			keyword: "CALL",
		},
		{
			name:    "LOAD CSV",
			query:   "LOAD CSV FROM 'file.csv' AS line RETURN line",
			keyword: "LOAD",
		},
		{
			name:    "FOREACH",
			query:   "FOREACH (i IN [1,2,3] | CREATE (:Node {v: i}))",
			keyword: "FOREACH",
		},
		{
			name:    "lowercase create",
			query:   "create (e:Entity {name: 'x'})",
			keyword: "CREATE",
		},
		{
			name:    "mixed case Create",
			query:   "Create (e:Entity {name: 'x'})",
			keyword: "CREATE",
		},
		{
			name:    "CREATE after valid MATCH",
			query:   "MATCH (e:Entity) WHERE e.name='X' CREATE (n:New {id:1})",
			keyword: "CREATE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := IsReadOnlyCypher(tc.query)
			if err == nil {
				t.Errorf("expected error for query with %q keyword, got nil", tc.keyword)
				return
			}
			if !strings.Contains(strings.ToUpper(err.Error()), tc.keyword) {
				t.Errorf("expected error to mention %q, got: %v", tc.keyword, err)
			}
		})
	}
}

// TestIsReadOnlyCypher_RejectsFilesystemVerbs covers the Kuzu verbs that reach the
// host filesystem, network or catalog rather than the graph. These are exactly the
// ones an "is it a write query?" deny-list is prone to omit, because none of them
// mutate the graph.
func TestIsReadOnlyCypher_RejectsFilesystemVerbs(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		keyword string
	}{
		{
			name:    "COPY TO writes an arbitrary file",
			query:   "COPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/pwned.csv'",
			keyword: "COPY",
		},
		{
			name:    "EXPORT DATABASE writes a directory tree",
			query:   "EXPORT DATABASE '/tmp/pwned-export'",
			keyword: "EXPORT",
		},
		{
			name:    "IMPORT DATABASE replays arbitrary DDL",
			query:   "IMPORT DATABASE '/tmp/evil-export'",
			keyword: "IMPORT",
		},
		{
			name:    "ATTACH mounts another database",
			query:   "ATTACH '/tmp/other.db' AS other (dbtype kuzu)",
			keyword: "ATTACH",
		},
		{
			name:    "INSTALL downloads a native extension",
			query:   "INSTALL httpfs",
			keyword: "INSTALL",
		},
		{
			name:    "USE switches database",
			query:   "USE other",
			keyword: "USE",
		},
		{
			name:    "lowercase copy",
			query:   "copy (match (e:Entity) return e.name) to '/tmp/pwned.csv'",
			keyword: "COPY",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := IsReadOnlyCypher(tc.query); err == nil {
				t.Fatalf("query %q was accepted by the read-only guard, but it reaches the host filesystem/network", tc.query)
			}
		})
	}
}

// TestIsReadOnlyCypher_RejectsHiddenVerbs asserts the property that matters -- no
// accepted query can carry a non-read clause -- rather than the artifact of which
// keyword the deny-list happened to spot. Kuzu accepts `//` and block comments and
// multi-statement input, all of which were verified to execute against a read-only
// handle, so each of these payloads really does run if it gets past the guard.
func TestIsReadOnlyCypher_RejectsHiddenVerbs(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{
			name:  "block comment in front of the verb",
			query: "/* MATCH (e:Entity) */ COPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/pwned.csv'",
		},
		{
			name:  "line comment in front of the verb",
			query: "// harmless looking\nCOPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/pwned.csv'",
		},
		{
			name:  "second statement after a valid read",
			query: "MATCH (e:Entity) RETURN e.name; COPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/pwned.csv'",
		},
		{
			name:  "leading whitespace and newlines",
			query: "\n\t   COPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/pwned.csv'",
		},
		{
			name:  "comment between statements",
			query: "MATCH (e:Entity) RETURN e.name ; /* x */ EXPORT DATABASE '/tmp/x'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := IsReadOnlyCypher(tc.query); err == nil {
				t.Fatalf("guard accepted %q, which executes a non-read clause", tc.query)
			}
		})
	}
}

// TestIsReadOnlyCypher_RejectsUnknownStatementStarters is the check that does not
// depend on the deny-list being complete. None of these words appear in
// writeMutatingKeywords; they must still be refused, because a statement that does
// not begin with a read clause is not a read.
func TestIsReadOnlyCypher_RejectsUnknownStatementStarters(t *testing.T) {
	cases := []string{
		"ALTER TABLE Entity RENAME TO Other",
		"BEGIN TRANSACTION",
		"CHECKPOINT",
		"CONVERTIBLE_TO_ANYTHING (e:Entity) RETURN e",
		"EXPLAIN MATCH (e:Entity) RETURN e",
		"PROFILE MATCH (e:Entity) RETURN e",
	}

	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			if err := IsReadOnlyCypher(q); err == nil {
				t.Fatalf("guard accepted %q, which does not begin with a read-only clause", q)
			}
		})
	}
}

// TestIsReadOnlyCypher_LiteralsDoNotSplitStatements pins the scanner's handling of
// quoting: a `;` or a `//` inside a string literal is data, not a statement
// separator or a comment, and must not cause the tail of the query to escape the
// leading-clause check.
func TestIsReadOnlyCypher_LiteralsDoNotSplitStatements(t *testing.T) {
	allowed := []string{
		"MATCH (e:Entity) WHERE e.name = 'a;b' RETURN e",
		`MATCH (e:Entity) WHERE e.name = "a;b" RETURN e`,
		`MATCH (e:Entity) WHERE e.name = 'it\'s' RETURN e`,
		"MATCH (e:Entity) WHERE e.url = 'http://example.com' RETURN e",
		"MATCH (e:Entity) RETURN e // trailing comment",
		"MATCH (e:Entity) /* inline */ RETURN e",
	}
	for _, q := range allowed {
		t.Run("allow/"+q, func(t *testing.T) {
			if err := IsReadOnlyCypher(q); err != nil {
				t.Fatalf("guard rejected legitimate read query %q: %v", q, err)
			}
		})
	}

	// A literal must not be able to smuggle a whole statement past the split.
	hidden := `MATCH (e:Entity) WHERE e.name = 'x ; ATTACH y' RETURN e`
	if err := IsReadOnlyCypher(hidden); err == nil {
		t.Fatalf("guard accepted %q; ATTACH inside a literal should still trip the deny-list", hidden)
	}
}

// TestIsReadOnlyCypher_FailsClosedOnMalformedInput: if the scanner cannot tell
// where a literal or comment ends it cannot tell what the statements are, so it
// must refuse rather than guess.
func TestIsReadOnlyCypher_FailsClosedOnMalformedInput(t *testing.T) {
	cases := []string{
		"MATCH (e:Entity) WHERE e.name = 'unterminated RETURN e",
		"MATCH (e:Entity) /* unterminated RETURN e",
		`MATCH (e:Entity) WHERE e.name = "unterminated RETURN e`,
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			if err := IsReadOnlyCypher(q); err == nil {
				t.Fatalf("guard accepted lexically unresolvable query %q instead of failing closed", q)
			}
		})
	}
}
