package search

import (
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/providers/llm"
	"aibooks/internal/util"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type EmbeddingProvider interface {
	Model() string
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
}

type Service struct {
	cfg      config.Config
	store    ChunkSearcher
	embedder EmbeddingProvider
	llm      llm.LLMProvider

	queryCacheMu         sync.Mutex
	queryCache           map[string][]float32
	queryCacheMaxEntries int
}

type ChunkSearcher interface {
	SearchChunksByVector(ctx context.Context, ownerUserID string, queryEmbedding []float32, limit int) ([]db.SearchChunkRow, error)
}

func NewService(cfg config.Config, store ChunkSearcher, embedder EmbeddingProvider, llmProvider llm.LLMProvider) *Service {
	return &Service{
		cfg:                  cfg,
		store:                store,
		embedder:             embedder,
		llm:                  llmProvider,
		queryCache:           make(map[string][]float32),
		queryCacheMaxEntries: 128,
	}
}

type BookAgg struct {
	BookID   string
	Title    string
	Author   string
	BestDist float64
	Excerpts []db.SearchChunkRow
}

func truncateByChars(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	s = strings.TrimSpace(s)
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "..."
}

var (
	wsMulti  = regexp.MustCompile(`[ \t]{2,}`)
	nlMulti  = regexp.MustCompile(`\n{3,}`)
	hyphenNL = regexp.MustCompile(`([\p{L}])-+\n+([\p{L}])`)
)

func normalizeExcerptText(in string) string {
	in = strings.ReplaceAll(in, "\u00ad", "")
	in = strings.ReplaceAll(in, "\r", "\n")
	in = hyphenNL.ReplaceAllString(in, "${1}${2}")
	in = nlMulti.ReplaceAllString(in, "\n\n")
	in = wsMulti.ReplaceAllString(in, " ")
	in = strings.TrimSpace(in)
	return in
}

func (s *Service) SearchBooks(ctx context.Context, ownerUserID, query string) (string, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return "", fmt.Errorf("owner user id is empty")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is empty")
	}

	k := s.cfg.SearchTopKChunks
	if k <= 0 {
		k = 40
	}
	nb := s.cfg.SearchTopBooks
	if nb <= 0 {
		nb = 3
	}

	log.Printf("SearchBooks: query=%q", query)

	qhash := sha256.Sum256([]byte(strings.ToLower(query)))
	cacheKey := hex.EncodeToString(qhash[:])
	s.queryCacheMu.Lock()
	if v, ok := s.queryCache[cacheKey]; ok && len(v) > 0 {
		s.queryCacheMu.Unlock()
		chunks, err := s.store.SearchChunksByVector(ctx, ownerUserID, v, k)
		if err != nil {
			return "", fmt.Errorf("vector search: %w", err)
		}
		return s.searchWithChunks(ctx, query, chunks, nb, k)
	}
	s.queryCacheMu.Unlock()

	embedRes, err := s.embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return "", fmt.Errorf("embed query: %w", err)
	}
	if len(embedRes) != 1 || len(embedRes[0]) == 0 {
		return "", fmt.Errorf("unexpected embedding result")
	}
	if len(embedRes[0]) > 0 {
		s.queryCacheMu.Lock()
		// если кэш полон, очищаем его
		if len(s.queryCache) >= s.queryCacheMaxEntries {
			s.queryCache = make(map[string][]float32)
		}
		s.queryCache[cacheKey] = embedRes[0]
		s.queryCacheMu.Unlock()
	}

	chunks, err := s.store.SearchChunksByVector(ctx, ownerUserID, embedRes[0], k)
	if err != nil {
		return "", fmt.Errorf("vector search: %w", err)
	}
	return s.searchWithChunks(ctx, query, chunks, nb, k)
}

func (s *Service) searchWithChunks(ctx context.Context, query string, chunks []db.SearchChunkRow, nb int, k int) (string, error) {
	if len(chunks) == 0 {
		return "Похоже, по вашему запросу ничего не найдено.", nil
	}
	start := time.Now()
	log.Printf("SearchBooks: query=%q vector_hits=%d", query, len(chunks))

	byBookAggs := aggregateBooks(chunks)
	if nb > 0 && len(byBookAggs) > nb {
		byBookAggs = byBookAggs[:nb]
	}

	excerptsText := buildExcerptsText(byBookAggs, s.cfg.SearchExcerptsMaxChars)
	systemPrompt, userPrompt := buildSearchPrompts(query, excerptsText)

	inTokens := util.EstimateTokensFromChars(len(systemPrompt) + len(userPrompt))
	outTokens := s.cfg.LLMMaxOutputTokens
	inCost := util.EstimateCostRUB(inTokens, s.cfg.LLMInputCostPer1MTokensRUB)
	outCost := util.EstimateCostRUB(outTokens, s.cfg.LLMOutputCostPer1MTokensRUB)
	log.Printf("SearchBooks: query=%q est_llm_tokens_in=%d est_llm_tokens_out=%d est_llm_cost_rub=%.6f", query, inTokens, outTokens, inCost+outCost)

	answer, err := s.generateSearchAnswer(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("llm generate: %w", err)
	}

	log.Printf("SearchBooks: query=%q completed in=%s (topBooks=%d topK=%d)", query, time.Since(start), len(byBookAggs), k)
	return strings.TrimSpace(answer), nil
}

func aggregateBooks(chunks []db.SearchChunkRow) []*BookAgg {
	hitsByBook := make(map[string][]db.SearchChunkRow)
	bookMeta := make(map[string]*BookAgg)
	for _, ch := range chunks {
		hitsByBook[ch.BookID] = append(hitsByBook[ch.BookID], ch)
		if bookMeta[ch.BookID] == nil {
			bookMeta[ch.BookID] = &BookAgg{
				BookID:   ch.BookID,
				Title:    ch.Title,
				Author:   ch.Author,
				BestDist: ch.Distance,
			}
		} else if ch.Distance < bookMeta[ch.BookID].BestDist {
			bookMeta[ch.BookID].BestDist = ch.Distance
		}
	}

	const maxExcerptsPerBook = 5
	byBookAggs := make([]*BookAgg, 0, len(hitsByBook))
	for bookID, hits := range hitsByBook {
		agg := bookMeta[bookID]
		if agg == nil {
			continue
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].Distance < hits[j].Distance })
		seen := make(map[string]struct{}, len(hits))
		excerpts := make([]db.SearchChunkRow, 0, maxExcerptsPerBook)
		for _, h := range hits {
			if len(excerpts) >= maxExcerptsPerBook {
				break
			}
			norm := normalizeExcerptText(h.Text)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			h.Text = norm
			excerpts = append(excerpts, h)
		}
		agg.Excerpts = excerpts
		byBookAggs = append(byBookAggs, agg)
	}

	sort.Slice(byBookAggs, func(i, j int) bool { return byBookAggs[i].BestDist < byBookAggs[j].BestDist })
	return byBookAggs
}

func buildExcerptsText(byBookAggs []*BookAgg, maxTotalChars int) string {
	if maxTotalChars <= 0 {
		maxTotalChars = 6000
	}

	usedChars := 0
	excerptRef := 0
	var excerpts strings.Builder
	for i, agg := range byBookAggs {
		fmt.Fprintf(&excerpts, "Книга #%d: %s%s\n", i+1, agg.Title, formatAuthor(agg.Author))
		for _, ex := range agg.Excerpts {
			if usedChars >= maxTotalChars {
				break
			}
			excerptRef++
			ref := fmt.Sprintf("E%d", excerptRef)
			perExcerptBudget := 900
			remain := maxTotalChars - usedChars
			if remain < perExcerptBudget {
				perExcerptBudget = remain
			}
			frag := truncateByChars(ex.Text, perExcerptBudget)
			usedChars += len(frag)
			fmt.Fprintf(&excerpts, "- стр. %d-%d (чанк %d) [%s]: %s\n", ex.PageStart, ex.PageEnd, ex.ChunkIndex, ref, frag)
		}
		excerpts.WriteString("\n")
	}
	return excerpts.String()
}

func formatAuthor(author string) string {
	if strings.TrimSpace(author) == "" {
		return ""
	}
	return " — " + author
}

func buildSearchPrompts(query, excerpts string) (string, string) {
	systemPrompt := "Ты ассистент библиотеки. Отвечай только на основании предоставленных фрагментов. Не выдумывай. Если недостаточно информации — прямо скажи об этом и не делай предположений."
	userPrompt := fmt.Sprintf(
		"Запрос пользователя:\n%s\n\nФрагменты (используй ссылки вида [E#]):\n%s\n\nТребования к ответу:\n1) Выбери 1-3 наиболее подходящие книги.\n2) Почему они подходят: строго ссылайся на фрагменты [E#].\n3) Если данных недостаточно, скажи: \"Недостаточно данных по фрагментам\" и укажи, какие фрагменты нужны.\n\nФормат ответа:\n- Книга: <title>\n- Почему: <коротко>\n- Фрагменты: <E#,...>",
		query,
		excerpts,
	)
	return systemPrompt, userPrompt
}

func (s *Service) generateSearchAnswer(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	llmCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		llmCtx, cancel = context.WithTimeout(ctx, 60*time.Second)
	}
	if cancel != nil {
		defer cancel()
	}
	return s.llm.Generate(llmCtx, systemPrompt, userPrompt, s.cfg.LLMMaxOutputTokens)
}
