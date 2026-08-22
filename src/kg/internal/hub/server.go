package hub

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	dataDir   string
	readToken string // "" = open reads
	seedToken string // "" = seeding disabled
	kgVersion string
	mu        sync.Mutex // guards registry + seed operations
}

// NewServer creates a hub server rooted at dataDir. An empty readToken leaves
// reads unauthenticated; an empty seedToken disables seeding entirely.
func NewServer(dataDir, readToken, seedToken, kgVersion string) *Server {
	s := &Server{
		dataDir:   dataDir,
		readToken: readToken,
		seedToken: seedToken,
		kgVersion: kgVersion,
	}
	s.sweepStaging()
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

// ReadAuthEnabled reports whether read requests require a bearer token.
func (s *Server) ReadAuthEnabled() bool { return s.readToken != "" }

// SeedingEnabled reports whether the hub accepts pushes.
func (s *Server) SeedingEnabled() bool { return s.seedToken != "" }

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

func (s *Server) readAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.readToken != "" && !tokenMatches(bearerToken(r), s.readToken) {
			writeError(w, http.StatusUnauthorized, "missing or invalid read token")
			return
		}
		next(w, r)
	}
}

func (s *Server) seedAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.seedToken == "" {
			writeError(w, http.StatusForbidden, "seeding disabled: KG_HUB_SEED_TOKEN not set on the hub")
			return
		}
		if !tokenMatches(bearerToken(r), s.seedToken) {
			writeError(w, http.StatusUnauthorized, "missing or invalid seed token")
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

func (s *Server) loadRegistryLocked() (*Registry, error) {
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
	reg, err := s.loadRegistryLocked()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"graphs": reg.Graphs})
}

func (s *Server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	reg, err := s.loadRegistryLocked()
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
	reg, err := s.loadRegistryLocked()
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
	writeJSON(w, http.StatusOK, map[string]any{
		"graph":      name,
		"commit":     info.Commit,
		"project_id": info.ProjectID,
		"results":    results,
	})
}

func (s *Server) handleFederatedSearch(w http.ResponseWriter, r *http.Request) {
	req, err := decodeSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg, err := s.loadRegistryLocked()
	if err != nil {
		internalError(w, "load registry", err)
		return
	}

	names := req.Graphs
	if len(names) == 0 {
		for name := range reg.Graphs {
			names = append(names, name)
		}
	} else {
		for _, name := range names {
			if _, ok := reg.Graphs[name]; !ok {
				writeError(w, http.StatusNotFound, fmt.Sprintf("unknown graph %q", name))
				return
			}
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
	writeJSON(w, http.StatusOK, map[string]string{
		"graph":   name,
		"commit":  commit,
		"message": "seeded",
	})
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
// repoints `current`, prunes stale commit directories, and updates the
// registry. Caller holds s.mu and owns tmpDir until install returns.
func (s *Server) install(name, commit string, info *GraphInfo, tmpDir string) (err error) {
	gdir := s.graphDir(name)

	// If this commit was pushed before, move the existing dir aside so the
	// rename succeeds; restore it if the swap fails half-way.
	commitDir := filepath.Join(gdir, commit)
	oldDir := ""
	if _, serr := os.Lstat(commitDir); serr == nil {
		oldDir = filepath.Join(gdir, ".old-"+randomSuffix())
		if err := os.Rename(commitDir, oldDir); err != nil {
			return fmt.Errorf("stash previous commit directory: %w", err)
		}
	}
	restoreOld := func() {
		if oldDir != "" {
			_ = os.RemoveAll(commitDir)
			_ = os.Rename(oldDir, commitDir)
		}
	}

	if err := os.Rename(tmpDir, commitDir); err != nil {
		restoreOld()
		return fmt.Errorf("install commit directory: %w", err)
	}

	// Remember the previous current target before repointing, for pruning.
	prevTarget, _ := os.Readlink(filepath.Join(gdir, "current"))

	// Repoint "current" atomically: create a temp symlink, rename over.
	linkTmp := filepath.Join(gdir, "current.tmp-"+randomSuffix())
	if err := os.Symlink(commit, linkTmp); err != nil {
		restoreOld()
		return fmt.Errorf("create current symlink: %w", err)
	}
	if err := os.Rename(linkTmp, filepath.Join(gdir, "current")); err != nil {
		os.Remove(linkTmp)
		restoreOld()
		return fmt.Errorf("update current symlink: %w", err)
	}

	// Prune: keep the current target and the immediately previous target;
	// leave in-flight .tmp-*/.old-* dirs alone, except our own .old dir.
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

	// Update the registry.
	reg, err := loadRegistry(s.dataDir)
	if err != nil {
		return err
	}
	reg.Graphs[name] = info
	return saveRegistry(s.dataDir, reg)
}
