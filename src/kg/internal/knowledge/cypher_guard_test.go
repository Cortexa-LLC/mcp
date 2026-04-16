package knowledge

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
			if err := isReadOnlyCypher(tc.query); err != nil {
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
			err := isReadOnlyCypher(tc.query)
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
