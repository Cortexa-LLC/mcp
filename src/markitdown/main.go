package main

import (
	"context"
	"log"

	"github.com/Cortexa-LLC/mcp/src/markitdown/converter"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer("markitdown", "0.1.0")
	conv := converter.NewConverter()
	registerTools(s, conv)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v\n", err)
	}
}

func registerTools(s *server.MCPServer, conv *converter.Converter) {
	// convert_to_markdown — convert a URI to Markdown
	s.AddTool(
		mcp.NewTool("convert_to_markdown",
			mcp.WithDescription("Convert a URI (http://, https://, or file://) to Markdown. "+
				"Simple formats (HTML, CSV, JSON, XML) are handled natively; "+
				"complex formats (PDF, DOCX, XLSX, PPTX, etc.) delegate to the markitdown CLI."),
			mcp.WithString("uri",
				mcp.Required(),
				mcp.Description("The URI to convert (e.g. https://example.com or file:///path/to/file.pdf)"),
			),
			mcp.WithBoolean("enable_plugins",
				mcp.Description("Enable markitdown plugins (e.g. LLM-based image descriptions). "+
					"Defaults to the MARKITDOWN_ENABLE_PLUGINS env var."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			uri, ok := req.Params.Arguments["uri"].(string)
			if !ok || uri == "" {
				return mcp.NewToolResultError("uri is required"), nil
			}
			enablePlugins, _ := req.Params.Arguments["enable_plugins"].(bool)

			result, err := conv.ConvertURI(ctx, uri, enablePlugins)
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
				"Simple formats (HTML, CSV, JSON, XML, TXT, MD) are handled natively; "+
				"complex formats (PDF, DOCX, XLSX, PPTX, etc.) delegate to the markitdown CLI."),
			mcp.WithString("file_path",
				mcp.Required(),
				mcp.Description("Absolute path to the local file to convert"),
			),
			mcp.WithBoolean("enable_plugins",
				mcp.Description("Enable markitdown plugins. Defaults to MARKITDOWN_ENABLE_PLUGINS env var."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			filePath, ok := req.Params.Arguments["file_path"].(string)
			if !ok || filePath == "" {
				return mcp.NewToolResultError("file_path is required"), nil
			}
			enablePlugins, _ := req.Params.Arguments["enable_plugins"].(bool)

			result, err := conv.ConvertFile(ctx, filePath, enablePlugins)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(result), nil
		},
	)

	// get_conversion_info — list formats and converter availability
	s.AddTool(
		mcp.NewTool("get_conversion_info",
			mcp.WithDescription("Return information about supported file formats, which converter handles each, "+
				"and whether the markitdown Python CLI is available."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(conv.GetConversionInfo(ctx)), nil
		},
	)
}
