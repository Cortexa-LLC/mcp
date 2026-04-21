package kglib

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIEmbedder implements the Embedder interface using OpenAI's API
type OpenAIEmbedder struct {
	client openai.Client
	model  string
	dims   int
}

// NewOpenAIEmbedder creates an OpenAI embedder
func NewOpenAIEmbedder(apiKey, model string) (*OpenAIEmbedder, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key required (set OPENAI_API_KEY)")
	}

	if model == "" {
		model = "text-embedding-3-small"
	}

	dims := 1536 // default dims
	if model == "text-embedding-3-large" {
		dims = 3072
	}

	return &OpenAIEmbedder{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
		dims:   dims,
	}, nil
}

// Embed generates embeddings using OpenAI's API
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
		Model: openai.EmbeddingModel(e.model),
	}
	resp, err := e.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai embeddings API: %w", err)
	}
	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		emb := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			emb[j] = float32(v)
		}
		result[i] = emb
	}
	return result, nil
}

func (e *OpenAIEmbedder) Dimensions() int {
	return e.dims
}

func (e *OpenAIEmbedder) Model() string {
	return e.model
}
