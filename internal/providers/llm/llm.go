package llm

import "context"

type LLMProvider interface {
	Model() string
	Generate(ctx context.Context, systemPrompt, userPrompt string, maxOutputTokens int) (string, error)
}
