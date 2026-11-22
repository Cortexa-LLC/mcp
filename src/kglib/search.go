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
	// Filter narrows results in the database rather than afterwards.
	Filter SearchFilter

	// Now is the reference instant for recency scoring. Zero means "sample it".
	// A federated search sets it once so every layer scores against the same
	// clock; without that, layers queried microseconds apart produce scores
	// that cannot be compared and an order that changes between runs.
	Now time.Time

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

// SearchFilter narrows what a search returns, in the database rather than after
// the fact — see KeywordSearchFiltered for why that distinction matters.
type SearchFilter struct {
	// PublicOnly drops symbols whose visibility is private. Entities with no
	// visibility — files, topics, hand-written knowledge — are kept: they have
	// no API surface to be outside of.
	PublicOnly bool
}

// defaultVisibilityPenalty is how far an unexported symbol sinks relative to an
// exported one of the same relevance.
//
// It must exceed the largest recency contribution — RecencyWeight (0.1) times a
// recency score of at most 1 — or a recently-touched unexported symbol
// outranks an exported one of equal relevance and the demotion is not reliable.
const defaultVisibilityPenalty = 0.15

// recencyNow returns the reference instant for recency scoring.
func (c SearchConfig) recencyNow() time.Time {
	if c.Now.IsZero() {
		return time.Now().UTC()
	}
	return c.Now
}

// visibilityPenalty returns a penalty that still dominates this config's
// recency weight, so a caller raising RecencyWeight does not silently disable
// the visibility ordering.
func (c SearchConfig) visibilityPenalty() float64 {
	if p := c.RecencyWeight * 1.5; p > defaultVisibilityPenalty {
		return p
	}
	return defaultVisibilityPenalty
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
	return s.KeywordSearchFiltered(projectID, query, limit, SearchFilter{})
}

// KeywordSearchFiltered is KeywordSearch with the filter applied in the
// database rather than by the caller.
//
// Filtering afterwards is not equivalent, and the difference is not subtle:
// LIMIT truncates before a caller-side filter can run, so asking for 20 public
// results returns however many of the top 20 rows happen to be public — often
// none. Indexing unexported symbols multiplied the candidate pool and made that
// routine. The predicate has to be in the query.
func (s *Store) KeywordSearchFiltered(projectID, query string, limit int, filter SearchFilter) ([]*SearchResult, error) {
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

	// Only when the column exists: a database written before it does not have
	// the property, and Kuzu rejects the whole query for a missing one.
	visibilityClause := ""
	if filter.PublicOnly && s.hasVisibility {
		visibilityClause = fmt.Sprintf("\n\t\t  AND (e.visibility IS NULL OR e.visibility <> '%s')", VisibilityPrivate)
	}

	// The id tiebreak is what makes truncation reproducible. Ordering by
	// updated_at alone leaves rows with equal timestamps in whatever order the
	// engine yields, and LIMIT then keeps an arbitrary subset of them — so two
	// identical queries against an unchanged database return different results.
	// Indexed rows share a timestamp routinely: the indexer stamps one `now`
	// per file, so every symbol in a file ties.
	//
	// LIMIT takes an integer expression; using %d (not user input) is safe here.
	cypherQuery := fmt.Sprintf(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id`+visibilityClause+`
		  AND (%s
		       OR EXISTS {
		         MATCH (e)-[:HAS_OBSERVATION]->(o:Observation)
		         WHERE %s
		       })
		RETURN DISTINCT `+s.entityColumns()+`
		ORDER BY e.updated_at DESC, e.id
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

		entity := entityFromRow(row)

		entities = append(entities, entity)
	}

	// Second pass: fetch observations for all entities in a single query.
	entityIDs := make([]string, len(entities))
	for i, e := range entities {
		entityIDs[i] = e.ID
	}
	obsMap, err := s.batchGetObservations(entityIDs, projectID, 3)
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

	rankResults(results, defaultVisibilityPenalty)

	return results, nil
}

// GetTopObservations retrieves the most recent observations for an entity
func (s *Store) GetTopObservations(entityID, projectID string, limit int) ([]*Observation, error) {
	// The id tiebreak is what makes truncation reproducible. Ordering by
	// updated_at alone leaves rows with equal timestamps in whatever order the
	// engine yields, and LIMIT then keeps an arbitrary subset of them — so two
	// identical queries against an unchanged database return different results.
	// Indexed rows share a timestamp routinely: the indexer stamps one `now`
	// per file, so every symbol in a file ties.
	//
	// Scoped by project through the parent entity, not by o.entity_id alone:
	// Observation has no project_id column, so matching the edge from an Entity
	// filtered on project_id is what keeps a project-A search from pulling in a
	// project-B observation that shares the entity_id. GetObservations takes the
	// same route.
	//
	// LIMIT takes an integer expression; using %d (not user input) is safe here.
	query := fmt.Sprintf(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.project_id = $project_id AND o.entity_id = $entity_id
		RETURN o.id, o.entity_id, o.content, o.created_at
		ORDER BY o.created_at DESC
		LIMIT %d
	`, limit)

	result, err := s.QueryParams(query, map[string]any{"entity_id": entityID, "project_id": projectID})
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

		// stringOrEmpty, not a bare .(string): a NULL content or entity_id comes
		// back as a nil interface and a bare assertion panics on it. Nothing in
		// this process recovers, and the MCP server dispatches tool handlers
		// synchronously, so that panic ends the agent's whole session rather than
		// one query. Bulk loading writes these columns through COPY FROM JSON,
		// which the Go type system never sees.
		obs := &Observation{
			ID:       stringOrEmpty(row[0]),
			EntityID: stringOrEmpty(row[1]),
			Content:  stringOrEmpty(row[2]),
		}

		// Parse timestamp (Kuzu returns timestamps as int64 microseconds)
		obs.CreatedAt = timeOrZero(row[3])

		observations = append(observations, obs)
	}

	return observations, nil
}

// batchGetObservations fetches the top-N most recent observations for each entity
// in entityIDs using a single Cypher query, eliminating N+1 round-trips.
// It returns a map keyed by entity ID; entities with no observations are absent.
// limit is applied per entity: only the newest `limit` rows are kept.
//
// projectID scopes the read: Observation carries no project_id, so under
// multi-project-per-DB two projects sharing an entity_id would otherwise cross-
// contaminate each other's search results. Matching the HAS_OBSERVATION edge
// from an Entity filtered on project_id confines the result to this project.
func (s *Store) batchGetObservations(entityIDs []string, projectID string, limit int) (map[string][]*Observation, error) {
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
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.project_id = $project_id AND o.entity_id IN $entity_ids
		RETURN o.id, o.entity_id, o.content, o.created_at
		ORDER BY o.entity_id, o.created_at DESC
	`

	result, err := s.QueryParams(query, map[string]any{"entity_ids": ids, "project_id": projectID})
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

		// This loop runs on every keyword search, so a bare .(string) here makes a
		// single NULL observation column enough to take down any session that
		// searches. See GetTopObservations above.
		entityID := stringOrEmpty(row[1])
		if counts[entityID] >= limit {
			continue
		}
		counts[entityID]++

		obs := &Observation{
			ID:       stringOrEmpty(row[0]),
			EntityID: entityID,
			Content:  stringOrEmpty(row[2]),
		}
		obs.CreatedAt = timeOrZero(row[3])

		obsMap[entityID] = append(obsMap[entityID], obs)
	}

	return obsMap, nil
}

// VectorSearch performs semantic search using an in-memory HNSW index.
// The index is lazily built on the first call per project and invalidated
// whenever SetEmbedding writes a new embedding, so results are always fresh.
// Entities and/or their observations must have been previously embedded via
// SetEmbedding / SetObservationEmbedding / BatchEmbed.
// Results are sorted by descending cosine similarity and capped at limit.
//
// The index holds a node per embedded observation as well as per embedded
// entity (see buildIndex), so several neighbours routinely resolve to the same
// entity — a document whose every section matched. Results are therefore
// deduplicated by entity, keeping each entity's best-matching vector, and the
// neighbour search over-fetches to compensate: asking HNSW for exactly limit
// neighbours would return far fewer than limit distinct entities.
func (s *Store) VectorSearch(projectID string, queryEmbedding []float32, limit int) ([]*SearchResult, error) {
	return s.VectorSearchFiltered(projectID, queryEmbedding, limit, SearchFilter{})
}

// VectorSearchFiltered is VectorSearch with the filter honoured, mirroring
// KeywordSearch / KeywordSearchFiltered. See VectorSearch for how the
// neighbour search and the per-entity collapse work.
func (s *Store) VectorSearchFiltered(projectID string, queryEmbedding []float32, limit int, filter SearchFilter) ([]*SearchResult, error) {
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

	// Collect entities and similarities from the k nearest neighbours, keeping
	// one candidate per entity: its best-matching vector. Without that, an
	// entity with many embedded observations occupies most of the result page.
	type candidate struct {
		entity     *Entity
		similarity float64
	}
	collect := func(k int) []candidate {
		// Search returns the k nearest neighbours sorted by ascending distance.
		// Node.Value is the stored vector; cosine similarity is ours to compute.
		neighbours := idx.graph.Search(queryEmbedding, k)
		best := make(map[string]int, len(neighbours)) // entity ID → candidates index
		out := make([]candidate, 0, len(neighbours))
		for _, node := range neighbours {
			entity, ok := idx.entities[node.Key]
			if !ok {
				continue
			}
			// The vector path has to exclude private entities itself. The
			// keyword path pushes PublicOnly into the Cypher WHERE clause, and
			// rankResults only *demotes* private entities by a score penalty —
			// it never drops them — so without this skip a private entity with
			// a strong vector match lands in a --public-only result set.
			//
			// The skip belongs here, inside the collection the widening loop
			// below re-runs, rather than after the search returns: results are
			// truncated to limit on the way out, so a private entity dropped
			// afterwards would already have consumed a result slot and left
			// the page under-filled. Skipping it during collection lets the
			// widening compensate and find a public entity to take its place.
			if filter.PublicOnly && entity.Visibility == VisibilityPrivate {
				continue
			}
			similarity := cosineSimilarity32(queryEmbedding, node.Value)
			if i, seen := best[entity.ID]; seen {
				if similarity > out[i].similarity {
					out[i].similarity = similarity
				}
				continue
			}
			best[entity.ID] = len(out)
			out = append(out, candidate{entity, similarity})
		}
		return out
	}

	// Observation nodes collapse onto their parent entity, so k neighbours yield
	// strictly fewer than k distinct entities. A fixed over-fetch factor is not
	// enough: a document with more embedded sections than the budget takes every
	// slot and crowds every other match out of the page completely — the better
	// the document matches, the more total the eviction. So widen the neighbour
	// search until the page can be filled, or until it covers the whole graph.
	// Measured against the index: a second entity ranked behind one document's
	// sections is missed at k=8 and k=16 and found at k=32, at every graph size
	// tried — and raising HNSW's EfSearch instead changed nothing, so the
	// neighbour count is the lever and the candidate-list size is not. Widening
	// stops at the graph size, past which there is nothing left to find; a page
	// can still under-fill when one document owns the whole neighbourhood, which
	// is an inherent limit of approximate search rather than a tuning problem.
	const vectorOverfetch = 4
	k := limit * vectorOverfetch
	candidates := collect(k)
	for len(candidates) < limit && k < idx.nodeCount {
		k *= 2
		candidates = collect(k)
	}

	// Batch-fetch observations for all candidate entities in a single query.
	entityIDs := make([]string, len(candidates))
	for i, c := range candidates {
		entityIDs[i] = c.entity.ID
	}
	obsMap, err := s.batchGetObservations(entityIDs, projectID, 3)
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
	rankResults(results, defaultVisibilityPenalty)

	// The over-fetch above is an implementation detail of hitting the target,
	// not a licence to return more than was asked for.
	if len(results) > limit {
		results = results[:limit]
	}

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

	// Fill in an unset limit without discarding the rest of the config. The
	// whole struct used to be replaced here, which silently dropped
	// config.Filter — so a caller that set PublicOnly but left Limit at zero
	// got an unfiltered search. Only the limit is missing; only the limit is
	// supplied.
	if config.Limit == 0 {
		config.Limit = DefaultSearchConfig().Limit
	}
	if config.Now.IsZero() {
		config.Now = time.Now().UTC()
	}

	// Collect results from both search methods
	var allResults []*SearchResult

	// Keyword search results
	if query != "" {
		keywordResults, err := s.KeywordSearchFiltered(projectID, query, config.Limit*2, config.Filter)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		allResults = append(allResults, keywordResults...)
	}

	// Vector search results (when implemented)
	if len(queryEmbedding) > 0 {
		vectorResults, err := s.VectorSearchFiltered(projectID, queryEmbedding, config.Limit*2, config.Filter)
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

	// Order is imposed by rankResults below, which is a total order — map
	// iteration order here does not survive it.
	var hybridResults []*SearchResult
	for _, result := range entityScores {
		// Apply recency boost
		recencyScore := calculateRecencyScore(result.Entity.UpdatedAt, config.recencyNow())
		result.Score = result.Score + config.RecencyWeight*recencyScore
		result.MatchType = "hybrid"
		hybridResults = append(hybridResults, result)
	}

	rankResults(hybridResults, config.visibilityPenalty())

	// Limit results
	if len(hybridResults) > config.Limit {
		hybridResults = hybridResults[:config.Limit]
	}

	return hybridResults, nil
}

// rankResults orders search results by effective relevance, which folds in a
// penalty for unexported symbols.
//
// This was a tie-break on exactly equal scores, and that never fired. The
// hybrid path adds config.RecencyWeight*recencyScore to every score, so two
// results are essentially never bit-identical float64s and the visibility term
// was dead code — while two documented places promised the behaviour.
//
// A penalty subtracted from the score composes instead of competing: it is
// applied once, at comparison time rather than by mutating Score, so it cannot
// be double-counted by a caller that ranks twice. It must exceed the largest
// recency contribution, or recency noise reorders across it — hence the
// relationship asserted in defaultVisibilityPenalty's comment.
//
// Only VisibilityPrivate is penalised. Empty visibility ranks with public: it
// covers files, markdown topics and hand-written knowledge, and hand-written
// knowledge is the last thing that should sink below an exported getter.
func rankResults(results []*SearchResult, penalty float64) {
	sort.Slice(results, func(i, j int) bool {
		si, sj := effectiveScore(results[i], penalty), effectiveScore(results[j], penalty)
		if si != sj {
			return si > sj
		}
		// A total order, not a stable sort over whatever order the caller
		// happened to build. Results are collected out of maps, and Go
		// randomises map iteration, so "stable" only preserves randomness —
		// identical queries against an unchanged database returned different
		// results because the limit then truncated a differently-ordered slice.
		// Ties are the normal case here: KeywordSearch scores everything 1.0,
		// and the indexer stamps one timestamp per file so recency ties too.
		return entityIDOf(results[i]) < entityIDOf(results[j])
	})
}

func entityIDOf(r *SearchResult) string {
	if r.Entity == nil {
		return ""
	}
	return r.Entity.ID
}

func effectiveScore(r *SearchResult, penalty float64) float64 {
	if visibilityRank(r) == 1 {
		return r.Score - penalty
	}
	return r.Score
}

func visibilityRank(r *SearchResult) int {
	if r.Entity != nil && r.Entity.Visibility == VisibilityPrivate {
		return 1
	}
	return 0
}

// calculateRecencyScore computes a recency boost based on entity update time
// Returns a score between 0.0 (very old) and 1.0 (very recent)
// calculateRecencyScore scores an entity's age against a caller-supplied
// reference instant.
//
// The instant is a parameter, not time.Now() sampled here, because every result
// in one query must be scored against the same clock. Sampling per call gave
// results from different layers — and even from the keyword and vector legs of
// one search — recency computed microseconds apart, so scores that should have
// tied differed in their last digits. The ordering then flipped between
// identical queries, which is how a "deterministic" merge stayed
// nondeterministic after the map iteration was fixed.
func calculateRecencyScore(updatedAt, now time.Time) float64 {
	if updatedAt.IsZero() {
		return 0.0
	}

	// Calculate age in days
	age := now.Sub(updatedAt)
	ageDays := age.Hours() / 24.0

	// Exponential decay: score = e^(-age/30)
	// Half-life of ~21 days
	score := math.Exp(-ageDays / 30.0)
	return math.Max(0.0, math.Min(1.0, score))
}
