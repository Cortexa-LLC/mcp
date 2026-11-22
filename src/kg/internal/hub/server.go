package hub

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
)

// graphNameRE constrains graph names: they become filesystem path components.
var graphNameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// validPathComponent reports whether name is safe as a single filesystem path
// component. The regex alone admits "." and ".." — path navigation, not names —
// so they are rejected explicitly. Everything the hub joins into a path (graph
// names, commit hashes, layer names) must pass this.
func validPathComponent(name string) bool {
	return graphNameRE.MatchString(name) && name != "." && name != ".."
}

// Server hosts read-only knowledge graphs under dataDir and answers search
// queries over HTTP.
type Server struct {
	dataDir      string
	readVerifier Verifier // nil = open reads
	seedVerifier Verifier // nil = seeding disabled
	kgVersion    string
	mu           sync.Mutex // guards registry + seed operations
}

// NewServer creates a hub server rooted at dataDir with shared-token auth.
// An empty readToken leaves reads unauthenticated; an empty seedToken
// disables seeding entirely.
func NewServer(dataDir, readToken, seedToken, kgVersion string) *Server {
	var read, seed Verifier
	if readToken != "" {
		read = NewTokenVerifier(readToken)
	}
	if seedToken != "" {
		seed = NewTokenVerifier(seedToken)
	}
	return NewServerWithAuth(dataDir, read, seed, kgVersion)
}

// NewServerWithAuth creates a hub server whose read and seed surfaces are
// guarded by the given verifiers. A nil readVerifier leaves reads
// unauthenticated; a nil seedVerifier disables seeding entirely.
func NewServerWithAuth(dataDir string, readVerifier, seedVerifier Verifier, kgVersion string) *Server {
	s := &Server{
		dataDir:      dataDir,
		readVerifier: readVerifier,
		seedVerifier: seedVerifier,
		kgVersion:    kgVersion,
	}
	s.sweepStaging()
	s.reconcileInstalls()
	return s
}

// sweepStaging removes staging leftovers (.tmp-*, .old-*, current.tmp-*) from
// every graph directory. A crash mid-seed strands them, and prune deliberately
// skips those prefixes; at construction time no seed is in flight (the hub is
// single-node by design), so anything with a staging prefix is garbage.
//
// current.tmp-* is no longer produced — it was the half-built symlink of the
// old two-record layout — but the pattern is still swept so a data directory
// that crashed under an older binary comes up clean.
func (s *Server) sweepStaging() {
	graphs, err := os.ReadDir(filepath.Join(s.dataDir, "graphs"))
	if err != nil {
		return
	}
	for _, g := range graphs {
		if !g.IsDir() {
			continue
		}
		gdir := filepath.Join(s.dataDir, "graphs", g.Name())
		entries, err := os.ReadDir(gdir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".tmp-") || strings.HasPrefix(n, ".old-") || strings.HasPrefix(n, "current.tmp-") {
				_ = os.RemoveAll(filepath.Join(gdir, n))
			}
		}
	}
}

// reconcileInstalls reports registry entries the hub cannot serve.
//
// There is nothing left here to repair. This pass used to reconcile a `current`
// symlink against the registry, because install repointed the symlink and then
// wrote the registry, and a hard kill between the two left `current` on a
// commit the registry never recorded — a silent, permanent split. The database
// path now derives from the registered commit (see currentDBPath), so the
// registry cannot disagree with a second record that no longer exists, and a
// kill before the registry write simply leaves an unregistered commit
// directory that the next successful install prunes.
//
// What remains is a health check, run at startup because the alternative is
// discovering it one failed search at a time: a registry entry whose commit
// directory is absent serves nothing, and no later read corrects it.
//
// One residual this cannot see: a kill while re-pushing the commit that is
// ALREADY installed. install stashes the existing directory as .old-*, moves
// the new database in, and dies before the registry write; sweepStaging then
// deletes the stash, and the registered commit still names a directory that
// exists, so nothing here looks wrong. The graph serves the new database under
// the old GraphInfo. It needs a same-commit re-push (a dirty push) whose
// ProjectID also changed, and closing it means stamping the installed
// database's identity somewhere this pass can compare — not worth the
// machinery until the failure is observed.
func (s *Server) reconcileInstalls() {
	reg, err := loadRegistry(s.dataDir)
	if err != nil {
		log.Printf("reconcile installs: load registry: %v", err)
		return
	}
	for name, info := range reg.Graphs {
		// registry.json is hand-editable, so this runs before the pointer is
		// dereferenced: `{"graphs":{"g":null}}` unmarshals to a nil entry, and
		// this pass runs in the constructor — a panic here aborts `kg hub
		// serve` outright rather than being contained to one request.
		if info == nil {
			log.Printf("reconcile: registry entry %q is null — skipping (the graph will not be served)", name)
			continue
		}
		// Validate everything joined into a path, as everywhere else.
		if !validPathComponent(name) || !validPathComponent(info.Commit) {
			log.Printf("reconcile: registry entry %q records invalid name or commit %q — skipping (the graph will not be served)", name, info.Commit)
			continue
		}
		if _, serr := os.Stat(filepath.Join(s.graphDir(name), info.Commit)); serr != nil {
			log.Printf("reconcile %s: registry records commit %s but its directory is missing — every search against this graph will fail until the directory is restored or the graph is re-pushed", name, info.Commit)
		}
	}
}

// ReadAuthEnabled reports whether read requests require authentication.
func (s *Server) ReadAuthEnabled() bool { return s.readVerifier != nil }

// SeedingEnabled reports whether the hub accepts pushes.
func (s *Server) SeedingEnabled() bool { return s.seedVerifier != nil }

// Handler returns the hub's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("GET /v1/graphs", s.readAuth(s.handleListGraphs))
	mux.HandleFunc("GET /v1/graphs/{name}", s.readAuth(s.handleGetGraph))
	mux.HandleFunc("POST /v1/graphs/{name}/search", s.readAuth(s.handleGraphSearch))
	mux.HandleFunc("POST /v1/search", s.readAuth(s.handleFederatedSearch))
	mux.HandleFunc("PUT /v1/graphs/{name}", s.seedAuth(s.handleSeed))
	return mux
}

// --- auth ---

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if tok, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return tok
	}
	return ""
}

func tokenMatches(token, want string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// identityKey stores the verified *Identity in the request context so
// handlers can name the caller in audit lines.
type ctxKey int

const identityKey ctxKey = 0

// identityFrom returns the request's verified identity, or nil on an
// unauthenticated (open-read) request.
func identityFrom(r *http.Request) *Identity {
	id, _ := r.Context().Value(identityKey).(*Identity)
	return id
}

// verify runs v against r and writes the appropriate error response on
// failure: 403 for ErrForbidden (authenticated but not allowed), 401
// otherwise. On success it returns the request with the identity attached.
func verify(v Verifier, w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	id, err := v.Verify(r)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrForbidden) {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), identityKey, id)), true
}

func (s *Server) readAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.readVerifier == nil {
			next(w, r)
			return
		}
		r, ok := verify(s.readVerifier, w, r)
		if !ok {
			return
		}
		next(w, r)
	}
}

func (s *Server) seedAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.seedVerifier == nil {
			writeError(w, http.StatusForbidden, "seeding disabled: no seed auth configured on the hub (KG_HUB_SEED_TOKEN or KG_HUB_SEED_AUTH)")
			return
		}
		r, ok := verify(s.seedVerifier, w, r)
		if !ok {
			return
		}
		next(w, r)
	}
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func randomSuffix() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) graphDir(name string) string {
	return filepath.Join(s.dataDir, "graphs", name)
}

// currentDBPath resolves the database file for the commit a graph is
// registered at. The registry is the only record of which commit is live.
//
// commit is validated rather than trusted, because it is joined into a
// filesystem path and registry.json is both hand-editable and re-read on every
// request: a recorded commit of ".." would resolve outside the graph's
// directory. handleSeed validates it on the way in and reconcileInstalls
// reports it at startup, but neither covers an edit made while the server runs.
func (s *Server) currentDBPath(name, commit string) (string, error) {
	if !validPathComponent(commit) {
		return "", fmt.Errorf("graph %s records an invalid commit %q", name, commit)
	}
	return filepath.Join(s.graphDir(name), commit, canonicalDBName), nil
}

// --- read handlers ---

// loadRegistrySnapshot reads the registry, taking s.mu for the duration.
//
// Named "snapshot" rather than the Go-conventional "Locked" suffix, which
// signals that the caller already holds the lock — the opposite of what this
// does. Callers must NOT hold s.mu: it is a plain Mutex, so doing so deadlocks.
func (s *Server) loadRegistrySnapshot() (*Registry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return loadRegistry(s.dataDir)
}

// internalError logs the real error server-side and returns a generic message:
// error strings from the storage layer embed filesystem paths, which are not
// the client's business.
func internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	writeError(w, http.StatusInternalServerError, what)
}

func (s *Server) handleListGraphs(w http.ResponseWriter, r *http.Request) {
	reg, err := s.loadRegistrySnapshot()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"graphs": reg.Graphs})
}

func (s *Server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	reg, err := s.loadRegistrySnapshot()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}
	info, ok := reg.graph(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown graph %q", name))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type searchRequest struct {
	Query  string   `json:"query"`
	Limit  int      `json:"limit"`
	Graphs []string `json:"graphs"`

	// IncludeLayers expands a per-graph search to the graph's registered
	// layer graphs (transitively). Ignored by the fan-out endpoint.
	IncludeLayers bool `json:"include_layers"`
}

func decodeSearchRequest(r *http.Request) (*searchRequest, error) {
	var req searchRequest
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}
	if req.Query == "" {
		return nil, fmt.Errorf("missing required field: query")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}
	return &req, nil
}

// searchGraph opens a graph's current database read-only and runs a hybrid
// keyword search (no embedding: the hub does not generate query embeddings).
func (s *Server) searchGraph(name string, info *GraphInfo, query string, limit int) ([]*knowledge.SearchResult, error) {
	dbPath, err := s.currentDBPath(name, info.Commit)
	if err != nil {
		return nil, err
	}
	store, err := knowledge.OpenStoreReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open graph %s: %w", name, err)
	}
	defer store.Close()

	cfg := knowledge.DefaultSearchConfig()
	cfg.Limit = limit
	results, err := store.HybridSearch(info.ProjectID, query,
		knowledge.SearchEmbedding(store, info.ProjectID, query), cfg)
	if err != nil {
		return nil, fmt.Errorf("search graph %s: %w", name, err)
	}
	return results, nil
}

func (s *Server) handleGraphSearch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Defense in depth: registry membership already gates unknown names, but
	// this name is joined into a filesystem path — never do that unvalidated.
	if !validPathComponent(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid graph name %q", name))
		return
	}
	req, err := decodeSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg, err := s.loadRegistrySnapshot()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}
	info, ok := reg.graph(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown graph %q", name))
		return
	}
	results, err := s.searchGraph(name, info, req.Query, req.Limit)
	if err != nil {
		internalError(w, fmt.Sprintf("search graph %q", name), err)
		return
	}

	var layersSearched []string
	if req.IncludeLayers {
		// Deliberately simpler than kglib's federation: a server-side merge
		// just needs dedup (higher score wins per entity) + rank.
		// layersSearched records only layers that actually answered — a layer
		// whose search failed is logged and excluded, so the field never
		// over-reports coverage.
		for _, layer := range expandLayers(reg, name) {
			layerInfo, ok := reg.graph(layer)
			if !ok {
				log.Printf("layer search graph %q (layer of %q): registry entry missing or null — skipping", layer, name)
				continue
			}
			layerResults, err := s.searchGraph(layer, layerInfo, req.Query, req.Limit)
			if err != nil {
				log.Printf("layer search graph %q (layer of %q): %v", layer, name, err)
				continue
			}
			layersSearched = append(layersSearched, layer)
			results = mergeResults(results, layerResults)
		}
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		if len(results) > req.Limit {
			results = results[:req.Limit]
		}
	}

	resp := map[string]any{
		"graph":      name,
		"commit":     info.Commit,
		"project_id": info.ProjectID,
		"results":    results,
	}
	if len(layersSearched) > 0 {
		resp["layers_searched"] = layersSearched
	}
	writeJSON(w, http.StatusOK, resp)
}

// maxLayerExpansion bounds transitive layer expansion: at most this many
// graphs are visited, and chains deeper than this are cut off.
const maxLayerExpansion = 16

// expandLayers returns the transitive layer closure of graph (excluding the
// graph itself), in breadth-first order. It is cycle-safe via a visited set
// and bounded by maxLayerExpansion. Layer names missing from the registry or
// failing path validation are skipped with a log line — registry.json is
// hand-editable, so validate before anything joins these into paths.
func expandLayers(reg *Registry, graph string) []string {
	visited := map[string]bool{graph: true}
	queue := []string{graph}
	var out []string
	for depth := 0; len(queue) > 0 && depth < maxLayerExpansion; depth++ {
		var next []string
		for _, g := range queue {
			info, ok := reg.graph(g)
			if !ok {
				continue
			}
			for _, layer := range info.Layers {
				if visited[layer] {
					continue
				}
				visited[layer] = true
				if !validPathComponent(layer) {
					log.Printf("expand layers of %q: invalid layer name %q — skipping", graph, layer)
					continue
				}
				if _, ok := reg.graph(layer); !ok {
					log.Printf("expand layers of %q: layer graph %q not on this hub — skipping", graph, layer)
					continue
				}
				if len(out) >= maxLayerExpansion {
					log.Printf("expand layers of %q: layer set exceeds %d graphs — truncating", graph, maxLayerExpansion)
					return out
				}
				out = append(out, layer)
				next = append(next, layer)
			}
		}
		queue = next
	}
	return out
}

// mergeResults merges b into a: for entities present in both, the
// higher-score entry wins. Order is left to the caller's final sort.
func mergeResults(a, b []*knowledge.SearchResult) []*knowledge.SearchResult {
	index := make(map[string]int, len(a))
	for i, r := range a {
		if r.Entity != nil {
			index[r.Entity.ID] = i
		}
	}
	for _, r := range b {
		if r.Entity == nil {
			a = append(a, r)
			continue
		}
		if i, ok := index[r.Entity.ID]; ok {
			if r.Score > a[i].Score {
				a[i] = r
			}
			continue
		}
		index[r.Entity.ID] = len(a)
		a = append(a, r)
	}
	return a
}

func (s *Server) handleFederatedSearch(w http.ResponseWriter, r *http.Request) {
	req, err := decodeSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg, err := s.loadRegistrySnapshot()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}

	// Every name below is joined into a filesystem path by currentDBPath, so
	// every name below is validated first — the same rule handleGraphSearch
	// and expandLayers follow. Registry membership is not a substitute:
	// registry.json is hand-editable, so a key in it is not proof that the
	// name is a single, non-navigating path component.
	var names []string
	if len(req.Graphs) == 0 {
		for name := range reg.Graphs {
			if !validPathComponent(name) {
				log.Printf("federated search: registry holds invalid graph name %q — skipping", name)
				continue
			}
			names = append(names, name)
		}
	} else {
		for _, name := range req.Graphs {
			if !validPathComponent(name) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid graph name %q", name))
				return
			}
			if _, ok := reg.graph(name); !ok {
				writeError(w, http.StatusNotFound, fmt.Sprintf("unknown graph %q", name))
				return
			}
			names = append(names, name)
		}
	}

	out := make(map[string]any, len(names))
	for _, name := range names {
		info, ok := reg.graph(name)
		if !ok {
			// Checked above when the name list was validated, so this only
			// fires if the entry is null rather than absent — which would
			// otherwise panic on info.Commit below.
			out[name] = map[string]any{"error": "unknown graph"}
			continue
		}
		results, err := s.searchGraph(name, info, req.Query, req.Limit)
		if err != nil {
			log.Printf("federated search graph %q: %v", name, err)
			out[name] = map[string]any{"commit": info.Commit, "error": "search failed"}
			continue
		}
		out[name] = map[string]any{"commit": info.Commit, "results": results}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// --- seeding ---

// versionGateOK compares major.minor between the hub's kg version and the
// pusher's. "dev" or empty on either side skips the check.
func versionGateOK(serverVersion, clientVersion string) bool {
	if serverVersion == "" || clientVersion == "" || serverVersion == "dev" || clientVersion == "dev" {
		return true
	}
	return majorMinor(serverVersion) == majorMinor(clientVersion)
}

func majorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}

func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validPathComponent(name) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid graph name %q: must match %s and not be . or ..", name, graphNameRE.String()))
		return
	}
	commit := r.Header.Get("X-KG-Commit")
	if commit == "" {
		writeError(w, http.StatusBadRequest, "missing required header X-KG-Commit")
		return
	}
	// The commit becomes a path component (graphs/<name>/<commit>/), and the
	// registry entry every later read resolves that path from — never join it
	// unvalidated.
	if !validPathComponent(commit) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid X-KG-Commit %q: must match %s and not be . or ..", commit, graphNameRE.String()))
		return
	}
	projectID := r.Header.Get("X-KG-Project-ID")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "missing required header X-KG-Project-ID")
		return
	}
	clientVersion := r.Header.Get("X-KG-Version")
	if !versionGateOK(s.kgVersion, clientVersion) {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"kg version mismatch: hub is %s, push is %s (major.minor must match)", s.kgVersion, clientVersion))
		return
	}

	info := &GraphInfo{
		Repo:      r.Header.Get("X-KG-Repo"),
		Commit:    commit,
		ProjectID: projectID,
		KGVersion: clientVersion,
		PushedAt:  time.Now().UTC(),
	}
	switch r.Header.Get("X-KG-Dirty") {
	case "1", "true":
		info.Dirty = true
	}
	if at := r.Header.Get("X-KG-Indexed-At"); at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			info.IndexedAt = t
		}
	}
	if info.IndexedAt.IsZero() {
		info.IndexedAt = info.PushedAt
	}
	if layers := r.Header.Get("X-KG-Layers"); layers != "" {
		for _, l := range strings.Split(layers, ",") {
			if l = strings.TrimSpace(l); l != "" {
				// Layer names are graph references and may be joined into
				// paths by consumers of the registry — hold them to the same
				// standard as graph names.
				if !validPathComponent(l) {
					writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid layer name %q in X-KG-Layers", l))
					return
				}
				info.Layers = append(info.Layers, l)
			}
		}
	}

	// A graph belongs to the repo that first seeded it. Refuse a push from a
	// different one rather than silently replacing someone else's graph: the
	// hub namespace is shared, names can be overridden with --graph, and the
	// failure mode without this is a team quietly losing their knowledge to a
	// name collision. X-KG-Force is the deliberate override, for the cases
	// where a repo really has been renamed or moved.
	if err := s.checkGraphOwnership(name, info.Repo, r.Header.Get("X-KG-Force")); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// Stage (unpack + validate) WITHOUT holding s.mu: the body read is paced by
	// the network, and holding the lock across it would let one slow push stall
	// every read handler. Only the install step below needs the lock.
	body := http.MaxBytesReader(w, r.Body, maxExtractBytes)
	tmpDir, err := s.stage(name, body)
	if err != nil {
		log.Printf("seed %s@%s: staging failed: %v", name, commit, err)
		writeError(w, http.StatusBadRequest, "pushed archive failed staging (invalid archive or database)")
		return
	}

	s.mu.Lock()
	err = s.install(name, commit, info, tmpDir)
	s.mu.Unlock()
	if err != nil {
		os.RemoveAll(tmpDir)
		log.Printf("seed %s@%s: install failed: %v", name, commit, err)
		writeError(w, http.StatusInternalServerError, "seeding failed")
		return
	}
	log.Printf("seed %s@%s accepted from %s", name, commit, identityFrom(r))
	writeJSON(w, http.StatusOK, map[string]string{
		"graph":   name,
		"commit":  commit,
		"message": "seeded",
	})
}

// checkGraphOwnership reports whether repo may seed the named graph.
//
// Permissive in the cases where ownership is genuinely unknown: a graph the
// registry has never seen, a registry entry recorded before repo was tracked,
// or a push that declines to say where it came from. Those are all "no evidence
// of a conflict", and refusing them would break existing deployments to guard
// against nothing.
func (s *Server) checkGraphOwnership(name, repo, force string) error {
	switch force {
	case "1", "true":
		return nil
	}

	reg, err := s.loadRegistrySnapshot()
	if err != nil {
		// A registry that cannot be read is a hub problem, not a push problem;
		// let the seed proceed and fail later if it is going to.
		return nil
	}
	existing, ok := reg.graph(name)
	if !ok || existing.Repo == "" || repo == "" || existing.Repo == repo {
		return nil
	}
	return fmt.Errorf("graph %q was seeded from %s, not %s — "+
		"pick another name with 'kg push --graph <name>', or resend with X-KG-Force to replace it",
		name, existing.Repo, repo)
}

// stage unpacks a pushed archive into a fresh temp dir inside the graph
// directory (so the later rename is atomic, same filesystem) and validates
// that the extracted database opens. It does NOT take s.mu — staging is paced
// by the network body and must not block readers. On error the temp dir is
// removed; on success the caller owns it.
func (s *Server) stage(name string, body io.Reader) (tmpDir string, err error) {
	gdir := s.graphDir(name)
	if err := os.MkdirAll(gdir, 0755); err != nil {
		return "", fmt.Errorf("create graph directory: %w", err)
	}

	tmpDir = filepath.Join(gdir, ".tmp-"+randomSuffix())
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := UnpackDB(body, tmpDir); err != nil {
		return "", fmt.Errorf("unpack archive: %w", err)
	}

	// Validate the extracted database opens before installing it.
	store, err := knowledge.OpenStoreReadOnly(filepath.Join(tmpDir, canonicalDBName))
	if err != nil {
		return "", fmt.Errorf("pushed database failed validation: %w", err)
	}
	if err := store.Close(); err != nil {
		return "", fmt.Errorf("close validated database: %w", err)
	}
	return tmpDir, nil
}

// install atomically installs a staged database as graphs/<name>/<commit>,
// records it in the registry, and only then prunes stale commit directories.
// Caller holds s.mu and owns tmpDir until install returns.
//
// Invariant: install either fully succeeds or leaves the graph exactly as it
// found it. Writing the registry is the commit point and the last step that
// can fail; the only step before it — moving the staged directory into place —
// is rolled back on error, and pruning, which cannot be undone, runs only
// after the commit point has passed.
//
// The registry is the single record of which commit is live. There used to be
// a second one: a `current` symlink that readers resolved through the OS,
// repointed here just before the registry write. Two records meant they could
// disagree — a search read the database from `current` but the ProjectID it
// queried with from the registry, so a mismatch answered HTTP 200 with zero
// results whenever the project ID had changed, and no later read corrected it.
// Closing that window took a second rollback path here and a repair pass at
// startup. Resolving the database path from the registered commit instead
// (see currentDBPath) removes the split by construction.
//
// It also removes a hard Windows limitation. Creating a symlink there needs
// SeCreateSymbolicLinkPrivilege, which a normal user does not hold, so every
// push failed with "A required privilege is not held by the client". A
// directory junction needs no privilege but cannot be swapped atomically —
// Windows refuses to rename any directory over an existing one — so it could
// not preserve the property the pointer existed for. Renaming a file over an
// existing file, which is how saveRegistry commits, works on every platform.
func (s *Server) install(name, commit string, info *GraphInfo, tmpDir string) error {
	gdir := s.graphDir(name)

	// If this commit was pushed before, move the existing dir aside so the
	// rename succeeds; restore it if anything below fails.
	commitDir := filepath.Join(gdir, commit)
	oldDir := ""
	if _, serr := os.Lstat(commitDir); serr == nil {
		oldDir = filepath.Join(gdir, ".old-"+randomSuffix())
		if err := os.Rename(commitDir, oldDir); err != nil {
			return fmt.Errorf("stash previous commit directory: %w", err)
		}
	}

	// restoreCommitDir undoes the staging rename. When nothing was stashed,
	// "as it was" means commitDir gone: leaving the staged data behind would
	// strand a commit directory the registry never mentions, and handleSeed's
	// own RemoveAll(tmpDir) cannot clean it up once tmpDir has been renamed.
	restoreCommitDir := func() {
		_ = os.RemoveAll(commitDir)
		if oldDir != "" {
			_ = os.Rename(oldDir, commitDir)
		}
	}

	if err := os.Rename(tmpDir, commitDir); err != nil {
		restoreCommitDir()
		return fmt.Errorf("install commit directory: %w", err)
	}

	// The registry write is the commit point, and the last thing that can
	// fail. It happens while both the old and the new commit directories still
	// exist, so a failure here can be walked all the way back.
	reg, err := loadRegistry(s.dataDir)
	if err != nil {
		restoreCommitDir()
		return err
	}

	// The commit this graph was registered at until now. Prune keeps its
	// directory for one more generation, so a reader that resolved it moments
	// before this write can still open the database it named.
	prevCommit := ""
	if prev, ok := reg.graph(name); ok {
		prevCommit = prev.Commit
	}

	reg.Graphs[name] = info
	if err := saveRegistry(s.dataDir, reg); err != nil {
		restoreCommitDir()
		return err
	}

	// Committed. Prune is irreversible, so it runs only now: keep the newly
	// registered commit and the one before it; leave in-flight .tmp-*/.old-*
	// dirs alone, except our own .old dir.
	//
	// A `current` symlink or a `current.tmp-*` left by a hub that predates the
	// registry-only layout is no longer special-cased, so it is swept here.
	// Removing a symlink unlinks the pointer, never the commit directory it
	// named, and that directory is retained or pruned on its own merits above.
	entries, rerr := os.ReadDir(gdir)
	if rerr == nil {
		for _, e := range entries {
			n := e.Name()
			if n == commit || (prevCommit != "" && n == prevCommit) {
				continue
			}
			if strings.HasPrefix(n, ".tmp-") || strings.HasPrefix(n, ".old-") {
				continue
			}
			_ = os.RemoveAll(filepath.Join(gdir, n))
		}
	}
	if oldDir != "" {
		_ = os.RemoveAll(oldDir)
	}
	return nil
}
