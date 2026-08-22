package kglib

import (
	"fmt"
	"strings"
	"time"
)

// KGMeta records the provenance of an indexed knowledge graph database: which
// repository and commit it was built from, when, and with which embedding
// model configured. One stamp per project ID, overwritten on each index run.
type KGMeta struct {
	ProjectID  string
	RepoURL    string
	Commit     string
	Dirty      bool // working tree had uncommitted changes at index time
	IndexedAt  time.Time
	EmbedModel string
	KGVersion  string
}

// SetMeta upserts the provenance stamp for a project.
func (s *Store) SetMeta(meta KGMeta) error {
	result, err := s.QueryParams(`
		MATCH (m:KGMeta {project_id: $project_id}) DELETE m
	`, map[string]any{"project_id": meta.ProjectID})
	if err != nil {
		return fmt.Errorf("delete existing meta: %w", err)
	}
	result.Close()

	result, err = s.QueryParams(`
		CREATE (m:KGMeta {
			project_id: $project_id, repo_url: $repo_url,
			commit_hash: $commit_hash, dirty: $dirty,
			indexed_at: $indexed_at, embed_model: $embed_model,
			kg_version: $kg_version
		})
	`, map[string]any{
		"project_id":  meta.ProjectID,
		"repo_url":    meta.RepoURL,
		"commit_hash": meta.Commit,
		"dirty":       meta.Dirty,
		"indexed_at":  meta.IndexedAt,
		"embed_model": meta.EmbedModel,
		"kg_version":  meta.KGVersion,
	})
	if err != nil {
		return fmt.Errorf("create meta: %w", err)
	}
	result.Close()

	return nil
}

// GetMeta returns the provenance stamp for a project, or (nil, nil) if the
// database has no stamp — including databases created before the KGMeta table
// existed (read-only opens never migrate schema, so the table itself may be
// missing; that case is also (nil, nil), not an error).
func (s *Store) GetMeta(projectID string) (*KGMeta, error) {
	result, err := s.QueryParams(`
		MATCH (m:KGMeta {project_id: $project_id})
		RETURN m.repo_url, m.commit_hash, m.dirty, m.indexed_at, m.embed_model, m.kg_version
	`, map[string]any{"project_id": projectID})
	if err != nil {
		// Kuzu reports a missing table as `Binder exception: Table KGMeta does
		// not exist.` — the normal case for databases created before this table
		// was added, since read-only opens never run schema DDL. Require the
		// table name in the match so other "does not exist" errors still surface.
		if strings.Contains(err.Error(), "KGMeta") && strings.Contains(err.Error(), "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("query meta: %w", err)
	}
	defer result.Close()

	if !result.HasNext() {
		return nil, nil
	}

	tuple, err := result.Next()
	if err != nil {
		return nil, fmt.Errorf("get next: %w", err)
	}
	defer tuple.Close()

	row, err := tuple.GetAsSlice()
	if err != nil {
		return nil, fmt.Errorf("get row: %w", err)
	}

	meta := &KGMeta{
		ProjectID:  projectID,
		RepoURL:    stringOrEmpty(row[0]),
		Commit:     stringOrEmpty(row[1]),
		IndexedAt:  timeOrZero(row[3]),
		EmbedModel: stringOrEmpty(row[4]),
		KGVersion:  stringOrEmpty(row[5]),
	}
	if dirty, ok := row[2].(bool); ok {
		meta.Dirty = dirty
	}

	return meta, nil
}
