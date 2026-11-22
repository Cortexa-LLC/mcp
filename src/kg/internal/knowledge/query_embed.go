package knowledge

// Query-side embedding for semantic search.
//
// Vectors were written into the graph long before anything read them: every
// caller of HybridSearch passed a nil query embedding, which skips VectorSearch
// entirely (see kglib.Store.HybridSearch), so every search in every surface —
// the CLI, the MCP tools, the hub — was keyword substring matching. `kg embed`
// filled a column nothing consulted.
//
// This file is the missing half. SearchEmbedding turns a query string into a
// vector when, and only when, doing so can help:
//
//   - the graph has at least one vector (HasEmbeddings), and
//   - an embedding provider is configured and answers.
//
// Anything else returns nil, which is not an error: nil means keyword-only
// search, the behaviour every caller had before, and degrading to it silently
// is correct. A missing OPENAI_API_KEY is the default state of most checkouts,
// not a fault to report on every query.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cortexa-llc/mcp/kglib"
)

// queryEmbedTimeout bounds the provider call. A search is interactive and an
// agent's preflight blocks on it; a hung Ollama host or a stalled HTTPS
// connection must cost a bounded delay and then fall back to keyword, not hang
// the session.
const queryEmbedTimeout = 10 * time.Second

// embedModelSource is the part of a Store that model resolution needs. Taking
// the interface rather than *Store lets the federated path pass its primary
// store, and lets tests substitute a stub without a Kuzu database.
type embedModelSource interface {
	GetMeta(projectID string) (*kglib.KGMeta, error)
	HasEmbeddings(projectID string) (bool, error)
}

// SearchEmbedding returns a query vector for semantic search, or nil to mean
// "keyword only". It never returns an error: every failure mode here is a
// degradation to the behaviour the caller had before, and a search that still
// works is a better answer than one that fails.
//
// store may be nil (a surface with no handle on the primary database), in which
// case the embedding-presence probe is skipped and the model comes from the
// environment alone.
func SearchEmbedding(store embedModelSource, projectID, query string) []float32 {
	if query == "" {
		return nil
	}

	if store != nil {
		has, err := store.HasEmbeddings(projectID)
		if err != nil || !has {
			// No vectors in the graph: VectorSearch over an empty index can
			// only return nothing, so the provider call would be pure cost.
			// A failed probe is treated the same way — a search that still
			// works beats one that fails over a diagnostic query.
			return nil
		}
	}

	embedder, err := newEmbedderForModel(resolveEmbedModel(store, projectID))
	if err != nil {
		// No provider configured (the usual case: no OPENAI_API_KEY).
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryEmbedTimeout)
	defer cancel()

	vectors, err := embedder.Embed(ctx, []string{query})
	if err != nil || len(vectors) == 0 {
		return nil
	}
	return vectors[0]
}

// resolveEmbedModel returns the model a query vector must be built with: the
// one recorded in the graph's meta when there is one, and the environment's
// otherwise.
//
// The stored model wins deliberately. Cosine distance between vectors from two
// different models is meaningless, and their widths need not even match, so a
// stale KNOWLEDGE_EMBED_MODEL in the shell would quietly turn semantic search
// into noise — ranked and presented exactly like signal. Resolution is its own
// function because that precedence is the part worth asserting directly: from
// the outside, both choices fail the same way when neither provider answers.
func resolveEmbedModel(store embedModelSource, projectID string) string {
	env := os.Getenv("KNOWLEDGE_EMBED_MODEL")
	if store == nil {
		return env
	}
	if meta, err := store.GetMeta(projectID); err == nil && meta != nil && meta.EmbedModel != "" {
		return meta.EmbedModel
	}
	return env
}

// newEmbedderForModel builds an embedder for an explicit model name, falling
// back to kglib's default when it is empty. Kept apart from
// NewEmbedderFromEnv, which reads the model from the environment, because the
// model a database was built with is not necessarily the one the environment
// names — and for a query vector only the former is usable.
//
// A var, not a func, so tests can substitute a provider. Every property worth
// asserting here — that an unembedded graph is never embedded against, that
// the stored model is the one used, that a provider failure degrades to
// keyword — is otherwise observable only as "returned nil", which is also what
// a failed network call returns. With no seam, those tests pass whether the
// logic holds or not.
var newEmbedderForModel = func(model string) (Embedder, error) {
	embedder, err := kglib.NewEmbedder(os.Getenv("OPENAI_API_KEY"), model)
	if err != nil {
		return nil, fmt.Errorf("embedding provider: %w", err)
	}
	return embedder, nil
}

// EmbedSourceFor returns the store whose stored vectors a query must be
// matched against: a plain store is its own source, and a federated store's is
// its primary layer — the project's own graph, whose meta records the model the
// vectors were written with.
//
// Returns nil for anything else (a remote-only federation, a test double),
// which SearchEmbedding reads as "no probe available": the model then comes
// from the environment and the presence check is skipped.
func EmbedSourceFor(s KeywordSearcher) embedModelSource {
	switch v := s.(type) {
	case *Store:
		return v
	case *FederatedStore:
		if p := v.PrimaryStore(); p != nil {
			return p
		}
	}
	return nil
}
