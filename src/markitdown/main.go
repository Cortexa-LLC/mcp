package main

import (
	"context"
	"log"

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
	argURI      = "uri"
	argFilePath = "file_path"
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
	// convert_to_markdown — convert a URI to Markdown
	s.AddTool(
		mcp.NewTool("convert_to_markdown",
			mcp.WithDescription("Convert a URI (http://, https://, or file://) to Markdown. "+
				"All conversions are handled natively in Go (HTML, CSV, JSON, XML, DOCX, XLSX)."),
			mcp.WithString(argURI,
				mcp.Required(),
				mcp.Description("The URI to convert (e.g. https://example.com or file:///path/to/doc.docx)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uri, ok := req.Params.Arguments[argURI].(string)
			if !ok || uri == "" {
				return mcp.NewToolResultError(argURI + " is required"), nil
			}
			result, err := conv.ConvertURI(ctx, uri)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// convert_file_to_markdown — convert a local file path to Markdown
	s.AddTool(
		mcp.NewTool("convert_file_to_markdown",
			mcp.WithDescription("Convert a local file to Markdown by absolute path. "+
				"Supported formats: HTML, HTM, CSV, JSON, XML, TXT, MD, DOCX, XLSX, XLS, PPTX, PDF, PNG, JPG, JPEG (OCR via Tesseract if installed)."),
			mcp.WithString(argFilePath,
				mcp.Required(),
				mcp.Description("Absolute path to the local file to convert"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath, ok := req.Params.Arguments[argFilePath].(string)
			if !ok || filePath == "" {
				return mcp.NewToolResultError(argFilePath + " is required"), nil
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
			mcp.WithDescription("Return supported file formats, conversion approach, and active configuration."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(conv.GetConversionInfo(ctx)), nil
		},
	)
}
