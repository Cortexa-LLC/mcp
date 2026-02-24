package main

import (
	"context"
	"log"
	"strings"

	"github.com/Cortexa-LLC/mcp/src/markitdown/converter"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server identity constants.
const (
	serverName    = "markitdown"
	serverVersion = "0.1.0"
)

// MCP tool parameter key constants — shared between schema definitions and
// argument extraction so a typo in one place is caught by the other.
const (
	argURI = "uri"
)

func main() {
	s := server.NewMCPServer(serverName, serverVersion)
	conv := converter.NewConverter()
	registerTools(s, conv)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v\n", err)
	}
}

// registerTools binds MCP tool definitions to their handlers.
// It accepts the FileConverter interface so tests can inject a mock.
func registerTools(s *server.MCPServer, conv converter.FileConverter) {
	// convert_to_markdown — convert a file path or URL to Markdown
	s.AddTool(
		mcp.NewTool("convert_to_markdown",
			mcp.WithDescription("Convert a file or URL to Markdown. "+
				"Pass an absolute file path (e.g. /path/to/doc.pdf) or an http:// / https:// URL. "+
				"Supported formats: HTML, HTM, CSV, JSON, XML, TXT, MD, DOCX, XLSX, XLS, PPTX, PDF, PNG, JPG, JPEG (OCR via Tesseract if installed)."),
			mcp.WithString(argURI,
				mcp.Required(),
				mcp.Description("Absolute file path or http/https URL to convert"),
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

	// get_conversion_info — list formats and configuration
	s.AddTool(
		mcp.NewTool("get_conversion_info",
			mcp.WithDescription("Return supported file formats, conversion approach, and active configuration."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(conv.GetConversionInfo(ctx)), nil
		},
	)
}
