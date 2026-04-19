package knowledge

import (
	"time"
)

// LogPlugin defines the interface for log file parsers.
// Each plugin knows how to detect and parse a specific log format.
//
// PLUGIN MATCHING STRATEGY:
// Plugins use a two-stage matching process to differentiate between log files:
//
// Stage 1: File Extension and Filename Patterns (fast filter)
//   - Check for unique extensions (.crash, .ips, .mylog)
//   - Check for filename hints (logcat, nginx, etc.)
//   - This provides a fast initial filter
//
// Stage 2: Header Content Matching (precise detection)
//   - For generic extensions like .log, read first 1KB of file content
//   - Match against format-specific patterns (regex or string markers)
//   - This ensures accurate differentiation between similar files
//
// The first plugin to return true from Matches() handles the file, so plugin
// order in the registry matters. Register more specific plugins before generic ones.
//
// Example differentiation for .log files:
//   application.log → Spring Boot (header contains "--- [")
//   logcat.log → Android (filename contains "logcat")
//   crash.log → iOS (header contains "Incident Identifier")
//
// See docs/kg-log-plugins.md for detailed guidance on creating plugins.
type LogPlugin interface {
	// Name returns a unique identifier for this plugin (e.g., "spring-boot", "ios-crash")
	Name() string

	// Matches returns true if this plugin should handle the given file.
	//
	// Parameters:
	//   filePath: full path to the file being checked
	//   header: first 1KB of file content for pattern matching
	//
	// Implementation Strategy:
	//   1. Check file extension first (fast) - return true for unique extensions
	//   2. Check filename patterns (fast) - return true if filename hints at format
	//   3. For generic extensions (.log, .txt), examine header content (precise)
	//   4. Return false if no patterns match
	//
	// IMPORTANT: Be specific enough to avoid false positives! Your plugin should
	// only return true for files it can definitively parse. The first matching
	// plugin handles the file.
	Matches(filePath string, header []byte) bool

	// Parse extracts structured data from the log file.
	//
	// Returns:
	//   - LogParseResult containing entities (errors, crashes), observations,
	//     relations to code entities, and summary statistics
	//   - error if the file cannot be parsed
	//
	// Entities represent discrete events (errors, crashes, warnings) extracted
	// from the log. Observations are key-value metadata attached to entities.
	Parse(filePath string, projectID string) (*LogParseResult, error)
}

// LogParseResult contains entities and observations extracted from a log file.
type LogParseResult struct {
	// Entities to create (e.g., "error:NullPointerException", "crash:main-thread")
	Entities []LogEntity

	// Observations to attach to entities
	Observations []LogObservation

	// Relations between entities and existing code entities
	Relations []LogRelation

	// Summary statistics for the log file
	Summary LogSummary
}

// LogEntity represents an entity extracted from logs
type LogEntity struct {
	Name        string            // Entity name (e.g., "error:NullPointerException:2024-01-15")
	Type        string            // Entity type (e.g., "error", "crash", "warning")
	Metadata    map[string]string // Additional metadata
	Timestamp   time.Time         // When this occurred
	Severity    string            // "error", "warning", "info", "debug"
	SourceFile  string            // Source file if known (for linking)
	SourceLine  int               // Source line if known
	Message     string            // Human-readable message
	StackTrace  []string          // Stack trace if available
}

// LogObservation represents an observation extracted from logs
type LogObservation struct {
	EntityName string    // Which entity this observation belongs to
	Content    string    // Observation content
	Timestamp  time.Time // When this was observed
	Tags       []string  // Tags for categorization (e.g., ["performance", "sql"])
}

// LogRelation represents a relationship between a log entity and code entity
type LogRelation struct {
	FromEntity string // Log entity name
	ToEntity   string // Code entity name (file, function, etc.)
	Type       string // Relation type (CAUSED_BY, OCCURRED_IN, RELATED_TO)
}

// LogSummary provides aggregate statistics for the log file
type LogSummary struct {
	TotalLines    int
	ErrorCount    int
	WarningCount  int
	TimeRange     TimeRange
	AffectedFiles []string // Source files mentioned in logs
}

// TimeRange represents a time span
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// LogPluginRegistry manages available log plugins
type LogPluginRegistry struct {
	plugins []LogPlugin
}

// NewLogPluginRegistry creates a new registry with default plugins.
// Plugins are registered in order of specificity (most specific first).
//
// Plugin order matters because FindPlugin() returns the first match.
// More specific plugins (unique extensions, distinctive patterns) should
// be registered before more generic ones.
//
// Current order:
//   1. iOSCrashLogPlugin - very specific (.crash, .ips extensions)
//   2. AndroidLogcatPlugin - specific (filename and header patterns)
//   3. SpringBootLogPlugin - specific header pattern (--- [)
func NewLogPluginRegistry() *LogPluginRegistry {
	return &LogPluginRegistry{
		plugins: []LogPlugin{
			// &IOSCrashLogPlugin{},    // TODO: iOS crash log plugin
			// &AndroidLogcatPlugin{},  // TODO: Android logcat plugin
			&SpringBootLogPlugin{}, // Specific header pattern
			// Add custom plugins here, in order of specificity
		},
	}
}

// RegisterPlugin adds a plugin to the registry
func (r *LogPluginRegistry) RegisterPlugin(plugin LogPlugin) {
	r.plugins = append(r.plugins, plugin)
}

// FindPlugin returns the first plugin that matches the file, or nil.
//
// Iterates through registered plugins in order and returns the first one
// whose Matches() method returns true. If no plugin matches, returns nil
// and the file is skipped during indexing.
//
// This is why plugin registration order matters - more specific plugins
// should be registered first to ensure they get priority over generic ones.
//
// Example:
//   app.log with Spring Boot format:
//     1. iOSCrashLogPlugin.Matches() → false (no "Incident Identifier")
//     2. AndroidLogcatPlugin.Matches() → false (no "AndroidRuntime")
//     3. SpringBootLogPlugin.Matches() → true (found "--- [" pattern)
//     Returns: SpringBootLogPlugin
func (r *LogPluginRegistry) FindPlugin(filePath string, header []byte) LogPlugin {
	for _, plugin := range r.plugins {
		if plugin.Matches(filePath, header) {
			return plugin
		}
	}
	return nil
}

// ListPlugins returns all registered plugins
func (r *LogPluginRegistry) ListPlugins() []LogPlugin {
	return r.plugins
}
