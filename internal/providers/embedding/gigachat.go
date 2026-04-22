package embedding

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type GigaChatEmbeddingOptions struct {
	AuthKey              string
	Scope                string
	OAuthURL             string
	BaseURL              string
	Model                string
	BatchSize            int
	MinRequestIntervalMs int
	ExpectedDimension    int
	InsecureSkipVerify   bool
}

type GigaChatProvider struct {
	authKey   string
	scope     string
	oauthURL  string
	baseURL   string
	model     string
	batchSize int

	httpClient *http.Client

	expectedDim int
	minInterval time.Duration
	mu          sync.Mutex
	lastCall    time.Time

	tokenMu      sync.Mutex
	accessToken  string
	tokenExpires time.Time

	maxInputTokens int
}

func NewGigaChatProvider(opts GigaChatEmbeddingOptions) *GigaChatProvider {
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 16
	}
	scope := strings.TrimSpace(opts.Scope)
	if scope == "" {
		scope = "GIGACHAT_API_PERS"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://gigachat.devices.sberbank.ru/api/v1"
	}
	oauthURL := strings.TrimSpace(opts.OAuthURL)
	if oauthURL == "" {
		oauthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = "Embeddings"
	}

	return &GigaChatProvider{
		authKey:   strings.TrimSpace(opts.AuthKey),
		scope:     scope,
		oauthURL:  oauthURL,
		baseURL:   baseURL,
		model:     model,
		batchSize: batchSize,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: opts.InsecureSkipVerify}, //nolint:gosec
			},
		},
		expectedDim:    opts.ExpectedDimension,
		minInterval:    time.Duration(opts.MinRequestIntervalMs) * time.Millisecond,
		maxInputTokens: 500,
	}
}

func (p *GigaChatProvider) Model() string { return p.model }

func (p *GigaChatProvider) throttle() {
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

func (p *GigaChatProvider) normalizeDim(values []float32) []float32 {
	if p.expectedDim <= 0 {
		return values
	}
	if len(values) == p.expectedDim {
		return values
	}
	if len(values) > p.expectedDim {
		return values[:p.expectedDim]
	}
	out := make([]float32, p.expectedDim)
	copy(out, values)
	return out
}

type gigachatOAuthResp struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

func (p *GigaChatProvider) ensureAccessToken(ctx context.Context) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	if p.accessToken != "" && time.Until(p.tokenExpires) > 30*time.Second {
		return p.accessToken, nil
	}

	form := "scope=" + url.QueryEscape(p.scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.oauthURL, strings.NewReader(form))
	if err != nil {
		return "", err
	}
	authHeader := p.authKey
	if !strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
		authHeader = "Basic " + authHeader
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("RqUID", uuid.NewString())

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gigachat oauth failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var parsed gigachatOAuthResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal gigachat oauth response: %w (body=%s)", err, string(body))
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return "", fmt.Errorf("gigachat oauth response has empty access_token")
	}
	p.accessToken = parsed.AccessToken
	if parsed.ExpiresAt > 0 {
		p.tokenExpires = time.UnixMilli(parsed.ExpiresAt)
	} else {
		p.tokenExpires = time.Now().Add(25 * time.Minute)
	}
	return p.accessToken, nil
}

type gigachatEmbeddingRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

type gigachatEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (p *GigaChatProvider) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	token, err := p.ensureAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	var out [][]float32
	for _, t := range texts {
		embed, err := p.embedWithAdaptiveTruncation(ctx, token, t)
		if err != nil {
			return nil, err
		}
		out = append(out, embed)
	}
	return out, nil
}

func (p *GigaChatProvider) truncateApproxTokens(s string) string {
	if p.maxInputTokens <= 0 {
		return s
	}
	parts := strings.Fields(s)
	if len(parts) <= p.maxInputTokens {
		return s
	}
	return strings.Join(parts[:p.maxInputTokens], " ")
}

func (p *GigaChatProvider) truncateByWords(s string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	parts := strings.Fields(s)
	if len(parts) <= maxWords {
		return s
	}
	return strings.Join(parts[:maxWords], " ")
}

func isGigaChatTokenLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status=413") && strings.Contains(msg, "Tokens limit exceeded")
}

func (p *GigaChatProvider) embedWithAdaptiveTruncation(ctx context.Context, token, text string) ([]float32, error) {
	trimmed := p.truncateApproxTokens(text)
	words := len(strings.Fields(trimmed))
	if words == 0 {
		words = 1
	}

	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		embeds, err := p.embedBatch(ctx, token, []string{trimmed})
		if err == nil {
			return embeds[0], nil
		}
		lastErr = err
		if !isGigaChatTokenLimitErr(err) {
			return nil, err
		}

		if words <= 32 {
			break
		}
		words /= 2
		trimmed = p.truncateByWords(trimmed, words)
	}
	if lastErr == nil {
		lastErr = errors.New("unknown embedding error")
	}
	return nil, fmt.Errorf("embed with adaptive truncation failed: %w", lastErr)
}

func (p *GigaChatProvider) embedBatch(ctx context.Context, token string, texts []string) ([][]float32, error) {
	p.throttle()
	payload := gigachatEmbeddingRequest{
		Model: p.model,
		Input: texts,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal gigachat embeddings request: %w", err)
	}

	url := p.baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gigachat embeddings failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var parsed gigachatEmbeddingResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal gigachat embeddings response: %w (body=%s)", err, string(respBody))
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("gigachat embeddings count mismatch: got=%d want=%d", len(parsed.Data), len(texts))
	}
	out := make([][]float32, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		out = append(out, p.normalizeDim(d.Embedding))
	}
	return out, nil
}
