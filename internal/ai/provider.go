package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

type Provider interface {
	Name() string
	Chat(ctx context.Context, systemPrompt string, messages []ChatMessage) (string, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type ProviderConfig struct {
	Provider       string // "ollama", "openai", "gemini"
	BaseURL        string
	APIKey         string
	Model          string
	EmbeddingModel string
}

func NewProvider(cfg ProviderConfig) Provider {
	client := &http.Client{Timeout: 90 * time.Second}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))

	switch provider {
	case "ollama":
		url := strings.TrimRight(cfg.BaseURL, "/")
		if url == "" {
			url = "http://localhost:11434"
		}
		model := cfg.Model
		if model == "" {
			model = "llama3.2"
		}
		embModel := cfg.EmbeddingModel
		if embModel == "" {
			embModel = "nomic-embed-text"
		}
		return &OllamaProvider{
			client:         client,
			baseURL:        url,
			model:          model,
			embeddingModel: embModel,
		}

	case "gemini":
		model := cfg.Model
		if model == "" {
			model = "gemini-1.5-flash"
		}
		embModel := cfg.EmbeddingModel
		if embModel == "" {
			embModel = "text-embedding-004"
		}
		return &GeminiProvider{
			client:         client,
			apiKey:         cfg.APIKey,
			model:          model,
			embeddingModel: embModel,
		}

	case "openai":
		url := strings.TrimRight(cfg.BaseURL, "/")
		if url == "" {
			url = "https://api.openai.com/v1"
		}
		model := cfg.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		embModel := cfg.EmbeddingModel
		if embModel == "" {
			embModel = "text-embedding-3-small"
		}
		return &OpenAIProvider{
			client:         client,
			baseURL:        url,
			apiKey:         cfg.APIKey,
			model:          model,
			embeddingModel: embModel,
		}

	default:
		return &DisabledProvider{reason: "AI provider is not configured"}
	}
}

// ---------------------------------------------------------------------
// Ollama Provider
// ---------------------------------------------------------------------
type OllamaProvider struct {
	client         *http.Client
	baseURL        string
	model          string
	embeddingModel string
}

func (p *OllamaProvider) Name() string { return "Ollama (" + p.model + ")" }

func (p *OllamaProvider) Chat(ctx context.Context, systemPrompt string, messages []ChatMessage) (string, error) {
	var ollamaMsgs []map[string]string
	if systemPrompt != "" {
		ollamaMsgs = append(ollamaMsgs, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	for _, m := range messages {
		ollamaMsgs = append(ollamaMsgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	reqBody := map[string]interface{}{
		"model":    p.model,
		"messages": ollamaMsgs,
		"stream":   false,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Ollama at %s: %w", p.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var res struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Message.Content), nil
}

func (p *OllamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	// First try modern /api/embed (Ollama >= 0.1.34)
	reqBody := map[string]interface{}{
		"model": p.embeddingModel,
		"input": texts,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/embed", bytes.NewReader(bodyBytes))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var res struct {
					Embeddings [][]float32 `json:"embeddings"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && len(res.Embeddings) == len(texts) {
					return res.Embeddings, nil
				}
			}
		}
	}

	// Fallback to legacy /api/embeddings per text
	result := make([][]float32, len(texts))
	for i, text := range texts {
		singleBody := map[string]interface{}{
			"model":  p.embeddingModel,
			"prompt": text,
		}
		singleBytes, _ := json.Marshal(singleBody)
		req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/embeddings", bytes.NewReader(singleBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Ollama embeddings returned status %d", resp.StatusCode)
		}

		var res struct {
			Embedding []float32 `json:"embedding"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return nil, err
		}
		result[i] = res.Embedding
	}

	return result, nil
}

// ---------------------------------------------------------------------
// OpenAI-Compatible Provider
// ---------------------------------------------------------------------
type OpenAIProvider struct {
	client         *http.Client
	baseURL        string
	apiKey         string
	model          string
	embeddingModel string
}

func (p *OpenAIProvider) Name() string { return "OpenAI-compatible (" + p.model + ")" }

func (p *OpenAIProvider) Chat(ctx context.Context, systemPrompt string, messages []ChatMessage) (string, error) {
	var openAIMsgs []map[string]string
	if systemPrompt != "" {
		openAIMsgs = append(openAIMsgs, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}
	for _, m := range messages {
		openAIMsgs = append(openAIMsgs, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	reqBody := map[string]interface{}{
		"model":    p.model,
		"messages": openAIMsgs,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned from OpenAI")
	}
	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}

func (p *OpenAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := map[string]interface{}{
		"model": p.embeddingModel,
		"input": texts,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI embeddings returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var res struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	embeddings := make([][]float32, len(texts))
	for _, item := range res.Data {
		if item.Index >= 0 && item.Index < len(embeddings) {
			embeddings[item.Index] = item.Embedding
		}
	}
	return embeddings, nil
}

// ---------------------------------------------------------------------
// Google Gemini Provider
// ---------------------------------------------------------------------
type GeminiProvider struct {
	client         *http.Client
	apiKey         string
	model          string
	embeddingModel string
}

func (p *GeminiProvider) Name() string { return "Google Gemini (" + p.model + ")" }

func (p *GeminiProvider) Chat(ctx context.Context, systemPrompt string, messages []ChatMessage) (string, error) {
	type Part struct {
		Text string `json:"text"`
	}
	type Content struct {
		Role  string `json:"role"`
		Parts []Part `json:"parts"`
	}

	var contents []Content
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, Content{
			Role:  role,
			Parts: []Part{{Text: m.Content}},
		})
	}

	reqBody := map[string]interface{}{
		"contents": contents,
	}
	if systemPrompt != "" {
		reqBody["systemInstruction"] = map[string]interface{}{
			"parts": []Part{{Text: systemPrompt}},
		}
	}

	bodyBytes, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", p.model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}
	return strings.TrimSpace(res.Candidates[0].Content.Parts[0].Text), nil
}

func (p *GeminiProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		reqBody := map[string]interface{}{
			"model": "models/" + p.embeddingModel,
			"content": map[string]interface{}{
				"parts": []map[string]string{{"text": text}},
			},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s", p.embeddingModel, p.apiKey)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Gemini embedding returned status %d", resp.StatusCode)
		}

		var res struct {
			Embedding struct {
				Values []float32 `json:"values"`
			} `json:"embedding"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return nil, err
		}
		result[i] = res.Embedding.Values
	}
	return result, nil
}

// ---------------------------------------------------------------------
// Disabled / Mock Provider
// ---------------------------------------------------------------------
type DisabledProvider struct {
	reason string
}

func (p *DisabledProvider) Name() string { return "Disabled" }

func (p *DisabledProvider) Chat(ctx context.Context, systemPrompt string, messages []ChatMessage) (string, error) {
	return "", fmt.Errorf("AI is currently disabled or unconfigured (%s). Configure PAPERLESS_AI_PROVIDER to 'ollama', 'openai', or 'gemini'", p.reason)
}

func (p *DisabledProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("AI embeddings are disabled or unconfigured (%s)", p.reason)
}

