package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

// kg health — a read-only report on the condition of the knowledge graph:
// counts, growth since the last run, staleness, orphans, and curation status.
// It is a report, not a gate: metric values never make it exit non-zero.

var (
	healthScopeName string
	healthJSON      bool
)

// healthSnapshotFile is the name of the growth snapshot inside .ai/. One file
// holds every scope's last measurement, keyed by scope name.
const healthSnapshotFile = "kg-health.json"

// healthDefaultScopeKey keys the legacy/default (unscoped) database in the
// snapshot file, where an empty string would be an unreadable key.
const healthDefaultScopeKey = "default"

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Report knowledge graph health: counts, growth, staleness, curation",
	Long: `Report the condition of the knowledge graph for this project.

Shows entity/relation/observation counts, growth since the previous run
(persisted at .ai/kg-health.json), the share of observations with no stored
timestamp (legacy, age unknown), orphaned entities, and how many observations
carry the [OBSOLETE curation marker.

Read-only against the graph; always exits 0 — it is a report, not a gate.
Use --json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := findProjectRoot(cwd)
		return runHealth(root, healthScopeName, healthJSON, os.Stdout)
	},
}

// healthGrowth is the per-metric delta against the previous snapshot.
type healthGrowth struct {
	Entities     int `json:"entities"`
	Relations    int `json:"relations"`
	Observations int `json:"observations"`
}

// healthOutput is the --json shape: the current measurement, the previous one
// when a snapshot existed, and the growth between them.
type healthOutput struct {
	Project  string                   `json:"project"`
	Scope    string                   `json:"scope"`
	Current  *knowledge.HealthMetrics `json:"current"`
	Previous *knowledge.HealthMetrics `json:"previous,omitempty"`
	Growth   *healthGrowth            `json:"growth,omitempty"`
}

func runHealth(root, scopeName string, jsonOut bool, out io.Writer) error {
	aiDir := filepath.Join(root, ".ai")
	projectID := projectIDFromCwd(root)

	dbPath, scopeName, err := resolveHealthDB(aiDir, scopeName)
	if err != nil {
		return err
	}

	store, err := knowledge.OpenStoreReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	metrics, err := knowledge.CollectHealthMetrics(store, projectID)
	if err != nil {
		return err
	}

	scopeKey := scopeName
	if scopeKey == "" {
		scopeKey = healthDefaultScopeKey
	}
	snapPath := filepath.Join(aiDir, healthSnapshotFile)
	previous := readHealthSnapshot(snapPath, scopeKey)
	if err := writeHealthSnapshot(snapPath, scopeKey, metrics); err != nil {
		return fmt.Errorf("write snapshot %s: %w", snapPath, err)
	}

	output := healthOutput{
		Project:  projectID,
		Scope:    scopeKey,
		Current:  metrics,
		Previous: previous,
	}
	if previous != nil {
		output.Growth = &healthGrowth{
			Entities:     metrics.Entities - previous.Entities,
			Relations:    metrics.Relations - previous.Relations,
			Observations: metrics.Observations - previous.Observations,
		}
	}

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}
	printHealth(out, output, snapPath)
	return nil
}

// resolveHealthDB resolves the database path the same way `kg stats` does:
// explicit scope, else the default scope, else the legacy knowledge.db.
// Returns the path and the scope name actually used ("" for legacy).
// A named scope that cannot be loaded is an error, never a silent fallback —
// reporting the legacy database's health under a scope the user asked for
// would be a wrong answer, not a degraded one.
func resolveHealthDB(aiDir, scopeName string) (string, string, error) {
	if scopeName == "" {
		defaultScope, err := knowledge.GetDefaultScope(aiDir)
		if err != nil {
			return "", "", err
		}
		scopeName = defaultScope
	}
	if scopeName == "" {
		return filepath.Join(aiDir, "knowledge.db"), "", nil
	}

	cfg, err := knowledge.LoadScopeConfig(aiDir, scopeName)
	if err != nil {
		return "", "", fmt.Errorf("load scope %q: %w", scopeName, err)
	}
	return filepath.Join(aiDir, cfg.Database), scopeName, nil
}

// readHealthSnapshot returns the previous measurement for scopeKey, or nil
// when there is none. A missing or unreadable snapshot is a first run, not an
// error: the report still works, it just cannot show growth yet.
func readHealthSnapshot(path, scopeKey string) *knowledge.HealthMetrics {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var snapshots map[string]*knowledge.HealthMetrics
	if err := json.Unmarshal(data, &snapshots); err != nil {
		return nil
	}
	return snapshots[scopeKey]
}

// writeHealthSnapshot stores metrics under scopeKey, preserving the other
// scopes' entries.
func writeHealthSnapshot(path, scopeKey string, metrics *knowledge.HealthMetrics) error {
	snapshots := make(map[string]*knowledge.HealthMetrics)
	if data, err := os.ReadFile(path); err == nil {
		// Best effort: a corrupt snapshot file is replaced, not fatal.
		_ = json.Unmarshal(data, &snapshots)
	}
	snapshots[scopeKey] = metrics

	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return err
	}
	// Temp+rename so a concurrent reader never sees a half-written file. The
	// read-modify-write across scope keys is still last-writer-wins, which is
	// acceptable for a snapshot two runs of a report command rarely share.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kg-health-*.tmp")
	if err != nil {
		return err
	}
	// CreateTemp opens 0600; keep the file world-readable like WriteFile did.
	_ = tmp.Chmod(0644)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func printHealth(out io.Writer, o healthOutput, snapPath string) {
	fmt.Fprintf(out, "Knowledge graph health — project %s, scope %s\n\n", o.Project, o.Scope)

	fmt.Fprintf(out, "Entities:     %d\n", o.Current.Entities)
	for _, line := range formatEntityTypes(o.Current.EntitiesByType) {
		fmt.Fprintf(out, "  %s\n", line)
	}
	fmt.Fprintf(out, "Relations:    %d\n", o.Current.Relations)
	fmt.Fprintf(out, "Observations: %d\n", o.Current.Observations)

	if o.Growth != nil {
		fmt.Fprintf(out, "\nGrowth since %s: entities %+d, relations %+d, observations %+d\n",
			o.Previous.GeneratedAt.Format("2006-01-02 15:04 MST"),
			o.Growth.Entities, o.Growth.Relations, o.Growth.Observations)
	} else {
		fmt.Fprintf(out, "\nNo previous snapshot — growth will be reported from the next run.\n")
	}

	share := 0.0
	if o.Current.Observations > 0 {
		share = float64(o.Current.ZeroTimestampObservations) / float64(o.Current.Observations) * 100
	}
	fmt.Fprintf(out, "\nZero-timestamp observations (legacy, age unknown): %d (%.1f%%)\n",
		o.Current.ZeroTimestampObservations, share)
	fmt.Fprintf(out, "Orphaned entities (no observations, no relations): %d\n", o.Current.OrphanedEntities)
	fmt.Fprintf(out, "[OBSOLETE-marked observations:                     %d\n", o.Current.ObsoleteObservations)

	fmt.Fprintf(out, "\nSnapshot written to %s\n", snapPath)
}

// formatEntityTypes renders the by-type counts sorted by count descending,
// then name, so the report is stable between runs.
func formatEntityTypes(byType map[string]int) []string {
	type typeCount struct {
		name  string
		count int
	}
	counts := make([]typeCount, 0, len(byType))
	for name, count := range byType {
		counts = append(counts, typeCount{name, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count != counts[j].count {
			return counts[i].count > counts[j].count
		}
		return counts[i].name < counts[j].name
	})
	lines := make([]string, len(counts))
	for i, tc := range counts {
		lines[i] = fmt.Sprintf("%-10s %d", tc.name+":", tc.count)
	}
	return lines
}

func init() {
	rootCmd.AddCommand(healthCmd)
	healthCmd.Flags().StringVar(&healthScopeName, "scope", "", "Report health for a specific scope")
	healthCmd.Flags().BoolVar(&healthJSON, "json", false, "Emit machine-readable JSON")
}
