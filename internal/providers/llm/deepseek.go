package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type DeepSeekLLMOptions struct {
	APIKey               string
	BaseURL              string
	Model                string
	MinRequestIntervalMs int
}

type DeepSeekProvider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client

	minInterval time.Duration
	mu          sync.Mutex
	lastCall    time.Time
}

func NewDeepSeekProvider(opts DeepSeekLLMOptions) *DeepSeekProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &DeepSeekProvider{
		apiKey:      opts.APIKey,
		baseURL:     baseURL,
		model:       opts.Model,
		httpClient:  &http.Client{Timeout: 120 * time.Second},
		minInterval: time.Duration(opts.MinRequestIntervalMs) * time.Millisecond,
	}
}

func (p *DeepSeekProvider) Model() string { return p.model }

func (p *DeepSeekProvider) throttle() {
	if p.minInterval <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if !p.lastCall.IsZero() {
		if elapsed := now.Sub(p.lastCall); elapsed < p.minInterval {
			time.Sleep(p.minInterval - elapsed)
		}
	}
	p.lastCall = time.Now()
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (p *DeepSeekProvider) Generate(ctx context.Context, systemPrompt, userPrompt string, maxOutputTokens int) (string, error) {
	var msgs []chatMessage
	if systemPrompt != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: systemPrompt})
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: userPrompt})

	payload := openAIChatRequest{
		Model:       p.model,
		Messages:    msgs,
		MaxTokens:   maxOutputTokens,
		Temperature: 0.2,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal deepseek chat request: %w", err)
	}

	endpoints := []string{
		p.baseURL + "/v1/chat/completions",
		p.baseURL + "/chat/completions",
	}

	var lastErr error
	for _, url := range endpoints {
		p.throttle()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("deepseek chat failed: status=%d body=%s", resp.StatusCode, string(respBody))
			continue
		}

		var parsed openAIChatResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return "", fmt.Errorf("unmarshal deepseek chat response: %w (body=%s)", err, string(respBody))
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("deepseek chat response has no choices")
		}
		content := strings.TrimSpace(parsed.Choices[0].Message.Content)
		if content == "" {
			return "", fmt.Errorf("deepseek chat response has empty content")
		}
		return content, nil
	}

	return "", lastErr
}
