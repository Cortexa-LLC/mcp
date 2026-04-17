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

// iOSCrashLogPlugin parses iOS crash logs (.crash, .ips files)
type iOSCrashLogPlugin struct{}

var (
	// iOS crash log patterns
	iosCrashHeaderPattern      = regexp.MustCompile(`^Incident Identifier:\s+(.+)$`)
	iosCrashTypePattern        = regexp.MustCompile(`^Exception Type:\s+(.+)$`)
	iosCrashReasonPattern      = regexp.MustCompile(`^Exception (?:Codes|Subtype|Message):\s+(.+)$`)
	iosTerminationPattern      = regexp.MustCompile(`^Termination Reason:\s+(.+)$`)
	iosDatePattern             = regexp.MustCompile(`^Date/Time:\s+(.+)$`)
	iosThreadPattern           = regexp.MustCompile(`^Thread\s+(\d+)\s*(?:name:\s*(.+?))?(?:\s+Crashed)?:`)
	iosStackFramePattern       = regexp.MustCompile(`^\d+\s+(\S+)\s+0x[0-9a-f]+\s+(.+?)\s+\+\s+\d+`)
	iosSwiftStackFramePattern  = regexp.MustCompile(`^(\d+)\s+(\S+)\s+0x[0-9a-f]+\s+([^\s]+(?:\s+in\s+[^+]+)?)\s+(?:\+\s+\d+)?`)

	// Swift method pattern: MyClass.methodName() -> () at MyClass.swift:123
	iosSwiftMethodPattern      = regexp.MustCompile(`([A-Za-Z0-9_]+)\.([A-Za-Z0-9_<>]+)\(.*\).*?(?:at\s+([^:]+):(\d+))?`)
)

func (p *iOSCrashLogPlugin) Name() string {
	return "ios-crash"
}

func (p *iOSCrashLogPlugin) Matches(filePath string, header []byte) bool {
	// Check file extension
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".crash" || ext == ".ips" {
		return true
	}

	// Check for iOS crash patterns in header
	headerStr := string(header)
	return strings.Contains(headerStr, "Incident Identifier") ||
		strings.Contains(headerStr, "Exception Type") ||
		strings.Contains(headerStr, "OS Version:") && strings.Contains(headerStr, "iOS")
}

func (p *iOSCrashLogPlugin) Parse(filePath string, projectID string) (*LogParseResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := &LogParseResult{
		Entities:     make([]LogEntity, 0),
		Observations: make([]LogObservation, 0),
		Summary: LogSummary{
			ErrorCount:    1, // Crash log = 1 error
			AffectedFiles: make([]string, 0),
		},
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var crash *LogEntity
	var currentStackTrace []string
	filesSeen := make(map[string]bool)
	inThreadSection := false
	crashedThread := ""

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		result.Summary.TotalLines = lineNum

		// Parse crash metadata
		if matches := iosCrashHeaderPattern.FindStringSubmatch(line); matches != nil {
			incidentID := matches[1]
			crash = &LogEntity{
				Name:       fmt.Sprintf("crash:%s", incidentID),
				Type:       "crash",
				Severity:   "error",
				Metadata:   make(map[string]string),
				StackTrace: make([]string, 0),
			}
			crash.Metadata["incident_id"] = incidentID
			crash.Metadata["file"] = filepath.Base(filePath)
		}

		if crash != nil {
			if matches := iosCrashTypePattern.FindStringSubmatch(line); matches != nil {
				crash.Metadata["exception_type"] = matches[1]
			}

			if matches := iosCrashReasonPattern.FindStringSubmatch(line); matches != nil {
				reason := matches[1]
				if crash.Message == "" {
					crash.Message = reason
				} else {
					crash.Message += " | " + reason
				}
			}

			if matches := iosTerminationPattern.FindStringSubmatch(line); matches != nil {
				crash.Metadata["termination_reason"] = matches[1]
				if crash.Message == "" {
					crash.Message = matches[1]
				}
			}

			if matches := iosDatePattern.FindStringSubmatch(line); matches != nil {
				// Try to parse timestamp
				timestamp, err := time.Parse("2006-01-02 15:04:05.00 -0700", matches[1])
				if err == nil {
					crash.Timestamp = timestamp
					result.Summary.TimeRange.Start = timestamp
					result.Summary.TimeRange.End = timestamp
				}
			}

			// Detect thread sections
			if matches := iosThreadPattern.FindStringSubmatch(line); matches != nil {
				inThreadSection = true
				threadNum := matches[1]
				threadName := ""
				if len(matches) > 2 {
					threadName = matches[2]
				}

				// Check if this is the crashed thread
				if strings.Contains(line, "Crashed") {
					crashedThread = threadNum
					if threadName != "" {
						crash.Metadata["crashed_thread"] = fmt.Sprintf("%s (%s)", threadNum, threadName)
					} else {
						crash.Metadata["crashed_thread"] = threadNum
					}
					currentStackTrace = make([]string, 0)
				}
			} else if inThreadSection {
				// Parse stack frames from crashed thread
				if crashedThread != "" {
					// Try Swift-style stack frame first
					if matches := iosSwiftStackFramePattern.FindStringSubmatch(line); matches != nil {
						module := matches[2]
						symbol := matches[3]

						// Try to extract Swift method and file info
						if swiftMatches := iosSwiftMethodPattern.FindStringSubmatch(symbol); swiftMatches != nil {
							className := swiftMatches[1]
							methodName := swiftMatches[2]
							sourceFile := ""
							sourceLine := ""
							if len(swiftMatches) > 3 {
								sourceFile = swiftMatches[3]
								sourceLine = swiftMatches[4]
							}

							var stackLine string
							if sourceFile != "" && sourceLine != "" {
								stackLine = fmt.Sprintf("%s.%s at %s:%s", className, methodName, sourceFile, sourceLine)

								// Track source file
								if !filesSeen[sourceFile] {
									filesSeen[sourceFile] = true
									result.Summary.AffectedFiles = append(result.Summary.AffectedFiles, sourceFile)
								}
							} else {
								stackLine = fmt.Sprintf("%s.%s in %s", className, methodName, module)
							}
							currentStackTrace = append(currentStackTrace, stackLine)
						} else {
							// Fallback to raw symbol
							stackLine := fmt.Sprintf("%s in %s", symbol, module)
							currentStackTrace = append(currentStackTrace, stackLine)
						}
					} else if matches := iosStackFramePattern.FindStringSubmatch(line); matches != nil {
						// Objective-C style stack frame
						module := matches[1]
						symbol := matches[2]
						stackLine := fmt.Sprintf("%s in %s", symbol, module)
						currentStackTrace = append(currentStackTrace, stackLine)
					} else if line == "" || !strings.HasPrefix(line, " ") {
						// End of thread section
						inThreadSection = false
						if len(currentStackTrace) > 0 {
							crash.StackTrace = currentStackTrace
							currentStackTrace = nil
						}
					}
				}
			}
		}
	}

	// Finalize crash entity
	if crash != nil {
		if crash.Message == "" {
			crash.Message = "iOS application crashed"
		}
		if len(currentStackTrace) > 0 {
			crash.StackTrace = currentStackTrace
		}
		result.Entities = append(result.Entities, *crash)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan log file: %w", err)
	}

	return result, nil
}
