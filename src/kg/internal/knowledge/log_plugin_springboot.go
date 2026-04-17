package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SpringBootLogPlugin parses Spring Boot application logs
type SpringBootLogPlugin struct{}

var (
	// Spring Boot log pattern: 2024-01-15 10:30:45.123  INFO 12345 --- [main] com.example.MyClass : Message
	springBootLogPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d{3})\s+(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)\s+(\d+)\s+---\s+\[([^\]]+)\]\s+([^\s:]+)\s*:\s*(.+)$`)

	// Exception pattern
	springBootExceptionPattern = regexp.MustCompile(`^([a-zA-Z0-9.]+Exception|[a-zA-Z0-9.]+Error):\s*(.+)$`)

	// Stack trace pattern: at com.example.MyClass.method(MyClass.java:123)
	springBootStackTracePattern = regexp.MustCompile(`^\s+at\s+([a-zA-Z0-9.$_]+)\.([a-zA-Z0-9_<>]+)\(([^:)]+):(\d+)\)`)
)

func (p *SpringBootLogPlugin) Name() string {
	return "spring-boot"
}

func (p *SpringBootLogPlugin) Matches(filePath string, header []byte) bool {
	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".log" && ext != "" {
		// Allow files with no extension (common for app.log, server.log)
		if ext != "" {
			return false
		}
	}

	// Check for Spring Boot log patterns in header
	headerStr := string(header)
	return springBootLogPattern.MatchString(headerStr) ||
		strings.Contains(headerStr, "--- [") && strings.Contains(headerStr, " INFO ") ||
		strings.Contains(headerStr, "org.springframework")
}

func (p *SpringBootLogPlugin) Parse(filePath string, projectID string) (*LogParseResult, error) {
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
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max line

	var currentError *LogEntity
	var currentStackTrace []string
	seenErrors := make(map[string]bool)
	filesSeen := make(map[string]bool)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		result.Summary.TotalLines = lineNum

		// Try to match main log line
		if matches := springBootLogPattern.FindStringSubmatch(line); matches != nil {
			timestamp, _ := time.Parse("2006-01-02 15:04:05.000", matches[1])
			level := matches[2]
			thread := matches[4]
			logger := matches[5]
			message := matches[6]

			// Update time range
			if result.Summary.TimeRange.Start.IsZero() || timestamp.Before(result.Summary.TimeRange.Start) {
				result.Summary.TimeRange.Start = timestamp
			}
			if timestamp.After(result.Summary.TimeRange.End) {
				result.Summary.TimeRange.End = timestamp
			}

			// If we were collecting a stack trace, finalize it
			if currentError != nil {
				currentError.StackTrace = currentStackTrace
				currentError = nil
				currentStackTrace = nil
			}

			// Count by level
			switch level {
			case "ERROR", "FATAL":
				result.Summary.ErrorCount++

				// Check if this is an exception
				if exMatches := springBootExceptionPattern.FindStringSubmatch(message); exMatches != nil {
					exceptionType := exMatches[1]
					exceptionMsg := exMatches[2]

					entityName := fmt.Sprintf("error:%s:%s", exceptionType, timestamp.Format("2006-01-02"))
					if !seenErrors[entityName] {
						seenErrors[entityName] = true

						currentError = &LogEntity{
							Name:      entityName,
							Type:      "error",
							Severity:  "error",
							Timestamp: timestamp,
							Message:   fmt.Sprintf("%s: %s", exceptionType, exceptionMsg),
							Metadata: map[string]string{
								"exception_type": exceptionType,
								"thread":         thread,
								"logger":         logger,
								"file":           filepath.Base(filePath),
							},
						}
						result.Entities = append(result.Entities, *currentError)
						currentStackTrace = make([]string, 0)
					}
				}

			case "WARN":
				result.Summary.WarningCount++
			}

		} else if currentError != nil {
			// We're in an error context, look for stack traces
			if stMatches := springBootStackTracePattern.FindStringSubmatch(line); stMatches != nil {
				className := stMatches[1]
				method := stMatches[2]
				sourceFile := stMatches[3]
				sourceLine := stMatches[4]

				stackLine := fmt.Sprintf("%s.%s(%s:%s)", className, method, sourceFile, sourceLine)
				currentStackTrace = append(currentStackTrace, stackLine)

				// Track affected source file
				if !filesSeen[sourceFile] {
					filesSeen[sourceFile] = true
					result.Summary.AffectedFiles = append(result.Summary.AffectedFiles, sourceFile)
				}

				// Try to create relation to source file if we can find it
				// This would require searching the indexed entities for matching file names
			}
		}
	}

	// Finalize last error if any
	if currentError != nil {
		currentError.StackTrace = currentStackTrace
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan log file: %w", err)
	}

	return result, nil
}
