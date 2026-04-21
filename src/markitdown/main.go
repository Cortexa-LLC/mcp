package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/Cortexa-LLC/mcp/src/markitdown/converter"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Version information injected at build time.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Server identity constants.
const (
	serverName = "markitdown"
)

// MCP tool parameter key constants — shared between schema definitions and
// argument extraction so a typo in one place is caught by the other.
const (
	argURI = "uri"
)

func main() {
	// Handle --version flag before other processing
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		printVersion()
		return
	}

	// Check if running in MCP server mode (stdin is not a terminal)
	stat, _ := os.Stdin.Stat()
	isPipe := (stat.Mode() & os.ModeCharDevice) == 0

	// If running as MCP server (stdin is piped)
	if isPipe && len(os.Args) == 1 {
		runMCPServer()
		return
	}

	// Otherwise, run as CLI
	runCLI()
}

func printVersion() {
	ver := Version
	if BuildTime != "unknown" {
		ver = fmt.Sprintf("%s built %s", Version, BuildTime)
	}
	fmt.Printf("markitdown version %s\n", ver)
	fmt.Printf("Platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Go:        %s\n", runtime.Version())
}

func runMCPServer() {
	s := server.NewMCPServer(serverName, Version)
	conv := converter.NewConverter()
	registerTools(s, conv)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v\n", err)
	}
}

func runCLI() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `markitdown - Convert documents to Markdown

Usage:
  markitdown [options] <file-or-url>
  markitdown --version
  markitdown --help

Options:
  --output, -o    Output file (default: stdout)
  --version, -v   Show version information
  --help, -h      Show this help message

Supported formats:
  HTML, HTM, CSV, JSON, XML, TXT, MD, DOCX, XLSX, XLS, PPTX, PDF
  PNG, JPG, JPEG (OCR via Tesseract if installed)

Examples:
  markitdown document.pdf
  markitdown https://example.com/page.html
  markitdown document.docx -o output.md
`)
	}

	outputFile := flag.String("output", "", "Output file (default: stdout)")
	flag.StringVar(outputFile, "o", "", "Output file (short)")
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	input := flag.Arg(0)
	ctx := context.Background()
	conv := converter.NewConverter()

	var result string
	var err error

	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		result, err = conv.ConvertURI(ctx, input)
	} else {
		result, err = conv.ConvertFile(ctx, input)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Write output
	var w io.Writer = os.Stdout
	if *outputFile != "" {
		f, err := os.Create(*outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}

	fmt.Fprint(w, result)
}

// registerTools binds MCP tool definitions to their handlers.
// It accepts the FileConverter interface so tests can inject a mock.
func registerTools(s *server.MCPServer, conv converter.FileConverter) {
	// convert_to_markdown — convert a file path or URL to Markdown
	s.AddTool(
		mcp.NewTool("convert_to_markdown",
			mcp.WithDescription("Convert a resource to markdown using hybrid approach (native TypeScript for simple formats, Python for complex). "+
				"Pass an absolute file path (e.g. /path/to/doc.pdf) or an http:// / https:// URL. "+
				"Supported formats: HTML, HTM, CSV, JSON, XML, TXT, MD, DOCX, XLSX, XLS, PPTX, PDF, PNG, JPG, JPEG (OCR via Tesseract if installed)."),
			mcp.WithString(argURI,
				mcp.Required(),
				mcp.Description("URI to convert (http:, https:, file:, or data:)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			input, ok := req.Params.Arguments[argURI].(string)
			if !ok || input == "" {
				return mcp.NewToolResultError(argURI + " is required"), nil
			}
			var result string
			var err error
			if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
				result, err = conv.ConvertURI(ctx, input)
			} else {
				result, err = conv.ConvertFile(ctx, input)
			}
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// convert_file_to_markdown — convert a local file to markdown (compatibility with Node version)
	s.AddTool(
		mcp.NewTool("convert_file_to_markdown",
			mcp.WithDescription("Convert a local file to markdown using hybrid approach (native TypeScript for simple formats, Python for complex). "+
				"Supported formats: HTML, HTM, CSV, JSON, XML, TXT, MD, DOCX, XLSX, XLS, PPTX, PDF, PNG, JPG, JPEG (OCR via Tesseract if installed)."),
			mcp.WithString("filePath",
				mcp.Required(),
				mcp.Description("Path to the file to convert"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath, ok := req.Params.Arguments["filePath"].(string)
			if !ok || filePath == "" {
				return mcp.NewToolResultError("filePath is required"), nil
			}
			result, err := conv.ConvertFile(ctx, filePath)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// get_conversion_info — list formats and configuration
	s.AddTool(
		mcp.NewTool("get_conversion_info",
			mcp.WithDescription("Get information about which conversion methods are used for different file formats"),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(conv.GetConversionInfo(ctx)), nil
		},
	)
}
