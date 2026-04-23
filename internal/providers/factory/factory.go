package factory

import (
	"aibooks/internal/config"
	"aibooks/internal/providers/embedding"
	"aibooks/internal/providers/llm"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

type EmbeddingProvider interface {
	Model() string
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
}

var (
	embeddingProviders = map[string]func(config.Config) (EmbeddingProvider, error){}
	llmProviders       = map[string]func(config.Config) (llm.LLMProvider, error){}

	registryMu     sync.RWMutex
	registryFrozen atomic.Bool
)

func requireKey(key, envVar, scope string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("%s: missing env var %s", scope, envVar)
	}
	return nil
}

func RegisterEmbeddingProvider(name string, fn func(config.Config) (EmbeddingProvider, error)) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if registryFrozen.Load() {
		return fmt.Errorf("embedding provider registry is frozen; registration is not allowed at runtime")
	}
	if name == "" {
		return fmt.Errorf("embedding provider name is empty")
	}
	if fn == nil {
		return fmt.Errorf("embedding provider factory fn is nil (name=%q)", name)
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := embeddingProviders[name]; exists {
		return fmt.Errorf("embedding provider %q already registered", name)
	}
	embeddingProviders[name] = fn
	return nil
}

func RegisterLLMProvider(name string, fn func(config.Config) (llm.LLMProvider, error)) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if registryFrozen.Load() {
		return fmt.Errorf("llm provider registry is frozen; registration is not allowed at runtime")
	}
	if name == "" {
		return fmt.Errorf("llm provider name is empty")
	}
	if fn == nil {
		return fmt.Errorf("llm provider factory fn is nil (name=%q)", name)
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := llmProviders[name]; exists {
		return fmt.Errorf("llm provider %q already registered", name)
	}
	llmProviders[name] = fn
	return nil
}

func init() {
	if err := RegisterEmbeddingProvider("gigachat", func(c config.Config) (EmbeddingProvider, error) {
		if err := requireKey(c.GigaChatAuthKey, "AIBOOKS_GIGACHAT_AUTH_KEY", "gigachat embeddings provider"); err != nil {
			return nil, err
		}
		return embedding.NewGigaChatProvider(embedding.GigaChatEmbeddingOptions{
			AuthKey:              c.GigaChatAuthKey,
			Scope:                c.GigaChatScope,
			OAuthURL:             c.GigaChatOAuthURL,
			BaseURL:              c.GigaChatBaseURL,
			Model:                c.EmbeddingModel,
			BatchSize:            c.EmbeddingBatchSz,
			MinRequestIntervalMs: c.EmbeddingMinRequestIntervalMs,
			ExpectedDimension:    c.EmbeddingDimension,
			InsecureSkipVerify:   c.GigaChatInsecureSkipVerify,
		}), nil
	}); err != nil {
		panic(err)
	}

	if err := RegisterLLMProvider("deepseek", func(c config.Config) (llm.LLMProvider, error) {
		if err := requireKey(c.DeepSeekAPIKey, "AIBOOKS_DEEPSEEK_API_KEY", "deepseek llm provider"); err != nil {
			return nil, err
		}
		return llm.NewDeepSeekProvider(llm.DeepSeekLLMOptions{
			APIKey:               c.DeepSeekAPIKey,
			BaseURL:              c.DeepSeekBaseURL,
			Model:                c.LLMModel,
			MinRequestIntervalMs: c.LLMMinRequestIntervalMs,
		}), nil
	}); err != nil {
		panic(err)
	}
}

func NewEmbeddingProvider(cfg config.Config) (EmbeddingProvider, error) {
	registryFrozen.Store(true)
	provider := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProviderType))
	if provider == "" {
		provider = "gigachat"
	}

	registryMu.RLock()
	fn, ok := embeddingProviders[provider]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported embedding provider: %q", provider)
	}
	return fn(cfg)
}

func NewLLMProvider(cfg config.Config) (llm.LLMProvider, error) {
	registryFrozen.Store(true)
	provider := strings.ToLower(strings.TrimSpace(cfg.LLMProviderType))
	if provider == "" {
		provider = "deepseek"
	}

	registryMu.RLock()
	fn, ok := llmProviders[provider]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported llm provider: %q", provider)
	}
	return fn(cfg)
}
