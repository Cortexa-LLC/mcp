package knowledge

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cortexa-llc/mcp/kglib"
)

// indexSource writes one source file into a scratch project, indexes it, and
// returns the store.
func indexSource(t *testing.T, filename, source string) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, filename), []byte(source), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := OpenStore(filepath.Join(root, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	idx, err := NewIndexer(store, "proj", root)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return store, "proj"
}

// The whole point: unexported symbols are indexed rather than dropped, and
// carry a visibility that says which they are.
func TestIndexerRecordsGoVisibility(t *testing.T) {
	store, projectID := indexSource(t, "main.go", `package main

func Exported() {}

func unexported() {}

type ExportedType struct{}

type unexportedType struct{}
`)

	for _, tc := range []struct {
		name       string
		entityType string
		want       string
	}{
		{"Exported", EntityTypeFunction, kglib.VisibilityPublic},
		{"unexported", EntityTypeFunction, kglib.VisibilityPrivate},
		{"ExportedType", EntityTypeType, kglib.VisibilityPublic},
		{"unexportedType", EntityTypeType, kglib.VisibilityPrivate},
	} {
		entity, err := store.GetEntityByNameAndType(tc.name, tc.entityType, projectID)
		if err != nil {
			t.Fatalf("GetEntityByNameAndType(%s): %v", tc.name, err)
		}
		if entity == nil {
			t.Errorf("%s was not indexed at all", tc.name)
			continue
		}
		if entity.Visibility != tc.want {
			t.Errorf("%s visibility = %q, want %q", tc.name, entity.Visibility, tc.want)
		}
	}
}

// Python's convention is a leading underscore, not capitalisation. The
// classifier is per-language and must not have been collapsed into Go's rule.
func TestIndexerRecordsPythonVisibility(t *testing.T) {
	store, projectID := indexSource(t, "mod.py", `
def public_function():
    pass

def _private_function():
    pass
`)

	public, err := store.GetEntityByNameAndType("public_function", EntityTypeFunction, projectID)
	if err != nil || public == nil {
		t.Fatalf("public_function not indexed: %v", err)
	}
	if public.Visibility != kglib.VisibilityPublic {
		t.Errorf("public_function visibility = %q, want %q", public.Visibility, kglib.VisibilityPublic)
	}

	private, err := store.GetEntityByNameAndType("_private_function", EntityTypeFunction, projectID)
	if err != nil {
		t.Fatalf("GetEntityByNameAndType: %v", err)
	}
	if private == nil {
		t.Fatal("_private_function was not indexed")
	}
	if private.Visibility != kglib.VisibilityPrivate {
		t.Errorf("_private_function visibility = %q, want %q", private.Visibility, kglib.VisibilityPrivate)
	}
}

// Entity kinds with no visibility concept must record none, rather than being
// defaulted to private and quietly demoted in search.
func TestNonSymbolEntitiesHaveNoVisibility(t *testing.T) {
	store, projectID := indexSource(t, "main.go", "package main\n\nfunc Run() {}\n")

	file, err := store.GetEntityByNameAndType("main.go", EntityTypeFile, projectID)
	if err != nil || file == nil {
		t.Fatalf("file entity missing: %v", err)
	}
	if file.Visibility != "" {
		t.Errorf("file entity visibility = %q, want empty", file.Visibility)
	}
}

// A `package main` command layer is the case that motivated all of this:
// nothing in it can be exported, so under the old filter it was entirely
// invisible to search.
func TestCommandLayerIsSearchable(t *testing.T) {
	store, projectID := indexSource(t, "main.go", `package main

func runIndexCommand() {}

func resolveScopeConfig() {}
`)

	results, err := store.KeywordSearch(projectID, "resolveScopeConfig", 10)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("an unexported command-layer function is still not searchable")
	}

	var found bool
	for _, r := range results {
		if r.Entity.Name == "resolveScopeConfig" {
			found = true
			if r.Entity.Visibility != kglib.VisibilityPrivate {
				t.Errorf("visibility = %q, want %q", r.Entity.Visibility, kglib.VisibilityPrivate)
			}
		}
	}
	if !found {
		t.Errorf("resolveScopeConfig missing from %d results", len(results))
	}
}

// The MCP search tool exposes the same filter as the CLI, and defaults to
// including unexported symbols.
func TestMCPSearchPublicOnly(t *testing.T) {
	root := t.TempDir()
	aiDir := filepath.Join(root, ".ai")
	if err := os.MkdirAll(aiDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	source := "package main\n\nfunc HandlerExported() {}\n\nfunc handlerUnexported() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := OpenStore(filepath.Join(aiDir, "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	idx, err := NewIndexer(store, "proj", root)
	if err != nil {
		t.Fatalf("NewIndexer: %v", err)
	}
	if _, err := idx.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	store.Close()

	_, handlers := buildTools(aiDir, nil, "proj", root, PersonalConfig{})

	names := func(v any) []string {
		results, ok := v.([]*SearchResult)
		if !ok {
			t.Fatalf("unexpected result type %T", v)
		}
		var out []string
		for _, r := range results {
			out = append(out, r.Entity.Name)
		}
		return out
	}

	all, err := mcpCall(t, handlers, "search_knowledge", map[string]interface{}{"query": "handler"})
	if err != nil {
		t.Fatalf("search_knowledge: %v", err)
	}
	got := names(all)
	if !slices.Contains(got, "handlerUnexported") {
		t.Errorf("default search omitted the unexported symbol: %v", got)
	}
	if !slices.Contains(got, "HandlerExported") {
		t.Errorf("default search omitted the exported symbol: %v", got)
	}

	public, err := mcpCall(t, handlers, "search_knowledge", map[string]interface{}{
		"query": "handler", "public_only": true,
	})
	if err != nil {
		t.Fatalf("search_knowledge public_only: %v", err)
	}
	got = names(public)
	if slices.Contains(got, "handlerUnexported") {
		t.Errorf("public_only returned an unexported symbol: %v", got)
	}
	if !slices.Contains(got, "HandlerExported") {
		t.Errorf("public_only dropped the exported symbol: %v", got)
	}
}
