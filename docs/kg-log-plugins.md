# Log File Plugins for Knowledge Graph

The knowledge graph can index various application log formats (Spring Boot, Android, iOS, etc.) and extract structured information like errors, crashes, stack traces, and affected source files.

## How Plugin Matching Works

When the indexer encounters a log file, it uses a **two-stage matching process** to determine which plugin should parse it:

### Stage 1: File Extension and Filename Patterns (Fast Filter)

Plugins first check file extensions and filename patterns to quickly filter files:

```go
// Example: iOS plugin checks for specific extensions
ext := strings.ToLower(filepath.Ext(filePath))
if ext == ".crash" || ext == ".ips" {
    return true  // Definitely an iOS crash log
}

// Example: Android plugin checks for filename hints
baseName := strings.ToLower(filepath.Base(filePath))
if strings.Contains(baseName, "logcat") || strings.Contains(baseName, "android") {
    return true  // Likely an Android log
}
```

### Stage 2: Header Content Matching (Precise Detection)

For files with generic extensions like `.log`, plugins read the **first 1KB of file content** and match against format-specific patterns:

```go
// Spring Boot pattern: timestamp, level, PID, thread marker, class, message
// Example: 2024-01-15 10:30:45.123  INFO 12345 --- [main] com.example.MyClass : Message
headerStr := string(header)
return springBootLogPattern.MatchString(headerStr) ||
    strings.Contains(headerStr, "--- [") && strings.Contains(headerStr, " INFO ") ||
    strings.Contains(headerStr, "org.springframework")
```

### Plugin Selection Priority

The registry returns the **first plugin that matches**:

```go
func (r *LogPluginRegistry) FindPlugin(filePath string, header []byte) LogPlugin {
    for _, plugin := range r.plugins {
        if plugin.Matches(filePath, header) {
            return plugin
        }
    }
    return nil // No plugin matched - file is skipped
}
```

**Important**: Plugin order in the registry matters. More specific plugins should be registered before more generic ones.

### Example: Differentiating Multiple .log Files

Consider a project with various log files:

```
project/
├── logs/
│   ├── application.log      # Spring Boot format
│   ├── android_logcat.log    # Android format
│   ├── crash_2024.log        # iOS format
│   ├── nginx_access.log      # Would need NginxLogPlugin
│   └── custom.log            # Would need CustomLogPlugin
```

**How each is matched:**

1. **application.log**
   - Extension: `.log` (generic, continue to header check)
   - Header contains: `2024-01-15 10:30:45.123  INFO 12345 --- [main]`
   - **Matched by**: SpringBootLogPlugin (header pattern)

2. **android_logcat.log**
   - Filename contains: `logcat`
   - **Matched by**: AndroidLogcatPlugin (filename pattern)

3. **crash_2024.log**
   - Extension: `.log` (generic, continue to header check)
   - Header contains: `Incident Identifier:` and `Exception Type:`
   - **Matched by**: iOSCrashLogPlugin (header pattern)

4. **nginx_access.log**
   - Filename contains: `nginx` or `access.log`
   - **Would require**: Custom NginxLogPlugin

5. **custom.log**
   - No plugin matches
   - **Result**: File is skipped (not indexed)

## Built-in Plugins

Currently supported log formats:

- **Spring Boot** - Java/Kotlin application logs with pattern `2024-01-15 10:30:45.123  INFO 12345 --- [main] com.example.MyClass : Message`
- **Android Logcat** - Android device logs with pattern `01-15 10:30:45.123  1234  5678 E AndroidRuntime: FATAL EXCEPTION`
- **iOS Crash Logs** - iOS crash reports (`.crash`, `.ips` files) with incident identifiers and stack traces

## Creating Custom Plugins

To add support for a new log format, create a plugin that implements the `LogPlugin` interface:

```go
// In your plugin file (e.g., log_plugin_myformat.go)
package knowledge

import (
    "bufio"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "time"
)

type MyFormatLogPlugin struct{}

// Name returns a unique identifier for this plugin
func (p *MyFormatLogPlugin) Name() string {
    return "my-format"
}

// Matches returns true if this plugin should handle the given file
// filePath: full path to the file
// header: first 1KB of file content for pattern matching
//
// IMPORTANT: Be specific enough to avoid false positives!
// The first plugin to return true will handle the file.
func (p *MyFormatLogPlugin) Matches(filePath string, header []byte) bool {
    // Stage 1: Check file extension (fast filter)
    ext := strings.ToLower(filepath.Ext(filePath))
    if ext == ".mylog" {
        return true  // Unique extension = definite match
    }
    
    // Stage 2: Check for format-specific patterns in header (precise detection)
    // This is critical for .log files where extension alone isn't enough
    headerStr := string(header)
    
    // Option A: Check for unique marker string
    if strings.Contains(headerStr, "MY_LOG_FORMAT_MARKER") {
        return true
    }
    
    // Option B: Check for regex pattern unique to your format
    // Example: MyFormat logs start with [MYLOGS] timestamp
    myFormatPattern := regexp.MustCompile(`^\[MYLOGS\] \d{4}-\d{2}-\d{2}`)
    if myFormatPattern.MatchString(headerStr) {
        return true
    }
    
    return false  // Doesn't match our format
}

// Parse extracts structured data from the log file
func (p *MyFormatLogPlugin) Parse(filePath string, projectID string) (*LogParseResult, error) {
    f, err := os.Open(filePath)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    
    result := &LogParseResult{
        Entities:     make([]LogEntity, 0),
        Observations: make([]LogObservation, 0),
        Summary: LogSummary{
            AffectedFiles: make([]string, 0),
        },
    }
    
    scanner := bufio.NewScanner(f)
    lineNum := 0
    
    for scanner.Scan() {
        lineNum++
        line := scanner.Text()
        result.Summary.TotalLines = lineNum
        
        // Parse your log format here
        // Extract errors, warnings, timestamps, etc.
        
        // Example: create an error entity
        if strings.Contains(line, "ERROR") {
            entity := LogEntity{
                Name:      "error:my-error:timestamp",
                Type:      "error",
                Severity:  "error",
                Timestamp: time.Now(),
                Message:   "Error message from log",
                Metadata:  make(map[string]string),
            }
            result.Entities = append(result.Entities, entity)
            result.Summary.ErrorCount++
        }
    }
    
    return result, scanner.Err()
}
```

## Registering Custom Plugins

### Option 1: Add to Built-in Registry

Edit `src/kg/internal/knowledge/log_plugin.go` and add your plugin to the default registry:

```go
func NewLogPluginRegistry() *LogPluginRegistry {
    return &LogPluginRegistry{
        plugins: []LogPlugin{
            &SpringBootLogPlugin{},
            &AndroidLogcatPlugin{},
            &iOSCrashLogPlugin{},
            &MyFormatLogPlugin{}, // Add your plugin here
        },
    }
}
```

### Option 2: External Plugin Directory (Future Enhancement)

We could extend the registry to load plugins from a directory:

```go
// In .ai/config.json
{
  "log_plugins": [
    ".ai/plugins/my_plugin.so"
  ]
}
```

This would allow teams to add custom parsers without modifying the kg source code.

## Log Entity Structure

Parsed log entities have the following structure:

```go
type LogEntity struct {
    Name        string            // Entity name (e.g., "error:NullPointerException:2024-01-15")
    Type        string            // Entity type ("error", "crash", "warning")
    Metadata    map[string]string // Additional metadata
    Timestamp   time.Time         // When this occurred
    Severity    string            // "error", "warning", "info", "debug"
    SourceFile  string            // Source file if known (for linking)
    SourceLine  int               // Source line if known
    Message     string            // Human-readable message
    StackTrace  []string          // Stack trace if available
}
```

## Querying Log Entities

Once indexed, log entities can be queried through the knowledge graph:

```bash
# Search for errors
kg search "NullPointerException"

# Find crash-related entities
kg search "crash thread main"

# Locate errors in specific files
kg search "error MainActivity.java"
```

Observations attached to log entities include:
- `source: path/to/logfile.log`
- `severity: error`
- `timestamp: 2024-01-15T10:30:45Z`
- `message: ...`
- `stack_trace: ...`
- Metadata fields (thread, PID, exception type, etc.)

## Plugin Matching Best Practices

### 1. Be Specific in Pattern Matching

Your `Matches()` method must be specific enough to avoid false positives:

```go
// ❌ BAD: Too generic - matches any log file
func (p *MyPlugin) Matches(filePath string, header []byte) bool {
    return strings.HasSuffix(filePath, ".log")
}

// ✅ GOOD: Specific pattern unique to this format
func (p *MyPlugin) Matches(filePath string, header []byte) bool {
    ext := strings.ToLower(filepath.Ext(filePath))
    if ext == ".mylog" {
        return true  // Unique extension
    }
    
    // For generic .log files, check header for unique pattern
    headerStr := string(header)
    return strings.Contains(headerStr, "UniqueFormatMarker") &&
           regexp.MustCompile(`SpecificPattern`).MatchString(headerStr)
}
```

### 2. Use Two-Stage Matching

Always implement both stages for robust detection:

```go
func (p *MyPlugin) Matches(filePath string, header []byte) bool {
    // Stage 1: Fast filename/extension check
    baseName := strings.ToLower(filepath.Base(filePath))
    ext := strings.ToLower(filepath.Ext(filePath))
    
    if ext == ".myformat" || strings.Contains(baseName, "myapp") {
        return true
    }
    
    // Stage 2: Header content verification for generic extensions
    if ext == ".log" || ext == ".txt" {
        headerStr := string(header)
        return myFormatPattern.MatchString(headerStr)
    }
    
    return false
}
```

### 3. Test Edge Cases

Test your plugin against similar formats to ensure it doesn't match incorrectly:

```go
// Test files your plugin SHOULD match
✅ myapp.log (contains your format)
✅ server.mylog (unique extension)

// Test files your plugin should NOT match
❌ application.log (Spring Boot format)
❌ logcat.log (Android format)
❌ nginx.log (Nginx format)
```

### 4. Consider Plugin Order

When registering plugins, order matters. Put **more specific** plugins before **more generic** ones:

```go
func NewLogPluginRegistry() *LogPluginRegistry {
    return &LogPluginRegistry{
        plugins: []LogPlugin{
            &iOSCrashLogPlugin{},    // Very specific (.crash, .ips)
            &AndroidLogcatPlugin{},  // Specific (logcat pattern)
            &SpringBootLogPlugin{},  // Specific (--- [ pattern)
            &MyCustomPlugin{},       // Your custom format
            // Put generic plugins last
        },
    }
}
```

## General Best Practices

1. **Idempotency**: Plugins should be safe to run multiple times. The indexer tracks indexed files by path.

2. **Performance**: Use buffered scanners with appropriate buffer sizes for large log files:
   ```go
   scanner := bufio.NewScanner(f)
   buf := make([]byte, 64*1024)
   scanner.Buffer(buf, 10*1024*1024) // 10MB max line
   ```

3. **Stack Trace Linking**: Extract source file names and line numbers from stack traces to create relationships with code entities:
   ```go
   entity.SourceFile = "MainActivity.java"
   entity.SourceLine = 123
   ```

4. **Deduplication**: Use consistent naming for entities to avoid duplicates:
   ```go
   // Good: includes date for daily aggregation
   Name: "error:NullPointerException:2024-01-15"
   
   // Avoid: would create one entity per occurrence
   Name: "error:NullPointerException:" + uuid.New().String()
   ```

5. **Severity Mapping**: Map log levels to standard severity values:
   - `error` - Errors, exceptions, crashes
   - `warning` - Warnings, deprecations
   - `info` - Informational messages
   - `debug` - Debug/verbose output

## Example: Custom Nginx Log Plugin

```go
type NginxLogPlugin struct{}

var nginxAccessPattern = regexp.MustCompile(
    `^(\S+) \S+ \S+ \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+)`,
)

func (p *NginxLogPlugin) Name() string {
    return "nginx"
}

func (p *NginxLogPlugin) Matches(filePath string, header []byte) bool {
    baseName := filepath.Base(filePath)
    if strings.Contains(baseName, "nginx") || strings.Contains(baseName, "access.log") {
        return true
    }
    return nginxAccessPattern.Match(header)
}

func (p *NginxLogPlugin) Parse(filePath string, projectID string) (*LogParseResult, error) {
    // Parse nginx access logs
    // Extract 4xx/5xx errors as entities
    // Track request patterns as observations
    // ...
}
```
