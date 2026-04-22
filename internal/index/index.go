package index

import (
	"aibooks/internal/chunking"
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/util"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
)

type EmbeddingProvider interface {
	Model() string
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
}

type Service struct {
	cfg      config.Config
	store    *db.Store
	chunker  *chunking.Chunker
	embedder EmbeddingProvider
}

func NewService(cfg config.Config, pool *pgxpool.Pool, embedder EmbeddingProvider) (*Service, error) {
	ch, err := chunking.NewChunker(cfg.ChunkTokens, cfg.ChunkOverlap)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:      cfg,
		store:    db.NewStore(pool),
		chunker:  ch,
		embedder: embedder,
	}, nil
}

func (s *Service) Close() {
	s.store.Close()
}

func (s *Service) IndexBook(ctx context.Context, bookID string) error {
	if bookID == "" {
		return fmt.Errorf("bookID is required")
	}
	start := time.Now()
	log.Printf("IndexBook: book_id=%s started", bookID)

	pages, err := s.store.GetOCRPages(ctx, bookID)
	if err != nil {
		return fmt.Errorf("load ocr_pages: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Errorf("no OCR pages for book_id=%s", bookID)
	}

	chunks, err := s.chunker.Chunk(pages)
	if err != nil {
		return fmt.Errorf("chunking: %w", err)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks created for book_id=%s", bookID)
	}
	log.Printf("IndexBook: book_id=%s ocr_pages=%d chunks=%d", bookID, len(pages), len(chunks))

	type chunkToEmbed struct {
		chunkID    string
		text       string
		pageStart  int
		pageEnd    int
		chunkIndex int
	}
	var (
		chunkIDs []string
		toEmbed  []chunkToEmbed
	)

	for _, ch := range chunks {
		id, err := s.store.UpsertChunk(ctx, db.ChunkInput{
			BookID:     bookID,
			PageStart:  ch.PageStart,
			PageEnd:    ch.PageEnd,
			ChunkIndex: ch.ChunkIndex,
			Text:       ch.Text,
			TokenCount: ch.TokenCount,
		})
		if err != nil {
			return fmt.Errorf("upsert chunk: %w", err)
		}
		chunkIDs = append(chunkIDs, id)

		// проверяем, что чанк уже имеет эмбединг
		ok, err := s.store.HasChunkEmbedding(ctx, id, s.embedder.Model())
		if err != nil {
			return fmt.Errorf("check chunk embedding exists: %w", err)
		}
		if ok {
			continue
		}
		// если чанк уже имеет эмбединг, пропускаем его
		toEmbed = append(toEmbed, chunkToEmbed{
			chunkID:    id,
			text:       ch.Text,
			pageStart:  ch.PageStart,
			pageEnd:    ch.PageEnd,
			chunkIndex: ch.ChunkIndex,
		})
	}

	if len(toEmbed) == 0 {
		return nil
	}
	texts := make([]string, 0, len(toEmbed))
	totalChars := 0
	for _, ce := range toEmbed {
		texts = append(texts, ce.text)
		totalChars += len(ce.text)
	}
	log.Printf("IndexBook: book_id=%s embedding missing_chunks=%d", bookID, len(toEmbed))
	estTokens := util.EstimateTokensFromChars(totalChars)
	estCost := util.EstimateCostRUB(estTokens, s.cfg.EmbeddingCostPer1MTokensRUB)
	log.Printf("IndexBook: book_id=%s est_embeddings_tokens=%d est_embeddings_cost_rub=%.6f",
		bookID, estTokens, estCost)
	embeddings, err := s.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		log.Printf("IndexBook: book_id=%s embedding failed: %v", bookID, err)
		return fmt.Errorf("embed texts: %w", err)
	}
	if len(embeddings) != len(toEmbed) {
		return fmt.Errorf("embed count mismatch: got=%d want=%d", len(embeddings), len(toEmbed))
	}

	for i, ce := range toEmbed {
		if err := s.store.UpsertChunkEmbedding(ctx, ce.chunkID, s.embedder.Model(), embeddings[i]); err != nil {
			log.Printf("IndexBook: book_id=%s upsert embedding failed chunk_id=%s pages=%d-%d chunk_index=%d err=%v",
				bookID, ce.chunkID, ce.pageStart, ce.pageEnd, ce.chunkIndex, err)
			return fmt.Errorf("upsert chunk embedding (chunk_id=%s): %w", ce.chunkID, err)
		}
	}

	log.Printf("IndexBook: book_id=%s completed in=%s", bookID, time.Since(start))
	return nil
}
