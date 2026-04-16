package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OllamaEmbedder implements the Embedder interface using Ollama's local API
type OllamaEmbedder struct {
	client  *http.Client
	baseURL string
	model   string
	dims    int
}

// NewOllamaEmbedder creates an Ollama embedder
func NewOllamaEmbedder(model string) *OllamaEmbedder {
	if model == "" {
		model = "nomic-embed-text"
	}

	// Default dimensions for common models
	dims := 768 // nomic-embed-text default
	if model == "nomic-embed-text" {
		dims = 768
	}

	return &OllamaEmbedder{
		client:  &http.Client{},
		baseURL: "http://localhost:11434",
		model:   model,
		dims:    dims,
	}
}

// Embed generates embeddings using Ollama's API
func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	result := make([][]float32, len(texts))

	for i, text := range texts {
		embedding, err := e.embedSingle(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		result[i] = embedding
	}

	return result, nil
}

// embedSingle embeds a single text
func (e *OllamaEmbedder) embedSingle(ctx context.Context, text string) ([]float32, error) {
	reqBody := map[string]interface{}{
		"model":  e.model,
		"prompt": text,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama API returned status %d", resp.StatusCode)
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Convert float64 to float32
	embedding := make([]float32, len(result.Embedding))
	for i, val := range result.Embedding {
		embedding[i] = float32(val)
	}

	return embedding, nil
}

// Dimensions returns the embedding vector size
func (e *OllamaEmbedder) Dimensions() int {
	return e.dims
}

// Model returns the model name
func (e *OllamaEmbedder) Model() string {
	return e.model
}
