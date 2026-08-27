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

// reconcileInstalls restores the invariant that `current` and the registry
// name the same commit for every graph.
//
// install's rollback covers error returns, but a hard kill between its two
// renames — repointing `current`, then writing the registry — leaves
// `current` on a commit the registry never recorded. That split is silent
// and permanent: a search reads the database from `current` but queries it
// with the registry's ProjectID, so the graph answers HTTP 200 with zero
// results whenever the project ID changed, and no later read corrects it.
//
// The registry write is install's commit point, so the registry is the
// authority: an unregistered `current` target means the seed never
// committed, and the symlink is rolled back to the registered commit. The
// orphaned commit directory is deliberately left in place — the next
// successful install's prune removes it, and deleting data is not a job for
// a constructor-time repair pass.
//
// One residual this cannot see: a kill while re-pushing the commit that is
// ALREADY installed. install stashes the existing directory as .old-*, moves
// the new database in, and dies before the registry write; sweepStaging then
// deletes the stash, and `current` still names the registered commit, so
// nothing here looks wrong. The graph serves the new database under the old
// GraphInfo. It needs a same-commit re-push (a dirty push) whose ProjectID
// also changed, and closing it means stamping the installed database's
// identity somewhere this pass can compare — not worth the machinery until
// the failure is observed.
func (s *Server) reconcileInstalls() {
	reg, err := loadRegistry(s.dataDir)
	if err != nil {
		log.Printf("reconcile installs: load registry: %v", err)
		return
	}
	for name, info := range reg.Graphs {
		// registry.json is hand-editable: validate everything joined into a
		// path, as everywhere else.
		if !validPathComponent(name) || !validPathComponent(info.Commit) {
			log.Printf("reconcile: registry entry %q records invalid name or commit %q — skipping (the graph will not be served)", name, info.Commit)
			continue
		}
		gdir := s.graphDir(name)
		currentLink := filepath.Join(gdir, "current")

		target, err := os.Readlink(currentLink)
		switch {
		case err == nil && target == info.Commit:
			continue
		case err != nil && !os.IsNotExist(err):
			// Something is there but unreadable as a symlink; repairing it
			// blind could destroy data this pass does not understand.
			log.Printf("reconcile %s: read current: %v — leaving as is", name, err)
			continue
		}
		// From here: current is missing (a partial copy, a restored backup, an
		// operator) or points somewhere the registry does not record. Both are
		// repaired from the registry, which is install's commit point.

		if _, serr := os.Stat(filepath.Join(gdir, info.Commit)); serr != nil {
			log.Printf("reconcile %s: registry records %s but its directory is missing — leaving as is", name, info.Commit)
			continue
		}
		if rerr := replaceSymlink(gdir, currentLink, info.Commit); rerr != nil {
			log.Printf("reconcile %s: point current at %s: %v", name, info.Commit, rerr)
			continue
		}
		if os.IsNotExist(err) {
			log.Printf("reconcile %s: current was missing; pointed it at the registered commit %s", name, info.Commit)
		} else {
			log.Printf("reconcile %s: current pointed at unregistered commit %s (interrupted seed); rolled back to registered %s", name, target, info.Commit)
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

// currentDBPath resolves the current database for a graph.
func (s *Server) currentDBPath(name string) string {
	return filepath.Join(s.graphDir(name), "current", canonicalDBName)
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
	info, ok := reg.Graphs[name]
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
	store, err := knowledge.OpenStoreReadOnly(s.currentDBPath(name))
	if err != nil {
		return nil, fmt.Errorf("open graph %s: %w", name, err)
	}
	defer store.Close()

	cfg := knowledge.DefaultSearchConfig()
	cfg.Limit = limit
	results, err := store.HybridSearch(info.ProjectID, query, nil, cfg)
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
	info, ok := reg.Graphs[name]
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
			layerResults, err := s.searchGraph(layer, reg.Graphs[layer], req.Query, req.Limit)
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
			info, ok := reg.Graphs[g]
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
				if _, ok := reg.Graphs[layer]; !ok {
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
			if _, ok := reg.Graphs[name]; !ok {
				writeError(w, http.StatusNotFound, fmt.Sprintf("unknown graph %q", name))
				return
			}
			names = append(names, name)
		}
	}

	out := make(map[string]any, len(names))
	for _, name := range names {
		info := reg.Graphs[name]
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
	// The commit becomes a path component (graphs/<name>/<commit>/) and the
	// `current` symlink target — never join it unvalidated.
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
	existing, ok := reg.Graphs[name]
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

// replaceSymlink points link at target atomically: create a temp symlink in
// dir, then rename it over. dir must be the directory holding link so the
// rename stays within one filesystem.
func replaceSymlink(dir, link, target string) error {
	tmp := filepath.Join(dir, "current.tmp-"+randomSuffix())
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename symlink: %w", err)
	}
	return nil
}

// install atomically installs a staged database as graphs/<name>/<commit>,
// repoints `current`, updates the registry, and only then prunes stale commit
// directories. Caller holds s.mu and owns tmpDir until install returns.
//
// Invariant: install either fully succeeds or leaves the graph exactly as it
// found it. Every step that can fail — moving the staged directory into place,
// repointing `current`, writing the registry — happens before the one step
// that cannot be undone (pruning old commit directories), and each is rolled
// back if a later one fails.
//
// That invariant is what keeps `current` and the registry entry from
// disagreeing about which commit is installed. A search reads the database
// from `current` but the ProjectID it queries with from the registry, so a
// mismatch is an HTTP 200 with zero results whenever the project ID changed,
// plus a stale advertised commit that no later read can correct. The registry
// used to be written last, after `current` had already been repointed and the
// stashed directory deleted, and a registry write error returned without
// undoing either — leaving exactly that split.
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

	// Remember the previous current target before repointing, for pruning and
	// for rollback.
	currentLink := filepath.Join(gdir, "current")
	prevTarget, _ := os.Readlink(currentLink)

	if err := replaceSymlink(gdir, currentLink, commit); err != nil {
		restoreCommitDir()
		return fmt.Errorf("update current symlink: %w", err)
	}
	restoreCurrent := func() {
		if prevTarget == "" {
			_ = os.Remove(currentLink)
			return
		}
		_ = replaceSymlink(gdir, currentLink, prevTarget)
	}

	// The registry is the last thing that can fail, and it is written while
	// both the old and the new commit directories still exist, so a failure
	// here can be walked all the way back.
	reg, err := loadRegistry(s.dataDir)
	if err != nil {
		restoreCurrent()
		restoreCommitDir()
		return err
	}
	reg.Graphs[name] = info
	if err := saveRegistry(s.dataDir, reg); err != nil {
		restoreCurrent()
		restoreCommitDir()
		return err
	}

	// Committed. Prune is irreversible, so it runs only now: keep the current
	// target and the immediately previous target; leave in-flight
	// .tmp-*/.old-* dirs alone, except our own .old dir.
	entries, rerr := os.ReadDir(gdir)
	if rerr == nil {
		for _, e := range entries {
			n := e.Name()
			if n == "current" || n == commit || (prevTarget != "" && n == prevTarget) {
				continue
			}
			if strings.HasPrefix(n, ".tmp-") || strings.HasPrefix(n, ".old-") || strings.HasPrefix(n, "current.tmp-") {
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
