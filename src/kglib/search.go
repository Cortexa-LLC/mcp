package kglib

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// SearchConfig defines search behavior and ranking weights
type SearchConfig struct {
	KeywordWeight float64 // α — keyword match weight, default 0.4
	RecencyWeight float64 // β — recency boost weight, default 0.1
	Limit         int     // maximum results to return, default 20
}

// DefaultSearchConfig returns the default search configuration
func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		KeywordWeight: 0.4,
		RecencyWeight: 0.1,
		Limit:         20,
	}
}

// SearchResult represents a single search result with score and metadata
type SearchResult struct {
	Entity       *Entity        `json:"entity"`
	Observations []*Observation `json:"observations,omitempty"`
	Score        float64        `json:"score"`
	MatchType    string         `json:"match_type"` // "keyword" | "vector" | "hybrid"
}

// KeywordSearch performs full-text search on entity names and observation content
// using Cypher's CONTAINS operator for case-insensitive substring matching.
// The query is tokenized on whitespace; tokens are matched with OR logic so that
// multi-word queries like "open closed ocp" find entities matching any term.
func (s *Store) KeywordSearch(projectID, query string, limit int) ([]*SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	if limit <= 0 {
		limit = 20
	}

	// Tokenize and normalize — each token matched independently (OR).
	rawTokens := strings.Fields(strings.ToLower(query))
	if len(rawTokens) == 0 {
		return nil, fmt.Errorf("query contains no searchable terms")
	}

	// Build per-token CONTAINS conditions using numbered parameters ($t0, $t1, …).
	// Parameter names are generated from an index (never from user input) so
	// interpolating them into the Cypher string is safe.
	params := map[string]any{"project_id": projectID}
	var nameConds, obsConds []string
	for i, tok := range rawTokens {
		key := fmt.Sprintf("t%d", i)
		params[key] = tok
		nameConds = append(nameConds, fmt.Sprintf("lower(e.name) CONTAINS $%s", key))
		obsConds = append(obsConds, fmt.Sprintf("lower(o.content) CONTAINS $%s", key))
	}
	nameClause := strings.Join(nameConds, " OR ")
	obsClause := strings.Join(obsConds, " OR ")

	// LIMIT takes an integer expression; using %d (not user input) is safe here.
	cypherQuery := fmt.Sprintf(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id
		  AND (%s
		       OR EXISTS {
		         MATCH (e)-[:HAS_OBSERVATION]->(o:Observation)
		         WHERE %s
		       })
		RETURN DISTINCT e.id, e.name, e.type, e.project_id, e.created_at, e.updated_at
		ORDER BY e.updated_at DESC
		LIMIT %d
	`, nameClause, obsClause, limit)

	result, err := s.QueryParams(cypherQuery, params)
	if err != nil {
		return nil, fmt.Errorf("execute keyword search: %w", err)
	}
	defer result.Close()

	// First pass: collect all matching entities.
	var entities []*Entity
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		if err != nil {
			return nil, fmt.Errorf("get row as slice: %w", err)
		}
		tuple.Close()

		entity := &Entity{
			ID:        row[0].(string),
			Name:      row[1].(string),
			Type:      row[2].(string),
			ProjectID: row[3].(string),
		}

		// Parse timestamps (Kuzu returns timestamps as int64 microseconds)
		if ts, ok := row[4].(int64); ok {
			entity.CreatedAt = time.UnixMicro(ts).UTC()
		}
		if ts, ok := row[5].(int64); ok {
			entity.UpdatedAt = time.UnixMicro(ts).UTC()
		}

		entities = append(entities, entity)
	}

	// Second pass: fetch observations for all entities in a single query.
	entityIDs := make([]string, len(entities))
	for i, e := range entities {
		entityIDs[i] = e.ID
	}
	obsMap, err := s.batchGetObservations(entityIDs, 3)
	if err != nil {
		// Degrade gracefully — empty observations rather than failing the search.
		obsMap = map[string][]*Observation{}
	}

	results := make([]*SearchResult, 0, len(entities))
	for _, entity := range entities {
		obs := obsMap[entity.ID]
		if obs == nil {
			obs = []*Observation{}
		}
		results = append(results, &SearchResult{
			Entity:       entity,
			Observations: obs,
			Score:        1.0,
			MatchType:    "keyword",
		})
	}

	return results, nil
}

// GetTopObservations retrieves the most recent observations for an entity
func (s *Store) GetTopObservations(entityID, projectID string, limit int) ([]*Observation, error) {
	// LIMIT takes an integer expression; using %d (not user input) is safe here.
	query := fmt.Sprintf(`
		MATCH (o:Observation)
		WHERE o.entity_id = $entity_id
		RETURN o.id, o.entity_id, o.content, o.created_at
		ORDER BY o.created_at DESC
		LIMIT %d
	`, limit)

	result, err := s.QueryParams(query, map[string]any{"entity_id": entityID})
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer result.Close()

	var observations []*Observation
	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		if err != nil {
			return nil, fmt.Errorf("get row as slice: %w", err)
		}
		tuple.Close()

		obs := &Observation{
			ID:       row[0].(string),
			EntityID: row[1].(string),
			Content:  row[2].(string),
		}

		// Parse timestamp (Kuzu returns timestamps as int64 microseconds)
		if ts, ok := row[3].(int64); ok {
			obs.CreatedAt = time.UnixMicro(ts).UTC()
		}

		observations = append(observations, obs)
	}

	return observations, nil
}

// batchGetObservations fetches the top-N most recent observations for each entity
// in entityIDs using a single Cypher query, eliminating N+1 round-trips.
// It returns a map keyed by entity ID; entities with no observations are absent.
// limit is applied per entity: only the newest `limit` rows are kept.
func (s *Store) batchGetObservations(entityIDs []string, limit int) (map[string][]*Observation, error) {
	if len(entityIDs) == 0 {
		return map[string][]*Observation{}, nil
	}

	// Build a []any slice because KuzuDB's prepared-statement executor requires
	// []any (not []string) to construct a Cypher LIST value.
	ids := make([]any, len(entityIDs))
	for i, id := range entityIDs {
		ids[i] = id
	}

	// ROW_NUMBER() is not available in KuzuDB Cypher; use ORDER BY + a per-entity
	// counter instead.  We fetch all rows ordered by (entity_id, created_at DESC)
	// and apply the per-entity limit in Go — this is still a single round-trip.
	query := `
		MATCH (o:Observation)
		WHERE o.entity_id IN $entity_ids
		RETURN o.id, o.entity_id, o.content, o.created_at
		ORDER BY o.entity_id, o.created_at DESC
	`

	result, err := s.QueryParams(query, map[string]any{"entity_ids": ids})
	if err != nil {
		return nil, fmt.Errorf("batch query observations: %w", err)
	}
	defer result.Close()

	obsMap := make(map[string][]*Observation, len(entityIDs))
	counts := make(map[string]int, len(entityIDs))

	for result.HasNext() {
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("get next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		if err != nil {
			return nil, fmt.Errorf("get row as slice: %w", err)
		}
		tuple.Close()

		entityID := row[1].(string)
		if counts[entityID] >= limit {
			continue
		}
		counts[entityID]++

		obs := &Observation{
			ID:       row[0].(string),
			EntityID: entityID,
			Content:  row[2].(string),
		}
		if ts, ok := row[3].(int64); ok {
			obs.CreatedAt = time.UnixMicro(ts).UTC()
		}

		obsMap[entityID] = append(obsMap[entityID], obs)
	}

	return obsMap, nil
}

// VectorSearch performs semantic search using an in-memory HNSW index.
// The index is lazily built on the first call per project and invalidated
// whenever SetEmbedding writes a new embedding, so results are always fresh.
// Entities must have been previously embedded via SetEmbedding / BatchEmbed.
// Results are sorted by descending cosine similarity and capped at limit.
func (s *Store) VectorSearch(projectID string, queryEmbedding []float32, limit int) ([]*SearchResult, error) {
	if len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("query embedding cannot be empty")
	}
	if limit <= 0 {
		limit = 20
	}

	// Obtain (or lazily build) the HNSW index for this project.
	idx, err := s.hnswIdx.get(projectID, func() (*projectIndex, error) {
		return s.buildIndex(projectID)
	})
	if err != nil {
		return nil, fmt.Errorf("build vector index: %w", err)
	}

	if len(idx.entities) == 0 {
		return []*SearchResult{}, nil
	}

	// Search returns the k nearest neighbours sorted by ascending distance.
	// Node.Value is the stored vector; we compute cosine similarity ourselves.
	neighbours := idx.graph.Search(queryEmbedding, limit)

	// Collect entity IDs and similarities from HNSW neighbours.
	type candidate struct {
		entity     *Entity
		similarity float64
	}
	candidates := make([]candidate, 0, len(neighbours))
	for _, node := range neighbours {
		entity, ok := idx.entities[node.Key]
		if !ok {
			continue
		}
		similarity := cosineSimilarity32(queryEmbedding, node.Value)
		candidates = append(candidates, candidate{entity, similarity})
	}

	// Batch-fetch observations for all candidate entities in a single query.
	entityIDs := make([]string, len(candidates))
	for i, c := range candidates {
		entityIDs[i] = c.entity.ID
	}
	obsMap, err := s.batchGetObservations(entityIDs, 3)
	if err != nil {
		obsMap = map[string][]*Observation{}
	}

	results := make([]*SearchResult, 0, len(candidates))
	for _, c := range candidates {
		obs := obsMap[c.entity.ID]
		if obs == nil {
			obs = []*Observation{}
		}
		results = append(results, &SearchResult{
			Entity:       c.entity,
			Observations: obs,
			Score:        c.similarity,
			MatchType:    "vector",
		})
	}

	// Sort results by descending cosine similarity so the caller always gets
	// the best matches first, regardless of the order HNSW returns them.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// cosineSimilarity32 computes the cosine similarity between two float32 vectors.
// Returns 0 if either vector is zero-length or a zero vector.
func cosineSimilarity32(a, b []float32) float64 {
	// Use the shorter length to avoid index out-of-range if dimensions differ.
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// HybridSearch combines keyword and vector search with configurable weights
func (s *Store) HybridSearch(projectID, query string, queryEmbedding []float32, config SearchConfig) ([]*SearchResult, error) {
	if query == "" && len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("either query or embedding must be provided")
	}

	// Use default config if not provided
	if config.Limit == 0 {
		config = DefaultSearchConfig()
	}

	// Collect results from both search methods
	var allResults []*SearchResult

	// Keyword search results
	if query != "" {
		keywordResults, err := s.KeywordSearch(projectID, query, config.Limit*2)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		allResults = append(allResults, keywordResults...)
	}

	// Vector search results (when implemented)
	if len(queryEmbedding) > 0 {
		vectorResults, err := s.VectorSearch(projectID, queryEmbedding, config.Limit*2)
		if err != nil {
			return nil, fmt.Errorf("vector search: %w", err)
		}
		allResults = append(allResults, vectorResults...)
	}

	// Deduplicate by entity ID and combine scores
	entityScores := make(map[string]*SearchResult)
	for _, result := range allResults {
		entityID := result.Entity.ID
		if existing, found := entityScores[entityID]; found {
			// Combine scores: weighted sum of keyword and semantic scores
			existing.Score += result.Score
			existing.MatchType = "hybrid"
		} else {
			entityScores[entityID] = result
		}
	}

	// Convert map back to slice
	var hybridResults []*SearchResult
	for _, result := range entityScores {
		// Apply recency boost
		recencyScore := calculateRecencyScore(result.Entity.UpdatedAt)
		result.Score = result.Score + config.RecencyWeight*recencyScore
		result.MatchType = "hybrid"
		hybridResults = append(hybridResults, result)
	}

	// Sort by score descending
	sort.Slice(hybridResults, func(i, j int) bool {
		return hybridResults[i].Score > hybridResults[j].Score
	})

	// Limit results
	if len(hybridResults) > config.Limit {
		hybridResults = hybridResults[:config.Limit]
	}

	return hybridResults, nil
}

// calculateRecencyScore computes a recency boost based on entity update time
// Returns a score between 0.0 (very old) and 1.0 (very recent)
func calculateRecencyScore(updatedAt time.Time) float64 {
	if updatedAt.IsZero() {
		return 0.0
	}

	// Calculate age in days
	now := time.Now().UTC()
	age := now.Sub(updatedAt)
	ageDays := age.Hours() / 24.0

	// Exponential decay: score = e^(-age/30)
	// Half-life of ~21 days
	score := math.Exp(-ageDays / 30.0)
	return math.Max(0.0, math.Min(1.0, score))
}
