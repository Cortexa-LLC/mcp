package converter

// openai.go — Optional OpenAI Vision API integration for image enhancement.
//
// When enabled via config, enhanceImageWithOpenAI generates semantic descriptions
// of images instead of (or in addition to) OCR text extraction. This provides
// better understanding of diagrams, charts, screenshots, and visual content.
//
// The integration is opt-in and requires:
// - MARKITDOWN_ENABLE_OPENAI=true
// - OPENAI_API_KEY=<your-key>
//
// Cost considerations: Each image costs ~$0.01-0.05 depending on size and model.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	openaiAPIURL     = "https://api.openai.com/v1/chat/completions"
	openaiMaxTokens  = 500
	openaiTimeout    = 30 * time.Second
	imagePrompt      = "Describe this image in detail. If it contains text, include the text. If it's a diagram or chart, explain what it shows."
)

// OpenAIClient handles requests to the OpenAI Vision API.
type OpenAIClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIClient creates a client with the given API key and model.
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: openaiTimeout},
	}
}

// openaiRequest is the structure for OpenAI Vision API requests.
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	MaxTokens int            `json:"max_tokens"`
}

type openaiMessage struct {
	Role    string               `json:"role"`
	Content []openaiContentPart  `json:"content"`
}

type openaiContentPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *openaiImageURL  `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
}

// openaiResponse is the structure for OpenAI Vision API responses.
type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// DescribeImage sends image data to OpenAI Vision API and returns a description.
// imageData should be raw image bytes (PNG, JPG, etc.)
// imageType is the file extension (e.g., ".png", ".jpg")
func (c *OpenAIClient) DescribeImage(imageData []byte, imageType string) (string, error) {
	// Encode image as base64 data URL
	mimeType := mimeTypeFromExt(imageType)
	base64Image := base64.StdEncoding.EncodeToString(imageData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Image)

	// Build request
	reqBody := openaiRequest{
		Model: c.model,
		Messages: []openaiMessage{
			{
				Role: "user",
				Content: []openaiContentPart{
					{
						Type: "text",
						Text: imagePrompt,
					},
					{
						Type: "image_url",
						ImageURL: &openaiImageURL{
							URL: dataURL,
						},
					},
				},
			},
		},
		MaxTokens: openaiMaxTokens,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal OpenAI request: %w", err)
	}

	// Send request
	req, err := http.NewRequest(http.MethodPost, openaiAPIURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("create OpenAI request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenAI API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read OpenAI response: %w", err)
	}

	// Parse response
	var openaiResp openaiResponse
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return "", fmt.Errorf("parse OpenAI response: %w", err)
	}

	// Check for API error
	if openaiResp.Error != nil {
		return "", fmt.Errorf("OpenAI API error (%s): %s", openaiResp.Error.Type, openaiResp.Error.Message)
	}

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return openaiResp.Choices[0].Message.Content, nil
}

// mimeTypeFromExt returns the MIME type for common image extensions.
func mimeTypeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg" // default fallback
	}
}
