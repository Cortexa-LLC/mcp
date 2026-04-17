package knowledge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// execMeta holds the fields we read from .beads/tasks/{id}/00-metadata.json.
type execMeta struct {
	Role        string            `json:"role"`
	Description string            `json:"description"`
	SpawnedAt   string            `json:"spawned_at"`
	Status      string            `json:"status"`
	TaskID      string            `json:"task_id"`
	Metadata    map[string]string `json:"metadata"`
}

// execParsedLog holds the structured data extracted from an execution.log file.
type execParsedLog struct {
	Turns        int
	ToolCounts   map[string]int
	FilesTouched []string
	HasErrors    bool
	TotalTokens  int64
}

var (
	lgToolLine         = regexp.MustCompile(`🔧 Tool: ([A-Za-z_][A-Za-z0-9_]*)`)
	lgTurnLine         = regexp.MustCompile(`Turn (\d+) \(inactive:`)
	lgCumulativeTokens = regexp.MustCompile(`cumulative:(\d+)`)
	lgErrorIndicator   = regexp.MustCompile(`(?i)\b(error|fail|panic|❌|✗)\b`)
)

func readExecMeta(path string) *execMeta {
	data, err := os.ReadFile(path)
	if err != nil {
		return &execMeta{}
	}
	var m execMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return &execMeta{}
	}
	return &m
}

func parseExecLog(logPath string) (*execParsedLog, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	parsed := &execParsedLog{ToolCounts: make(map[string]int)}
	filesSeen := make(map[string]struct{})

	scanner := bufio.NewScanner(f)
	// Execution logs can contain large tool results. Use a 16 MB buffer so
	// common oversized lines don't abort the parse; lines exceeding that are
	// skipped by the scanner (tool result content, not structural lines).
	const maxBuf = 16 * 1024 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxBuf)

	for scanner.Scan() {
		line := scanner.Text()

		if m := lgToolLine.FindStringSubmatch(line); m != nil {
			name := m[1]
			parsed.ToolCounts[name]++
			if name == "Read" || name == "Write" || name == "Edit" || name == "MultiEdit" {
				prefix := fmt.Sprintf("🔧 Tool: %s(", name)
				if idx := strings.Index(line, prefix); idx >= 0 {
					arg := line[idx+len(prefix):]
					if end := strings.IndexAny(arg, ",)"); end > 0 {
						arg = strings.TrimSpace(arg[:end])
					}
					if strings.ContainsRune(arg, '.') && !strings.ContainsRune(arg, ' ') {
						if _, seen := filesSeen[arg]; !seen {
							filesSeen[arg] = struct{}{}
							parsed.FilesTouched = append(parsed.FilesTouched, arg)
						}
					}
				}
			}
		}

		if m := lgTurnLine.FindStringSubmatch(line); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > parsed.Turns {
				parsed.Turns = n
			}
		}

		if m := lgCumulativeTokens.FindStringSubmatch(line); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				parsed.TotalTokens = n
			}
		}

		if !parsed.HasErrors && lgErrorIndicator.MatchString(line) {
			lowered := strings.ToLower(line)
			if !strings.Contains(lowered, "err != nil") &&
				!strings.Contains(lowered, "return err") &&
				!strings.Contains(lowered, "error handling") &&
				!strings.Contains(lowered, `"error"`) &&
				!strings.Contains(lowered, "`error`") {
				parsed.HasErrors = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan execution log: %w", err)
	}
	sort.Strings(parsed.FilesTouched)
	return parsed, nil
}

func buildExecObservations(parsed *execParsedLog, meta *execMeta, folderName string) []string {
	var obs []string

	if meta.Role != "" {
		obs = append(obs, "role: "+meta.Role)
	}
	if meta.Status != "" {
		obs = append(obs, "status: "+meta.Status)
	}

	// First line of description as task summary (truncated to 300 chars).
	desc := strings.SplitN(meta.Description, "\n", 2)[0]
	desc = strings.TrimSpace(desc)
	const maxDesc = 300
	if utf8.RuneCountInString(desc) > maxDesc {
		runes := []rune(desc)
		desc = string(runes[:maxDesc]) + "…"
	}
	if desc != "" {
		obs = append(obs, "task: "+desc)
	}

	if meta.SpawnedAt != "" {
		if t, err := time.Parse(time.RFC3339, meta.SpawnedAt); err == nil {
			obs = append(obs, "started: "+t.Format("2006-01-02T15:04:05Z"))
		}
	}

	obs = append(obs, fmt.Sprintf("turns: %d", parsed.Turns))

	if parsed.TotalTokens > 0 {
		obs = append(obs, fmt.Sprintf("total_tokens: %d", parsed.TotalTokens))
	}

	if len(parsed.ToolCounts) > 0 {
		names := make([]string, 0, len(parsed.ToolCounts))
		for name := range parsed.ToolCounts {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s:%d", name, parsed.ToolCounts[name]))
		}
		obs = append(obs, "tool_calls: "+strings.Join(parts, ", "))
	}

	if len(parsed.FilesTouched) > 0 {
		obs = append(obs, "files_touched: "+strings.Join(parsed.FilesTouched, ", "))
	}

	if parsed.HasErrors {
		obs = append(obs, "had_errors: true")
	}

	return obs
}

type obsRecord struct {
	id       string
	entityID string
	content  string
	created  time.Time
}

// LogIndexStats holds counters returned by IndexExecutionLogs.
type LogIndexStats struct {
	LogsFound    int // total execution.log files discovered
	LogsIndexed  int // newly created entities (not already in graph)
	LogsSkipped  int // already-indexed (idempotent skip) or parse errors
	Observations int // observation nodes created
}

// IndexExecutionLogs walks .beads/tasks/*/execution.log under projectRoot and
// indexes each task run as an "exec:{folderName}" topic entity with structured
// observations. Already-indexed entities are skipped (idempotent).
//
// SCOPE: This function ONLY indexes agent execution logs from .beads/tasks/.
// It does NOT index:
//   - Application logs (crash logs, system logs, server logs)
//   - Build logs (Xcode build output, compiler warnings)
//   - Test logs (unit test results, XCTest output)
//   - CI/CD logs
//   - Any other general log files in the project
//
// All new entities and observations are collected in memory first, then written
// in two bulk COPY FROM JSON passes to avoid per-row Kuzu round-trips.
func IndexExecutionLogs(store *Store, projectID, projectRoot string) (LogIndexStats, error) {
	var stats LogIndexStats
	pattern := filepath.Join(projectRoot, ".beads", "tasks", "*", "execution.log")
	logPaths, err := filepath.Glob(pattern)
	if err != nil {
		return stats, fmt.Errorf("glob execution logs: %w", err)
	}
	stats.LogsFound = len(logPaths)

	var newEntities []entityRecord
	var newObs []obsRecord
	now := time.Now().UTC()

	for _, logPath := range logPaths {
		taskDir := filepath.Dir(logPath)
		folderName := filepath.Base(taskDir)
		entityName := "exec:" + folderName

		// Idempotency check — skip if already in the graph.
		existing, err := store.GetEntityByName(entityName, projectID)
		if err != nil {
			fmt.Printf("Warning: checking entity %s: %v\n", entityName, err)
			stats.LogsSkipped++
			continue
		}
		if existing != nil {
			stats.LogsSkipped++
			continue
		}

		meta := readExecMeta(filepath.Join(taskDir, "00-metadata.json"))

		parsed, err := parseExecLog(logPath)
		if err != nil {
			fmt.Printf("Warning: parse %s: %v\n", logPath, err)
			stats.LogsSkipped++
			continue
		}

		entityID := uuid.New().String()
		newEntities = append(newEntities, entityRecord{
			ID:        entityID,
			Name:      entityName,
			Type:      "topic",
			ProjectID: projectID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		for _, o := range buildExecObservations(parsed, meta, folderName) {
			newObs = append(newObs, obsRecord{
				id:       uuid.New().String(),
				entityID: entityID,
				content:  o,
				created:  now,
			})
		}
	}

	if len(newEntities) == 0 {
		return stats, nil
	}

	// Bulk-insert entities via COPY FROM JSON (same mechanism as main indexer).
	if err := bulkLoadExecEntities(store, newEntities); err != nil {
		return stats, fmt.Errorf("bulk load exec entities: %w", err)
	}

	// Bulk-insert observations and HAS_OBSERVATION edges.
	if len(newObs) > 0 {
		if err := bulkLoadExecObservations(store, newObs); err != nil {
			fmt.Printf("Warning: bulk load observations failed (%v); falling back to row-by-row\n", err)
			for _, o := range newObs {
				if _, err2 := store.CreateObservation(o.entityID, o.content, projectID); err2 != nil {
					fmt.Printf("Warning: add observation: %v\n", err2)
				}
			}
		}
	}

	stats.LogsIndexed = len(newEntities)
	stats.Observations = len(newObs)
	return stats, nil
}

func bulkLoadExecEntities(store *Store, entities []entityRecord) error {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("kg-exec-ents-%d.json", time.Now().UnixNano()))
	defer os.Remove(path)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range entities {
		if err := enc.Encode(map[string]string{
			"id":         e.ID,
			"name":       e.Name,
			"type":       e.Type,
			"project_id": e.ProjectID,
			"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at": e.UpdatedAt.UTC().Format(time.RFC3339),
		}); err != nil {
			f.Close()
			return err
		}
	}
	f.Close()

	result, err := store.query(fmt.Sprintf(
		`COPY Entity(id, name, type, project_id, created_at, updated_at) FROM '%s'`, path,
	))
	if err != nil {
		// Fall back to row-by-row if COPY FROM JSON is unavailable.
		for _, e := range entities {
			r, err2 := store.queryParams(`
				CREATE (n:Entity {
					id: $id, name: $name, type: $type,
					project_id: $project_id,
					created_at: $created_at, updated_at: $updated_at
				})`, map[string]any{
				"id": e.ID, "name": e.Name, "type": e.Type,
				"project_id": e.ProjectID,
				"created_at": e.CreatedAt, "updated_at": e.UpdatedAt,
			})
			if err2 == nil {
				r.Close()
			}
		}
		return nil
	}
	result.Close()
	return nil
}

func bulkLoadExecObservations(store *Store, obs []obsRecord) error {
	nodesPath := filepath.Join(os.TempDir(), fmt.Sprintf("kg-exec-obs-%d.json", time.Now().UnixNano()))
	edgesPath := filepath.Join(os.TempDir(), fmt.Sprintf("kg-exec-obs-edges-%d.json", time.Now().UnixNano()))
	defer os.Remove(nodesPath)
	defer os.Remove(edgesPath)

	nf, err := os.Create(nodesPath)
	if err != nil {
		return err
	}
	ef, err := os.Create(edgesPath)
	if err != nil {
		nf.Close()
		return err
	}
	nenc := json.NewEncoder(nf)
	eenc := json.NewEncoder(ef)
	for _, o := range obs {
		if err := nenc.Encode(map[string]string{
			"id":        o.id,
			"entity_id": o.entityID,
			"content":   o.content,
			"created_at": o.created.UTC().Format(time.RFC3339),
		}); err != nil {
			nf.Close()
			ef.Close()
			return err
		}
		if err := eenc.Encode(map[string]string{
			"from": o.entityID,
			"to":   o.id,
		}); err != nil {
			nf.Close()
			ef.Close()
			return err
		}
	}
	nf.Close()
	ef.Close()

	r, err := store.query(fmt.Sprintf(
		`COPY Observation(id, entity_id, content, created_at) FROM '%s'`, nodesPath,
	))
	if err != nil {
		return fmt.Errorf("COPY Observation: %w", err)
	}
	r.Close()

	r, err = store.query(fmt.Sprintf(`COPY HAS_OBSERVATION FROM '%s'`, edgesPath))
	if err != nil {
		return fmt.Errorf("COPY HAS_OBSERVATION: %w", err)
	}
	r.Close()
	return nil
}
