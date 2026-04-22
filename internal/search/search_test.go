package search

import (
	"aibooks/internal/config"
	"aibooks/internal/db"
	"context"
	"strings"
	"testing"
)

type fakeEmbedder struct {
	embedding []float32
}

func (f fakeEmbedder) Model() string { return "fake-embed" }

func (f fakeEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for range texts {
		out = append(out, f.embedding)
	}
	return out, nil
}

type fakeLLM struct {
	lastUserPrompt string
	lastSystem     string
	answer         string
}

func (f *fakeLLM) Model() string { return "fake-llm" }

func (f *fakeLLM) Generate(ctx context.Context, systemPrompt, userPrompt string, maxOutputTokens int) (string, error) {
	f.lastSystem = systemPrompt
	f.lastUserPrompt = userPrompt
	return f.answer, nil
}

type fakeChunkSearcher struct {
	hits []db.SearchChunkRow
}

func (f fakeChunkSearcher) SearchChunksByVector(ctx context.Context, ownerUserID string, queryEmbedding []float32, limit int) ([]db.SearchChunkRow, error) {
	if limit <= 0 {
		return []db.SearchChunkRow{}, nil
	}
	if len(f.hits) < limit {
		return f.hits, nil
	}
	return f.hits[:limit], nil
}

func TestSearchBooks_TopBooksAndExcerptsLimit(t *testing.T) {
	cfg := config.Config{
		SearchTopKChunks:       50,
		SearchTopBooks:         2,
		SearchExcerptsMaxChars: 900,
		LLMMaxOutputTokens:     256,
	}

	var hits []db.SearchChunkRow
	for i := 1; i <= 6; i++ {
		hits = append(hits, db.SearchChunkRow{
			BookID:     "bookB",
			Title:      "BookB",
			Author:     "AuthorB",
			ChunkID:    "b" + strings.Repeat("x", i),
			PageStart:  10 + i,
			PageEnd:    10 + i,
			ChunkIndex: i,
			Text:       "excerptB_" + string(rune('0'+i)),
			Distance:   0.1,
		})
	}
	for i := 1; i <= 2; i++ {
		hits = append(hits, db.SearchChunkRow{
			BookID:     "bookA",
			Title:      "BookA",
			Author:     "AuthorA",
			ChunkID:    "a" + string(rune('0'+i)),
			PageStart:  1 + i,
			PageEnd:    1 + i,
			ChunkIndex: i,
			Text:       "excerptA_" + string(rune('0'+i)),
			Distance:   0.2,
		})
	}
	hits = append(hits, db.SearchChunkRow{
		BookID:     "bookC",
		Title:      "BookC",
		Author:     "AuthorC",
		ChunkID:    "c1",
		PageStart:  99,
		PageEnd:    99,
		ChunkIndex: 1,
		Text:       "excerptC_1",
		Distance:   0.3,
	})

	fEmbed := fakeEmbedder{embedding: make([]float32, 3072)}
	fStore := fakeChunkSearcher{hits: hits}
	fLLM := &fakeLLM{answer: "OK"}

	svc := NewService(cfg, fStore, fEmbed, fLLM)
	got, err := svc.SearchBooks(context.Background(), "user-1", "ветряные мельницы")
	if err != nil {
		t.Fatalf("SearchBooks failed: %v", err)
	}
	if got != "OK" {
		t.Fatalf("expected answer OK, got %q", got)
	}

	if !strings.Contains(fLLM.lastUserPrompt, "Книга #1: BookB") {
		t.Fatalf("expected BookB as #1, userPrompt=%q", fLLM.lastUserPrompt)
	}
	if !strings.Contains(fLLM.lastUserPrompt, "Книга #2: BookA") {
		t.Fatalf("expected BookA as #2, userPrompt=%q", fLLM.lastUserPrompt)
	}
	if strings.Contains(fLLM.lastUserPrompt, "BookC") {
		t.Fatalf("did not expect BookC in prompt, userPrompt=%q", fLLM.lastUserPrompt)
	}

	count := strings.Count(fLLM.lastUserPrompt, "- стр.")
	if count != 7 {
		t.Fatalf("expected 7 excerpt lines, got %d; prompt=%q", count, fLLM.lastUserPrompt)
	}
}

func TestSearchBooks_EmptyHits(t *testing.T) {
	cfg := config.Config{
		SearchTopKChunks:       10,
		SearchTopBooks:         2,
		SearchExcerptsMaxChars: 900,
		LLMMaxOutputTokens:     128,
	}
	fEmbed := fakeEmbedder{embedding: make([]float32, 3072)}
	fStore := fakeChunkSearcher{hits: nil}
	fLLM := &fakeLLM{answer: "SHOULD_NOT_CALL"}

	svc := NewService(cfg, fStore, fEmbed, fLLM)
	got, err := svc.SearchBooks(context.Background(), "user-1", "что-то")
	if err != nil {
		t.Fatalf("SearchBooks failed: %v", err)
	}
	if got == "" {
		t.Fatalf("expected non-empty message")
	}
	if strings.Contains(got, "SHOULD_NOT_CALL") {
		t.Fatalf("LLM should not be called when no hits; got=%q", got)
	}
}
