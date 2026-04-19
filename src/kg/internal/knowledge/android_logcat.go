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

// AndroidLogcatPlugin parses Android logcat output
type AndroidLogcatPlugin struct{}

var (
	// Android logcat pattern: 01-15 10:30:45.123  1234  5678 E AndroidRuntime: FATAL EXCEPTION: main
	// Format: MM-DD HH:MM:SS.mmm  PID  TID LEVEL TAG: Message
	androidLogcatPattern = regexp.MustCompile(`^(\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEF])\s+([^:]+):\s*(.+)$`)

	// Android exception pattern
	androidExceptionPattern = regexp.MustCompile(`^([a-zA-Z0-9.]+Exception|[a-zA-Z0-9.]+Error):\s*(.+)$`)

	// Android stack trace: at com.example.MyClass.method(MyClass.java:123)
	androidStackTracePattern = regexp.MustCompile(`^\s+at\s+([a-zA-Z0-9.$_]+)\.([a-zA-Z0-9_<>]+)\(([^:)]+):(\d+)\)`)

	// Fatal exception header
	androidFatalPattern = regexp.MustCompile(`FATAL EXCEPTION:\s+(.+)`)
)

func (p *AndroidLogcatPlugin) Name() string {
	return "android-logcat"
}

func (p *AndroidLogcatPlugin) Matches(filePath string, header []byte) bool {
	// Check filename patterns
	baseName := strings.ToLower(filepath.Base(filePath))
	if strings.Contains(baseName, "logcat") || strings.Contains(baseName, "android") {
		return true
	}

	// Check for logcat patterns in header
	headerStr := string(header)
	return androidLogcatPattern.MatchString(headerStr) ||
		strings.Contains(headerStr, "AndroidRuntime") ||
		strings.Contains(headerStr, "FATAL EXCEPTION")
}

func (p *AndroidLogcatPlugin) Parse(filePath string, projectID string) (*LogParseResult, error) {
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
	scanner.Buffer(buf, 10*1024*1024)

	var currentCrash *LogEntity
	var currentStackTrace []string
	seenCrashes := make(map[string]bool)
	filesSeen := make(map[string]bool)

	lineNum := 0
	currentYear := time.Now().Year()

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		result.Summary.TotalLines = lineNum

		// Try to match main logcat line
		if matches := androidLogcatPattern.FindStringSubmatch(line); matches != nil {
			// Parse timestamp (logcat doesn't include year, use current year)
			timestampStr := fmt.Sprintf("%d-%s", currentYear, matches[1])
			timestamp, _ := time.Parse("2006-01-02 15:04:05.000", timestampStr)

			pid := matches[2]
			tid := matches[3]
			level := matches[4]
			tag := matches[5]
			message := matches[6]

			// Update time range
			if result.Summary.TimeRange.Start.IsZero() || timestamp.Before(result.Summary.TimeRange.Start) {
				result.Summary.TimeRange.Start = timestamp
			}
			if timestamp.After(result.Summary.TimeRange.End) {
				result.Summary.TimeRange.End = timestamp
			}

			// Map logcat levels to severity
			switch level {
			case "E": // Error
				result.Summary.ErrorCount++
			case "W": // Warning
				result.Summary.WarningCount++
			case "F": // Fatal (some variants)
				result.Summary.ErrorCount++
			}

			// Check for fatal exceptions
			if fatalMatches := androidFatalPattern.FindStringSubmatch(message); fatalMatches != nil {
				threadName := fatalMatches[1]

				entityName := fmt.Sprintf("crash:%s:%s", threadName, timestamp.Format("2006-01-02-15:04"))
				if !seenCrashes[entityName] {
					seenCrashes[entityName] = true

					currentCrash = &LogEntity{
						Name:      entityName,
						Type:      "crash",
						Severity:  "error",
						Timestamp: timestamp,
						Message:   fmt.Sprintf("Fatal exception in thread: %s", threadName),
						Metadata: map[string]string{
							"thread": threadName,
							"pid":    pid,
							"tid":    tid,
							"tag":    tag,
							"file":   filepath.Base(filePath),
						},
					}
					result.Entities = append(result.Entities, *currentCrash)
					currentStackTrace = make([]string, 0)
				}
			}

			// Check for exceptions
			if exMatches := androidExceptionPattern.FindStringSubmatch(message); exMatches != nil && level == "E" {
				exceptionType := exMatches[1]
				exceptionMsg := exMatches[2]

				entityName := fmt.Sprintf("error:%s:%s", exceptionType, timestamp.Format("2006-01-02"))
				if !seenCrashes[entityName] {
					seenCrashes[entityName] = true

					currentCrash = &LogEntity{
						Name:      entityName,
						Type:      "error",
						Severity:  "error",
						Timestamp: timestamp,
						Message:   fmt.Sprintf("%s: %s", exceptionType, exceptionMsg),
						Metadata: map[string]string{
							"exception_type": exceptionType,
							"tag":            tag,
							"pid":            pid,
							"file":           filepath.Base(filePath),
						},
					}
					result.Entities = append(result.Entities, *currentCrash)
					currentStackTrace = make([]string, 0)
				}
			}

		} else if currentCrash != nil {
			// Look for stack traces
			if stMatches := androidStackTracePattern.FindStringSubmatch(line); stMatches != nil {
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
			} else if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
				// End of stack trace
				if currentCrash != nil {
					currentCrash.StackTrace = currentStackTrace
					currentCrash = nil
					currentStackTrace = nil
				}
			}
		}
	}

	// Finalize last crash if any
	if currentCrash != nil {
		currentCrash.StackTrace = currentStackTrace
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan log file: %w", err)
	}

	return result, nil
}
