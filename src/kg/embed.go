package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

var embedScopeName string

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Generate and attach vector embeddings for entities and observations",
	Long: `Backfill vector embeddings for anything in the graph that lacks them.

Indexing embeds as it goes when an embedding provider is configured, so this is
for the case where one was configured afterwards: rather than re-indexing the
whole project to pick up embeddings, embed only what is missing them.

Requires OPENAI_API_KEY or OLLAMA_HOST. Without embeddings, search falls back to
keyword matching, which works — it is just not semantic.

Examples:
  kg embed                      # default scope
  kg embed --scope selling      # a named scope
  kg embed --personal           # the personal knowledge store`,
	RunE: func(cmd *cobra.Command, args []string) error {
		embedder, err := knowledge.NewEmbedderFromEnv()
		if err != nil {
			return fmt.Errorf("no embedding provider configured: %w\n"+
				"Set OPENAI_API_KEY, or OLLAMA_HOST for a local model", err)
		}

		store, projectID, err := openTarget(false, embedScopeName)
		if err != nil {
			return err
		}
		defer store.Close()

		// Counted before the run so the summary can say what was done, rather
		// than reporting the zero that is true afterwards.
		entities, err := store.GetUnembeddedEntities(projectID)
		if err != nil {
			return fmt.Errorf("count un-embedded entities: %w", err)
		}
		observations, err := store.GetUnembeddedObservations(projectID)
		if err != nil {
			return fmt.Errorf("count un-embedded observations: %w", err)
		}
		if len(entities) == 0 && len(observations) == 0 {
			cmd.Printf("Nothing to embed — every entity and observation in %s already has an embedding.\n", projectID)
			return nil
		}

		cmd.Printf("Embedding %d entit%s and %d observation%s with %s...\n",
			len(entities), plural(len(entities), "y", "ies"),
			len(observations), plural(len(observations), "", "s"),
			embedder.Model())

		start := time.Now()
		if err := store.BatchEmbed(context.Background(), projectID, embedder); err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		cmd.Printf("✅ Done in %.1fs\n", time.Since(start).Seconds())
		return nil
	},
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	embedCmd.Flags().StringVar(&embedScopeName, "scope", "", "Scope to embed (default: default scope)")
	registerPersonalFlag(embedCmd)
}
