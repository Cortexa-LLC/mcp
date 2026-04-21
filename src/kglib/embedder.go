package kglib

import (
	"context"
	"os"
	"strings"
)

// Embedder generates vector embeddings for text
type Embedder interface {
	// Embed generates embeddings for a batch of texts
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the size of the embedding vectors
	Dimensions() int

	// Model returns the name of the embedding model
	Model() string
}

// NewEmbedder creates an embedder based on the model name
// Supports:
//   - OpenAI models: "text-embedding-3-small", "text-embedding-3-large", "openai:model-name"
//   - Ollama models: "ollama:nomic-embed-text", "nomic-embed-text", etc.
func NewEmbedder(apiKey, model string) (Embedder, error) {
	if model == "" {
		model = "text-embedding-3-small"
	}
	if strings.HasPrefix(model, "openai:") {
		modelName := strings.TrimPrefix(model, "openai:")
		return NewOpenAIEmbedder(apiKey, modelName)
	}
	if strings.HasPrefix(model, "ollama:") {
		modelName := strings.TrimPrefix(model, "ollama:")
		return NewOllamaEmbedder(modelName), nil
	}
	if strings.HasPrefix(model, "text-embedding-") {
		return NewOpenAIEmbedder(apiKey, model)
	}
	return NewOllamaEmbedder(model), nil
}

// NewEmbedderFromEnv creates an embedder using environment variables
// Reads KNOWLEDGE_EMBED_MODEL (defaults to text-embedding-3-small) and OPENAI_API_KEY
func NewEmbedderFromEnv() (Embedder, error) {
	model := os.Getenv("KNOWLEDGE_EMBED_MODEL")
	apiKey := os.Getenv("OPENAI_API_KEY")
	return NewEmbedder(apiKey, model)
}
