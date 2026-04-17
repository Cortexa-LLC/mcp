package knowledge

import (
	"bufio"
	"fmt"
	"os"

	"github.com/cortexa-llc/mcp/kg/internal/mcp"
)

// RunMCPServer exposes Store APIs as MCP tools over stdio (MCP protocol).
//
// Open-use-close pattern: each tool handler opens the database, executes its
// operation, and closes the database before returning.  No connection is held
// between calls, so `kg index` (and any other CLI commands) can run at any time
// without hitting a "database is locked" error.
//
// Reads open in read-only mode (multiple concurrent readers allowed).
// Writes open in write mode (exclusive, but released immediately after the call).
//
// If scopeConfig is provided and has layers, reads use FederatedStore for
// cross-layer queries. Writes always go to the primary scope's database.
// KeywordSearcher interface for search operations (implemented by both Store and FederatedStore)
type KeywordSearcher interface {
	KeywordSearch(projectID, query string, limit int) ([]*SearchResult, error)
	HybridSearch(projectID, query string, queryEmbedding []float32, config SearchConfig) ([]*SearchResult, error)
}

func RunMCPServer(aiDir string, scopeConfig *ScopeConfig, projectID, projectRoot string) error {
	// jsonSchema is a helper to build a minimal JSON Schema object descriptor.
	jsonSchema := func(props map[string]string, required ...string) map[string]interface{} {
		properties := map[string]interface{}{}
		for k, typ := range props {
			properties[k] = map[string]string{"type": typ}
		}
		req := make([]string, len(required))
		copy(req, required)
		return map[string]interface{}{
			"type":       "object",
			"properties": properties,
			"required":   req,
		}
	}

	tools := []mcp.Tool{
		{
			Name:        "get_preflight_context",
			Description: "Returns a formatted context block of relevant knowledge graph entities for a given task description. Called automatically before each agent task.",
			InputSchema: jsonSchema(map[string]string{"task": "string"}, "task"),
		},
		{
			Name:        "search_knowledge",
			Description: "Hybrid search for entities and observations in the knowledge graph. Returns matching functions, types, files, and topics. Use short, specific terms (1–3 words); each whitespace-separated token is matched independently (OR logic), so prefer concise queries over long phrases.",
			InputSchema: jsonSchema(map[string]string{"query": "string", "limit": "integer"}, "query"),
		},
		{
			Name:        "add_entity",
			Description: "Create or upsert an entity in the knowledge graph. Type should be one of: function, type, file, module, topic, package, import. Returns the entity ID.",
			InputSchema: jsonSchema(map[string]string{"name": "string", "type": "string"}, "name", "type"),
		},
		{
			Name:        "add_observation",
			Description: "Attach a text observation or note to an existing entity (e.g. a bug found, a design decision, a caveat discovered during the task).",
			InputSchema: jsonSchema(map[string]string{"entity_id": "string", "content": "string"}, "entity_id", "content"),
		},
		{
			Name:        "link_entities",
			Description: "Create a directed relation between two entities. Relation must be one of: CONTAINS, IMPORTS, CALLS, IMPLEMENTS, BELONGS_TO, DEPENDS_ON, RELATES_TO.",
			InputSchema: jsonSchema(map[string]string{"from_id": "string", "relation": "string", "to_id": "string"}, "from_id", "relation", "to_id"),
		},
		{
			Name:        "get_file_context",
			Description: "Return all entities associated with a file path (functions, types, imports defined in that file).",
			InputSchema: jsonSchema(map[string]string{"file": "string"}, "file"),
		},
		{
			Name:        "query_graph",
			Description: "Run a read-only Cypher query against the knowledge graph. Only MATCH/RETURN queries are allowed.",
			InputSchema: jsonSchema(map[string]string{"cypher": "string"}, "cypher"),
		},
		{
			Name:        "index_project",
			Description: "Re-index the entire project codebase into the knowledge graph (scans all source files, updates entities and relations). Call this after making significant code changes.",
			InputSchema: jsonSchema(map[string]string{}),
		},
	}

	// Determine database path (legacy or scoped)
	var dbPath string
	useFederation := false
	if scopeConfig != nil {
		dbPath = fmt.Sprintf("%s/%s", aiDir, scopeConfig.Database)
		useFederation = len(scopeConfig.Layers) > 0
	} else {
		dbPath = fmt.Sprintf("%s/knowledge.db", aiDir)
	}

	// withRO opens the DB in read-only mode, runs fn, then closes.
	withRO := func(fn func(*Store) (any, error)) (any, error) {
		s, err := OpenStoreReadOnly(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open store: %w", err)
		}
		defer s.Close()
		return fn(s)
	}

	// withRW opens the DB in read-write mode, runs fn, then closes.
	withRW := func(fn func(*Store) (any, error)) (any, error) {
		s, err := OpenStore(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open store: %w", err)
		}
		defer s.Close()
		return fn(s)
	}

	// withSearch opens the appropriate store for search operations.
	// Uses federated store if scope has layers, otherwise single store.
	withSearch := func(fn func(KeywordSearcher) (any, error)) (any, error) {
		if useFederation {
			fs, err := OpenFederatedStore(aiDir, scopeConfig, true)
			if err != nil {
				return nil, fmt.Errorf("open federated store: %w", err)
			}
			defer fs.Close()
			return fn(fs)
		}

		s, err := OpenStoreReadOnly(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open store: %w", err)
		}
		defer s.Close()
		return fn(s)
	}

	handlers := map[string]mcp.ToolHandler{
		"get_preflight_context": func(req *mcp.ToolCallRequest) (any, error) {
			return withSearch(func(s KeywordSearcher) (any, error) {
				task, _ := req.Arguments["task"].(string)
				entities, err := s.KeywordSearch(projectID, task, 16)
				if err != nil {
					return nil, err
				}
				res := "---\nRelevant Knowledge Entities for Task\n---\n"
				for _, e := range entities {
					if e.Entity != nil {
						res += "- " + e.Entity.Name + " (" + e.Entity.Type + ")\n"
					}
				}
				return res, nil
			})
		},

		"search_knowledge": func(req *mcp.ToolCallRequest) (any, error) {
			return withSearch(func(s KeywordSearcher) (any, error) {
				q, _ := req.Arguments["query"].(string)
				lim, _ := req.Arguments["limit"].(float64)
				if lim == 0 {
					lim = 12
				}
				return s.KeywordSearch(projectID, q, int(lim))
			})
		},

		"get_file_context": func(req *mcp.ToolCallRequest) (any, error) {
			return withRO(func(s *Store) (any, error) {
				file, _ := req.Arguments["file"].(string)
				return s.ListEntities(projectID, file)
			})
		},

		"query_graph": func(req *mcp.ToolCallRequest) (any, error) {
			return withRO(func(s *Store) (any, error) {
				cypher, _ := req.Arguments["cypher"].(string)
				if err := isReadOnlyCypher(cypher); err != nil {
					return nil, err
				}
				result, err := s.query(cypher)
				if err != nil {
					return nil, fmt.Errorf("query: %w", err)
				}
				defer result.Close()
				var rows [][]any
				for result.HasNext() {
					tuple, err := result.Next()
					if err != nil {
						return nil, err
					}
					cols, err := tuple.GetAsSlice()
					tuple.Close()
					if err != nil {
						return nil, err
					}
					rows = append(rows, cols)
				}
				return rows, nil
			})
		},

		"add_entity": func(req *mcp.ToolCallRequest) (any, error) {
			return withRW(func(s *Store) (any, error) {
				name, _ := req.Arguments["name"].(string)
				typeStr, _ := req.Arguments["type"].(string)
				return s.CreateEntity(name, typeStr, projectID)
			})
		},

		"add_observation": func(req *mcp.ToolCallRequest) (any, error) {
			return withRW(func(s *Store) (any, error) {
				entityID, _ := req.Arguments["entity_id"].(string)
				content, _ := req.Arguments["content"].(string)
				return s.CreateObservation(entityID, content, projectID)
			})
		},

		"link_entities": func(req *mcp.ToolCallRequest) (any, error) {
			return withRW(func(s *Store) (any, error) {
				from, _ := req.Arguments["from_id"].(string)
				rel, _ := req.Arguments["relation"].(string)
				to, _ := req.Arguments["to_id"].(string)
				return nil, s.CreateRelation(from, to, rel, projectID)
			})
		},

		"index_project": func(req *mcp.ToolCallRequest) (any, error) {
			return withRW(func(s *Store) (any, error) {
				indexer, err := NewIndexer(s, projectID, projectRoot)
				if err != nil {
					return nil, fmt.Errorf("create indexer: %w", err)
				}
				stats, err := indexer.Index()
				if err != nil {
					return nil, fmt.Errorf("index project: %w", err)
				}
				return fmt.Sprintf("Indexed %d files, created %d entities and %d relations in project '%s'",
					stats.FilesScanned, stats.EntitiesCreated, stats.RelationsCreated, projectID), nil
			})
		},
	}

	server := mcp.NewServer(tools, handlers, bufio.NewReader(os.Stdin), os.Stdout)
	return server.Serve()
}
