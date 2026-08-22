package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cortexa-llc/mcp/kglib"
)

// PushRequest describes one graph push to a hub.
type PushRequest struct {
	HubURL    string // scheme optional; "http://" is prepended if missing
	Graph     string
	DBPath    string
	SeedToken string
	Meta      kglib.KGMeta // Commit required; caller fills from stamp or fallback
	Layers    []string
}

// normalizeHubURL prepends "http://" when no scheme is present and trims a
// trailing slash.
func normalizeHubURL(hubURL string) string {
	if !strings.Contains(hubURL, "://") {
		hubURL = "http://" + hubURL
	}
	return strings.TrimSuffix(hubURL, "/")
}

// Push packs the database at req.DBPath and PUTs it to the hub, carrying the
// provenance metadata as headers.
func Push(req PushRequest) error {
	if req.Meta.Commit == "" {
		return fmt.Errorf("push %s: commit is required", req.Graph)
	}

	// Pack to a temp file first so the PUT has a known Content-Length and no
	// database file handles are held during the upload.
	tmp, err := os.CreateTemp("", "kg-push-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create archive temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := PackDB(tmp, req.DBPath); err != nil {
		tmp.Close()
		return fmt.Errorf("pack %s: %w", req.DBPath, err)
	}
	size, err := tmp.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		tmp.Close()
		return fmt.Errorf("rewind archive: %w", err)
	}

	url := fmt.Sprintf("%s/v1/graphs/%s", normalizeHubURL(req.HubURL), req.Graph)
	httpReq, err := http.NewRequest(http.MethodPut, url, tmp)
	if err != nil {
		tmp.Close()
		return err
	}
	httpReq.ContentLength = size
	httpReq.Header.Set("Content-Type", "application/x-kg-graph+tar+gzip")
	httpReq.Header.Set("Authorization", "Bearer "+req.SeedToken)
	httpReq.Header.Set("X-KG-Commit", req.Meta.Commit)
	httpReq.Header.Set("X-KG-Project-ID", req.Meta.ProjectID)
	if req.Meta.RepoURL != "" {
		httpReq.Header.Set("X-KG-Repo", req.Meta.RepoURL)
	}
	if req.Meta.Dirty {
		httpReq.Header.Set("X-KG-Dirty", "1")
	}
	if !req.Meta.IndexedAt.IsZero() {
		httpReq.Header.Set("X-KG-Indexed-At", req.Meta.IndexedAt.Format(time.RFC3339))
	}
	if req.Meta.KGVersion != "" {
		httpReq.Header.Set("X-KG-Version", req.Meta.KGVersion)
	}
	if len(req.Layers) > 0 {
		httpReq.Header.Set("X-KG-Layers", strings.Join(req.Layers, ","))
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("push %s: %w", req.Graph, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("push %s: hub returned %s: %s", req.Graph, resp.Status, readBodySnippet(resp.Body))
	}
	return nil
}

// ListGraphs fetches the hub's registry. readToken may be empty for open hubs.
func ListGraphs(hubURL, readToken string) (*Registry, error) {
	url := normalizeHubURL(hubURL) + "/v1/graphs"
	httpReq, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if readToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+readToken)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list graphs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list graphs: hub returned %s: %s", resp.Status, readBodySnippet(resp.Body))
	}

	var reg Registry
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("list graphs: parse response: %w", err)
	}
	if reg.Graphs == nil {
		reg.Graphs = map[string]*GraphInfo{}
	}
	return &reg, nil
}

// readBodySnippet returns up to 512 bytes of a response body for error messages.
func readBodySnippet(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 512))
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "(empty body)"
	}
	return strconv.Quote(s)
}
