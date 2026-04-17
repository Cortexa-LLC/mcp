package knowledge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AppLogIndexStats holds counters returned by IndexApplicationLogs.
type AppLogIndexStats struct {
	FilesScanned int // total files examined
	LogsMatched  int // files matched by a plugin
	LogsIndexed  int // successfully parsed and indexed
	LogsSkipped  int // already indexed or parse errors
	Entities     int // log entities created (errors, crashes, etc.)
	Observations int // observations created
}

// Common directories to exclude when scanning for application logs.
var defaultExcludeDirs = []string{
	".git", ".beads", ".ai", "node_modules", "vendor", "build", "dist",
	".gradle", ".idea", ".vscode", "__pycache__", ".pytest_cache",
}

// IndexApplicationLogs scans projectRoot for application log files and indexes
// them using registered log plugins (Spring Boot, Android, iOS, etc.).
//
// Unlike IndexExecutionLogs (which only handles .beads/tasks agent logs), this
// function processes general application logs: crash logs, server logs, build
// logs, test logs, etc.
//
// FILE DISCOVERY AND MATCHING:
//
// 1. Walks the entire project directory tree
// 2. Skips excluded directories (.git, node_modules, build, etc.)
// 3. Applies scope filter if provided (for multi-scope monorepos)
// 4. For each file, reads first 1KB as header content
// 5. Passes file path and header to plugin registry
// 6. First matching plugin parses the file
// 7. No match → file is skipped (not an error)
//
// DIFFERENTIATION BETWEEN .log FILES:
//
// Plugins use a two-stage matching strategy:
//   Stage 1: Check file extension/filename (fast)
//   Stage 2: Check header content patterns (precise)
//
// Example with multiple .log files:
//   application.log → SpringBootLogPlugin (header: "--- [main]")
//   logcat.log → AndroidLogcatPlugin (filename: "logcat")
//   crash_2024.log → iOSCrashLogPlugin (header: "Incident Identifier")
//   nginx.log → nil (no plugin matches, skipped)
//
// IDEMPOTENCY:
//
// Already-indexed files are skipped based on log file path. The system tracks
// which files have been indexed by querying for existing entities with names
// matching the pattern "log:{relative_path}:*".
func IndexApplicationLogs(store *Store, projectID, projectRoot string, scopeFilter *ScopeConfig) (AppLogIndexStats, error) {
	var stats AppLogIndexStats
	registry := NewLogPluginRegistry()

	var newEntities []entityRecord
	var newObs []obsRecord
	now := time.Now().UTC()

	// Track which log files we've already indexed (by relative path).
	indexedLogs := make(map[string]bool)

	// Query existing log entities to avoid re-indexing.
	// We use a naming convention: log entities have names like "log:{relative_path}:{entity_id}"
	result, err := store.queryParams(`
		MATCH (e:Entity {project_id: $project_id})
		WHERE e.name STARTS WITH 'log:'
		RETURN e.name
	`, map[string]any{"project_id": projectID})
	if err != nil {
		return stats, fmt.Errorf("query existing logs: %w", err)
	}
	for result.HasNext() {
		row, err := result.Next()
		if err != nil {
			break
		}
		name := row.GetValue(0).(string)
		// Extract relative path from name pattern: log:{path}:...
		parts := strings.SplitN(name, ":", 3)
		if len(parts) >= 2 {
			indexedLogs[parts[1]] = true
		}
	}
	result.Close()

	// Walk the project directory looking for log files.
	err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't access
		}

		// Skip directories in exclusion list.
		if info.IsDir() {
			for _, exclude := range defaultExcludeDirs {
				if info.Name() == exclude {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Only process regular files.
		if !info.Mode().IsRegular() {
			return nil
		}

		// Skip files larger than 100MB (likely not text logs).
		if info.Size() > 100*1024*1024 {
			return nil
		}

		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			relPath = path
		}

		// Apply scope filter if provided.
		if scopeFilter != nil && !scopeFilter.ShouldIncludePath(relPath) {
			return nil
		}

		stats.FilesScanned++

		// Skip if already indexed.
		if indexedLogs[relPath] {
			stats.LogsSkipped++
			return nil
		}

		// Read file header for plugin matching.
		header, err := readFileHeader(path, 1024)
		if err != nil {
			return nil // skip files we can't read
		}

		// Find matching plugin.
		plugin := registry.FindPlugin(path, header)
		if plugin == nil {
			return nil // no plugin matched, skip
		}

		stats.LogsMatched++

		// Parse the log file.
		result, err := plugin.Parse(path, projectID)
		if err != nil {
			fmt.Printf("Warning: parse %s with %s: %v\n", relPath, plugin.Name(), err)
			stats.LogsSkipped++
			return nil
		}

		// Create entities from parsed log.
		for _, logEntity := range result.Entities {
			entityID := uuid.New().String()

			// Entity name includes log path for idempotency and traceability.
			// Format: log:{relative_path}:{original_name}
			entityName := fmt.Sprintf("log:%s:%s", relPath, logEntity.Name)

			newEntities = append(newEntities, entityRecord{
				ID:        entityID,
				Name:      entityName,
				Type:      logEntity.Type,
				ProjectID: projectID,
				CreatedAt: now,
				UpdatedAt: now,
			})
			stats.Entities++

			// Add metadata as observations.
			obs := buildLogEntityObservations(logEntity, relPath)
			for _, content := range obs {
				newObs = append(newObs, obsRecord{
					id:       uuid.New().String(),
					entityID: entityID,
					content:  content,
					created:  now,
				})
			}
		}

		// Add standalone observations.
		// (For now, observations are attached to entities, but this allows future extension)

		return nil
	})

	if err != nil {
		return stats, fmt.Errorf("walk project: %w", err)
	}

	// Bulk insert entities.
	if len(newEntities) > 0 {
		if err := bulkLoadExecEntities(store, newEntities); err != nil {
			return stats, fmt.Errorf("bulk load log entities: %w", err)
		}
		stats.LogsIndexed = len(newEntities)
	}

	// Bulk insert observations.
	if len(newObs) > 0 {
		if err := bulkLoadExecObservations(store, newObs); err != nil {
			fmt.Printf("Warning: bulk load log observations failed (%v); falling back to row-by-row\n", err)
			for _, o := range newObs {
				if _, err2 := store.CreateObservation(o.entityID, o.content, projectID); err2 != nil {
					fmt.Printf("Warning: add observation: %v\n", err2)
				}
			}
		}
		stats.Observations = len(newObs)
	}

	return stats, nil
}

// readFileHeader reads the first maxBytes from a file for plugin matching.
func readFileHeader(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	header := make([]byte, maxBytes)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return header[:n], nil
}

// buildLogEntityObservations converts a LogEntity into observation strings.
func buildLogEntityObservations(entity LogEntity, filePath string) []string {
	var obs []string

	obs = append(obs, fmt.Sprintf("source: %s", filePath))
	obs = append(obs, fmt.Sprintf("severity: %s", entity.Severity))

	if !entity.Timestamp.IsZero() {
		obs = append(obs, fmt.Sprintf("timestamp: %s", entity.Timestamp.Format(time.RFC3339)))
	}

	if entity.Message != "" {
		obs = append(obs, fmt.Sprintf("message: %s", entity.Message))
	}

	// Add metadata as key:value observations.
	for k, v := range entity.Metadata {
		obs = append(obs, fmt.Sprintf("%s: %s", k, v))
	}

	// Add stack trace as a single multi-line observation.
	if len(entity.StackTrace) > 0 {
		stackStr := "stack_trace:\n" + strings.Join(entity.StackTrace, "\n")
		obs = append(obs, stackStr)
	}

	if entity.SourceFile != "" {
		obs = append(obs, fmt.Sprintf("source_file: %s", entity.SourceFile))
		if entity.SourceLine > 0 {
			obs = append(obs, fmt.Sprintf("source_line: %d", entity.SourceLine))
		}
	}

	return obs
}
