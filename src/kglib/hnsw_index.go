package kglib

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/coder/hnsw"
)

// projectIndex holds a lazily built HNSW graph for a single project plus a
// reverse map from node key to the *Entity a hit resolves to, so we can
// reconstruct SearchResult without a second round-trip to Kuzu.
//
// A node key is not an entity ID. The graph holds two kinds of vector:
//
//   - an entity vector, keyed by the entity's own ID
//   - an observation vector, keyed by obsNodeKey(observation ID)
//
// Both resolve to the entity a result is reported against, so several keys can
// map to the same *Entity. Callers must therefore deduplicate by entity ID;
// VectorSearch does.
type projectIndex struct {
	graph    *hnsw.Graph[string]
	entities map[string]*Entity // node key → the entity a hit resolves to
	// nodeCount is how many vectors the graph holds. VectorSearch needs it as
	// the ceiling for growing k: without it, a search asking for more distinct
	// entities than the graph can supply would widen forever.
	nodeCount int
	builtAt   time.Time
}

// hnswSeed fixes the level-generation RNG so an index built twice from the
// same vectors has the same shape, and a query against it returns the same
// results.
//
// hnsw.NewGraph seeds from time.Now().UnixNano(). The index is in-memory and
// lazily rebuilt, so every process gets a different graph shape from identical
// data — and recall genuinely diverged on that: two runs of the same probe
// over the same 221 vectors disagreed about whether a given document was
// reachable at all. An approximate index legitimately misses things; it must
// at least miss the same things every time, for the same reason
// KeywordSearchFiltered carries an id tiebreak.
//
// No unit test pins this. Divergence needs a marginal neighbourhood, and every
// fixture small enough to build in a test returned identical results seeded or
// not — so a test here would pass either way and guarantee nothing. The one
// line stands on the mechanism: a clock-seeded index cannot be reproducible.
//
// The value itself is arbitrary. Only its fixedness matters.
const hnswSeed = 0x6b67 // "kg"

// obsNodeKey namespaces an observation's ID for use as an HNSW node key, so
// an observation vector can never collide with the entity vector of an entity
// that happens to share its ID.
func obsNodeKey(observationID string) string {
	return "obs:" + observationID
}

// vectorIndexCache manages per-project HNSW indices.
// It is embedded in Store and is safe for concurrent use.
type vectorIndexCache struct {
	mu      sync.RWMutex
	indices map[string]*projectIndex // project ID → index
}

func newVectorIndexCache() *vectorIndexCache {
	return &vectorIndexCache{
		indices: make(map[string]*projectIndex),
	}
}

// invalidate marks the index for projectID as stale so it will be rebuilt on
// the next call to VectorSearch.
func (c *vectorIndexCache) invalidate(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.indices, projectID)
}

// get returns the cached index for projectID, building it if necessary.
// It uses double-checked locking: a fast read-lock path for cache hits and a
// write-lock path (with a second check) to ensure only one goroutine triggers
// the expensive KuzuDB rebuild for any given project.
func (c *vectorIndexCache) get(projectID string, build func() (*projectIndex, error)) (*projectIndex, error) {
	// Fast path: allow concurrent reads.
	c.mu.RLock()
	idx, ok := c.indices[projectID]
	c.mu.RUnlock()
	if ok {
		return idx, nil
	}

	// Slow path: acquire write lock and double-check before building.
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok = c.indices[projectID]; ok {
		return idx, nil // another goroutine built it while we waited
	}
	idx, err := build()
	if err != nil {
		return nil, err
	}
	c.indices[projectID] = idx
	return idx, nil
}

// buildIndex fetches every embedded vector for projectID from Kuzu — entity
// vectors and observation vectors both — and constructs an HNSW graph. It is
// called lazily on the first VectorSearch after a cache miss or invalidation.
//
// Observation vectors are what make semantic search useful. An entity vector
// is the embedding of "<type>: <name>" (see BatchEmbed), so a graph indexing
// only those can match a query against a symbol name and nothing else; the
// prose — a PDF's text, a markdown section's body, a hand-written review
// finding — lives in observations. They were embedded and stored from the
// start, and until this query existed nothing ever read them.
func (s *Store) buildIndex(projectID string) (*projectIndex, error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)
		WHERE e.project_id = $project_id AND e.embedding IS NOT NULL
		RETURN `+s.entityColumns()+`, e.embedding
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return nil, fmt.Errorf("query entities for index build: %w", err)
	}
	defer result.Close()

	g := hnsw.NewGraph[string]()
	g.Distance = hnsw.CosineDistance           // smaller = more similar
	g.Rng = rand.New(rand.NewSource(hnswSeed)) //nolint:gosec // reproducibility, not secrecy

	entities := make(map[string]*Entity)
	nodes := make([]hnsw.Node[string], 0, 256)
	dropped := 0
	// Counted per row, not derived at the end: rows whose embedding column is
	// missing or empty are skipped before `dropped` is touched, so
	// dropped+len(nodes) would omit them and understate what was actually read.
	read := 0

	for result.HasNext() {
		read++
		tuple, err := result.Next()
		if err != nil {
			return nil, fmt.Errorf("index build next: %w", err)
		}

		row, err := tuple.GetAsSlice()
		if err != nil {
			tuple.Close()
			return nil, fmt.Errorf("index build row: %w", err)
		}
		tuple.Close()

		// Shares entityColumns/entityFromRow with every other entity
		// projection. This query was the one the centralisation missed, so
		// entities reached through the vector path carried no visibility and
		// slipped past --public-only while their keyword-found twins did not.
		entity := entityFromRow(row)

		// The embedding is appended after entityColumns, whose width varies with
		// what the database actually has — so index from the end, not by number.
		rawEmb, ok := row[len(row)-1].([]any)
		if !ok || len(rawEmb) == 0 {
			continue
		}
		emb, ok := embeddingFromRaw(rawEmb)
		if !ok {
			// A component of unhandled type must exclude the whole node: the
			// old behaviour left it at 0, silently distorting every distance
			// this vector participated in. Excluding it is correct but just as
			// invisible from the outside — the entity simply stops appearing
			// in vector results — so the count is reported below.
			dropped++
			continue
		}

		entities[entity.ID] = entity
		nodes = append(nodes, hnsw.MakeNode(entity.ID, emb))
	}

	obsRead, obsDropped, err := s.addObservationVectors(projectID, entities, &nodes)
	if err != nil {
		return nil, err
	}
	read += obsRead
	dropped += obsDropped

	if dropped > 0 {
		// Not an error — the index is still usable — but silently reduced
		// recall is exactly the kind of thing nobody discovers from search
		// results, so say it once per build.
		log.Printf("index build (project %s): dropped %d of %d vectors read, whose components had unhandled types",
			projectID, dropped, read)
	}

	if len(nodes) > 0 {
		g.Add(nodes...)
	}

	return &projectIndex{
		graph:     g,
		entities:  entities,
		nodeCount: len(nodes),
		builtAt:   time.Now().UTC(),
	}, nil
}

// addObservationVectors loads every embedded observation in the project and
// appends one node per observation, keyed by obsNodeKey and resolving to the
// parent entity. Returns how many rows were read and how many were dropped,
// which the caller folds into its own counts for the single build report.
//
// The parent entity is projected alongside, rather than looked up in the
// `entities` map already built: an entity carries a vector only once it has
// been embedded, and GetUnembeddedEntities/BatchEmbed can leave an entity
// un-embedded while its observations are embedded. Resolving through the map
// would silently drop exactly those — and a file entity whose only content is
// its observations is the common case for PDFs and markdown.
func (s *Store) addObservationVectors(
	projectID string,
	entities map[string]*Entity,
	nodes *[]hnsw.Node[string],
) (read, dropped int, err error) {
	result, err := s.QueryParams(`
		MATCH (e:Entity)-[:HAS_OBSERVATION]->(o:Observation)
		WHERE e.project_id = $project_id AND o.embedding IS NOT NULL
		RETURN `+s.entityColumns()+`, o.id, o.embedding
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return 0, 0, fmt.Errorf("query observations for index build: %w", err)
	}
	defer result.Close()

	for result.HasNext() {
		read++
		tuple, err := result.Next()
		if err != nil {
			return read, dropped, fmt.Errorf("index build next observation: %w", err)
		}
		row, err := tuple.GetAsSlice()
		if err != nil {
			tuple.Close()
			return read, dropped, fmt.Errorf("index build observation row: %w", err)
		}
		tuple.Close()

		// Two columns are appended after entityColumns, whose width varies with
		// what the database has — so index from the end, not by number.
		obsID, _ := row[len(row)-2].(string)
		if obsID == "" {
			continue
		}
		rawEmb, ok := row[len(row)-1].([]any)
		if !ok || len(rawEmb) == 0 {
			continue
		}
		emb, ok := embeddingFromRaw(rawEmb)
		if !ok {
			dropped++
			continue
		}

		entity := entityFromRow(row[:len(row)-2])
		key := obsNodeKey(obsID)
		entities[key] = entity
		*nodes = append(*nodes, hnsw.MakeNode(key, emb))
	}

	return read, dropped, nil
}

// embeddingFromRaw converts a Kuzu list cell to an embedding vector. go-kuzu
// returns float32 for FLOAT columns today, but a DOUBLE column — or a driver
// change — arrives as float64; both are accepted. Any other component type
// returns ok=false so the caller can drop the vector: zeroing the component,
// which is what a bare type assertion used to do, silently corrupts every
// distance the vector participates in and is invisible in search results.
func embeddingFromRaw(raw []any) ([]float32, bool) {
	emb := make([]float32, len(raw))
	for i, v := range raw {
		switch f := v.(type) {
		case float32:
			emb[i] = f
		case float64:
			emb[i] = float32(f)
		default:
			return nil, false
		}
	}
	return emb, true
}
