// Package remote implements a federation layer backed by a kg hub: it
// satisfies kglib.SearchLayer by forwarding hybrid searches to a hub graph
// over HTTP (docs/kg-shared-service-design.md, Phase 3).
package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cortexa-llc/mcp/kglib"
)

// Layer forwards hybrid searches to a single graph on a kg hub.
type Layer struct {
	hubURL    string // normalized: scheme prepended, no trailing slash
	graph     string
	readToken string
	client    *http.Client
}

// NewLayer creates a remote search layer for the named graph on the hub at
// hubURL. readToken may be empty when the hub does not require read auth.
func NewLayer(hubURL, graph, readToken string) *Layer {
	// Normalize like hub.normalizeHubURL (duplicated deliberately: a shared
	// package is not worth the coupling).
	if !strings.Contains(hubURL, "://") {
		hubURL = "http://" + hubURL
	}
	hubURL = strings.TrimRight(hubURL, "/")
	// The graph name is interpolated into the request path. Scope configs are
	// local and trusted, and the hub re-validates — but a name like "../x"
	// would silently address a different endpoint, so sanitize to something
	// that can only ever be one path segment.
	graph = url.PathEscape(graph)
	return &Layer{
		hubURL:    hubURL,
		graph:     graph,
		readToken: readToken,
		// A down hub must not hang searches: connection-refused fails fast,
		// the timeout bounds black-holes.
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

type searchRequest struct {
	Query         string `json:"query"`
	Limit         int    `json:"limit,omitempty"`
	IncludeLayers bool   `json:"include_layers"`
}

type searchResponse struct {
	Results []*kglib.SearchResult `json:"results"`
}

// HybridSearch implements kglib.SearchLayer by POSTing the query to the hub's
// per-graph search endpoint. projectID and queryEmbedding are ignored: the hub
// resolves the graph's own project ID and does not accept query embeddings.
func (l *Layer) HybridSearch(projectID, query string, queryEmbedding []float32, config kglib.SearchConfig) ([]*kglib.SearchResult, error) {
	_ = projectID
	_ = queryEmbedding

	body, err := json.Marshal(searchRequest{
		Query:         query,
		Limit:         config.Limit,
		IncludeLayers: true,
	})
	if err != nil {
		return nil, fmt.Errorf("hub search %s@%s: encode request: %w", l.graph, l.hubURL, err)
	}

	endpoint := fmt.Sprintf("%s/v1/graphs/%s/search", l.hubURL, l.graph)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hub search %s@%s: %w", l.graph, l.hubURL, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if l.readToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.readToken)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub search %s@%s: %w", l.graph, l.hubURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hub search %s@%s: %s: %s", l.graph, l.hubURL, resp.Status, strings.TrimSpace(string(msg)))
	}

	var decoded searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("hub search %s@%s: decode response: %w", l.graph, l.hubURL, err)
	}

	// Drop results without an entity: the federation merge dereferences
	// result.Entity unconditionally, so a buggy (or hostile) hub responding
	// `{"results":[{"score":1}]}` must degrade here, not panic every consumer.
	results := decoded.Results[:0]
	for _, r := range decoded.Results {
		if r != nil && r.Entity != nil {
			results = append(results, r)
		}
	}
	return results, nil
}

// Close implements kglib.SearchLayer. Remote layers hold no resources beyond
// pooled HTTP connections, so this is a no-op.
func (l *Layer) Close() error { return nil }

var _ kglib.SearchLayer = (*Layer)(nil)
