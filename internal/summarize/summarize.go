package summarize

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/providers/llm"
	"aibooks/internal/util"
)

type Service struct {
	cfg   config.Config
	store *db.Store
	llm   llm.LLMProvider
}

func NewService(cfg config.Config, store *db.Store, llmProvider llm.LLMProvider) *Service {
	return &Service{
		cfg:   cfg,
		store: store,
		llm:   llmProvider,
	}
}

func (s *Service) SummarizeBook(ctx context.Context, bookID string) error {
	if bookID == "" {
		return fmt.Errorf("bookID is required")
	}
	start := time.Now()
	log.Printf("SummarizeBook: book_id=%s started", bookID)

	chunks, err := s.store.GetChunks(ctx, bookID)
	if err != nil {
		return fmt.Errorf("load chunks: %w", err)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks for book_id=%s", bookID)
	}
	log.Printf("SummarizeBook: book_id=%s chunks=%d map_max=%d", bookID, len(chunks), s.cfg.SummaryMapMaxChunks)

	maxMap := s.cfg.SummaryMapMaxChunks
	if maxMap <= 0 || maxMap > len(chunks) {
		maxMap = len(chunks)
	}
	selected := chunks[:maxMap]
	mapInputChars := 0
	for _, ch := range selected {
		mapInputChars += len(ch.Text)
	}
	mapInputTokens := util.EstimateTokensFromChars(mapInputChars)
	mapOutputTokens := maxMap * s.cfg.LLMMaxOutputTokens
	mapCost := util.EstimateCostRUB(mapInputTokens, s.cfg.LLMInputCostPer1MTokensRUB) + util.EstimateCostRUB(mapOutputTokens, s.cfg.LLMOutputCostPer1MTokensRUB)
	log.Printf("SummarizeBook: book_id=%s est_map_tokens_in=%d est_map_tokens_out=%d est_map_cost_usd=%.6f", bookID, mapInputTokens, mapOutputTokens, mapCost)

	mapSummaries := make([]string, 0, maxMap)
	systemMap := "Ты ассистент для суммаризации книг. Пиши по-русски. Точность важнее красивых формулировок."
	for i := 0; i < maxMap; i++ {
		ch := chunks[i]
		userMap := fmt.Sprintf(
			"Сделай краткое резюме фрагмента книги (страницы %d-%d).\n\nФРАГМЕНТ:\n%s\n\nТребования:\n- 4-8 предложений\n- выдели главные тезисы\n- упомяни ключевые понятия/термины\n- если в фрагменте есть аргументы, кратко перескажи их логикой",
			ch.PageStart, ch.PageEnd, ch.Text,
		)

		out, err := s.llm.Generate(ctx, systemMap, userMap, s.cfg.LLMMaxOutputTokens)
		if err != nil {
			log.Printf("SummarizeBook: book_id=%s llm map failed chunk_index=%d pages=%d-%d err=%v",
				bookID, ch.ChunkIndex, ch.PageStart, ch.PageEnd, err)
			return fmt.Errorf("map summarize chunk_index=%d: %w", ch.ChunkIndex, err)
		}
		mapSummaries = append(mapSummaries, strings.TrimSpace(out))
	}

	systemReduce := "Ты ассистент для итоговой суммаризации книги. Пиши по-русски."
	reduceInput := strings.Join(mapSummaries, "\n\n---\n\n")
	reduceInputTokens := util.EstimateTokensFromChars(len(reduceInput))
	reduceOutputTokens := s.cfg.LLMMaxOutputTokens
	reduceCost := util.EstimateCostRUB(reduceInputTokens, s.cfg.LLMInputCostPer1MTokensRUB) + util.EstimateCostRUB(reduceOutputTokens, s.cfg.LLMOutputCostPer1MTokensRUB)
	log.Printf("SummarizeBook: book_id=%s est_reduce_tokens_in=%d est_reduce_tokens_out=%d est_reduce_cost_usd=%.6f", bookID, reduceInputTokens, reduceOutputTokens, reduceCost)
	userReduce := fmt.Sprintf(
		"На основе резюме фрагментов сформируй цельный обзор всей книги.\n\nРЕЗЮМЕ ФРАГМЕНТОВ:\n%s\n\nСделай:\n1) 2-3 абзаца общего содержания\n2) список из 5-8 ключевых идей/тезисов\n3) коротко объясни, для кого/в каких контекстах книга будет полезна",
		reduceInput,
	)

	bookSummary, err := s.llm.Generate(ctx, systemReduce, userReduce, s.cfg.LLMMaxOutputTokens)
	if err != nil {
		return fmt.Errorf("reduce summarize: %w", err)
	}

	if err := s.store.UpsertBookSummary(ctx, bookID, s.llm.Model(), s.cfg.SummaryPromptVersion, strings.TrimSpace(bookSummary)); err != nil {
		return fmt.Errorf("upsert book summary: %w", err)
	}
	log.Printf("SummarizeBook: book_id=%s completed in=%s", bookID, time.Since(start))
	return nil
}
