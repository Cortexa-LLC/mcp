package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// perfMetrics is a subset of monitoring.ExecutionMetrics that we decode from
// each metrics.json file. We redeclare the fields here to avoid importing the
// monitoring package (cmd/kg is a self-contained binary).
type perfMetrics struct {
	TaskID           string  `json:"task_id"`
	Role             string  `json:"role"`
	DurationMs       int64   `json:"duration_ms"`
	Turns            int     `json:"turns"`
	TotalTokens      int     `json:"total_tokens"`
	ToolCallsTotal   int     `json:"tool_calls_total"`
	KgPreflightBytes int     `json:"kg_preflight_bytes"`
	ExplorationRatio float64 `json:"exploration_ratio"`
	HasErrors        bool    `json:"has_errors"`
}

// perfBucket accumulates running totals for one group (KG enabled / disabled).
type perfBucket struct {
	Count            int
	TurnsSum         float64
	TokensSum        float64
	ExplorationSum   float64
	ExplorationCount int // entries where ExplorationRatio != -1
	ErrorCount       int
	DurationMsSum    float64
}

// perfReport is the JSON-serialisable output.
type perfReport struct {
	KgEnabled  perfSummary `json:"kg_enabled"`
	KgDisabled perfSummary `json:"kg_disabled"`
}

type perfSummary struct {
	Count            int     `json:"count"`
	AvgTurns         float64 `json:"avg_turns"`
	AvgTokens        float64 `json:"avg_tokens"`
	AvgExploration   float64 `json:"avg_exploration_ratio"`
	ErrorRate        float64 `json:"error_rate"`
	AvgDurationMs    float64 `json:"avg_duration_ms"`
}

var perfJsonFlag bool

var perfCmd = &cobra.Command{
	Use:   "perf",
	Short: "A/B performance report: KG-enabled vs KG-disabled task executions",
	Long: `Walks .ai/tasks/*/metrics.json (ai-pack project), bins runs by whether the KG
preflight context was active (kg_preflight_bytes > 0), then prints an aggregate
comparison table. Use --json to emit machine-readable JSON instead.`,
	RunE: runPerf,
}

func init() {
	perfCmd.Flags().BoolVar(&perfJsonFlag, "json", false, "Emit machine-readable JSON instead of the human-readable table")
}

func runPerf(cmd *cobra.Command, args []string) error {
	root, err := findProjectRootFromCwd()
	if err != nil {
		return fmt.Errorf("could not determine project root: %w", err)
	}

	tasksDir := filepath.Join(root, ".ai", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no tasks directory found at %s; run this from within an ai-pack project that has executed tasks", tasksDir)
		}
		return fmt.Errorf("could not read %s: %w", tasksDir, err)
	}

	var kgOn, kgOff perfBucket

	loaded := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metricsPath := filepath.Join(tasksDir, e.Name(), "metrics.json")
		data, err := os.ReadFile(metricsPath)
		if err != nil {
			// metrics.json absent or unreadable – skip silently.
			continue
		}
		var m perfMetrics
		if err := json.Unmarshal(data, &m); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", metricsPath, err)
			continue
		}
		loaded++

		var b *perfBucket
		if m.KgPreflightBytes > 0 {
			b = &kgOn
		} else {
			b = &kgOff
		}

		b.Count++
		b.TurnsSum += float64(m.Turns)
		b.TokensSum += float64(m.TotalTokens)
		b.DurationMsSum += float64(m.DurationMs)
		if m.ExplorationRatio >= 0 {
			b.ExplorationSum += m.ExplorationRatio
			b.ExplorationCount++
		}
		if m.HasErrors {
			b.ErrorCount++
		}
	}

	if loaded == 0 {
		return fmt.Errorf("no metrics.json files found under %s; run ai-pack tasks to generate data", tasksDir)
	}

	onSummary := summarise(&kgOn)
	offSummary := summarise(&kgOff)

	if perfJsonFlag {
		report := perfReport{
			KgEnabled:  onSummary,
			KgDisabled: offSummary,
		}
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	printPerfTable(onSummary, offSummary)
	return nil
}

func summarise(b *perfBucket) perfSummary {
	if b.Count == 0 {
		return perfSummary{}
	}
	avgExploration := math.NaN()
	if b.ExplorationCount > 0 {
		avgExploration = b.ExplorationSum / float64(b.ExplorationCount)
	}
	return perfSummary{
		Count:          b.Count,
		AvgTurns:       b.TurnsSum / float64(b.Count),
		AvgTokens:      b.TokensSum / float64(b.Count),
		AvgExploration: avgExploration,
		ErrorRate:      float64(b.ErrorCount) / float64(b.Count),
		AvgDurationMs:  b.DurationMsSum / float64(b.Count),
	}
}

// findProjectRootFromCwd resolves the project root by delegating to the
// existing findProjectRoot helper (which walks upward looking for .ai and
// falls back to git rev-parse).
func findProjectRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := findProjectRoot(cwd)
	if root == "" {
		return "", fmt.Errorf("no .ai directory found; run from within an ai-pack project")
	}
	return root, nil
}

func printPerfTable(on, off perfSummary) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                 A/B Performance Report: Knowledge Graph              ║")
	fmt.Println("╠══════════════════════════╦═══════════════════════╦════════════════════╣")
	fmt.Printf("║ %-24s ║ %-21s ║ %-18s ║\n", "Metric", "KG Enabled", "KG Disabled")
	fmt.Println("╠══════════════════════════╬═══════════════════════╬════════════════════╣")

	fmtFloat := func(v float64, unit string) string {
		if math.IsNaN(v) {
			return "n/a"
		}
		return fmt.Sprintf("%.2f%s", v, unit)
	}

	printRow := func(label string, a, b string) {
		fmt.Printf("║ %-24s ║ %-21s ║ %-18s ║\n", label, a, b)
	}

	printRow("Sample count", fmt.Sprintf("%d", on.Count), fmt.Sprintf("%d", off.Count))
	printRow("Avg turns", fmtFloat(on.AvgTurns, ""), fmtFloat(off.AvgTurns, ""))
	printRow("Avg tokens", fmtFloat(on.AvgTokens, ""), fmtFloat(off.AvgTokens, ""))
	printRow("Avg exploration ratio", fmtFloat(on.AvgExploration, ""), fmtFloat(off.AvgExploration, ""))
	printRow("Error rate", fmtFloat(on.ErrorRate*100, "%"), fmtFloat(off.ErrorRate*100, "%"))
	printRow("Avg duration (ms)", fmtFloat(on.AvgDurationMs, ""), fmtFloat(off.AvgDurationMs, ""))

	fmt.Println("╚══════════════════════════╩═══════════════════════╩════════════════════╝")
	fmt.Println()

	// Print a brief delta analysis if both groups have data.
	if on.Count > 0 && off.Count > 0 {
		printDelta("turns", on.AvgTurns, off.AvgTurns, true)
		printDelta("tokens", on.AvgTokens, off.AvgTokens, true)
		if !math.IsNaN(on.AvgExploration) && !math.IsNaN(off.AvgExploration) {
			printDelta("exploration ratio", on.AvgExploration, off.AvgExploration, true)
		}
		printDelta("error rate (%)", on.ErrorRate*100, off.ErrorRate*100, true)
		printDelta("duration (ms)", on.AvgDurationMs, off.AvgDurationMs, true)
		fmt.Println()
	}
}

// printDelta prints a one-line comparison for a single metric.
// lowerIsBetter controls the direction of the "better" indicator.
func printDelta(label string, onVal, offVal float64, lowerIsBetter bool) {
	if offVal == 0 {
		fmt.Printf("  %-22s  KG-on: %.2f  KG-off: %.2f  (no baseline)\n", label, onVal, offVal)
		return
	}
	pct := (onVal - offVal) / offVal * 100
	indicator := "▲"
	good := false
	if pct < 0 {
		indicator = "▼"
		good = lowerIsBetter
	} else if pct > 0 {
		good = !lowerIsBetter
	}
	arrow := indicator
	if good {
		arrow = "✓ " + indicator
	} else if pct != 0 {
		arrow = "✗ " + indicator
	}
	fmt.Printf("  %-22s  KG-on: %8.2f  KG-off: %8.2f  delta: %+.1f%%  %s\n",
		label, onVal, offVal, pct, arrow)
}
