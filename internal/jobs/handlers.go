package jobs

import (
	"aibooks/internal/config"
	"aibooks/internal/index"
	"aibooks/internal/ingest"
	"aibooks/internal/summarize"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TaskOCRBook       = "ocr:book"
	TaskIndexBook     = "index:book"
	TaskSummarizeBook = "summarize:book"
)

type OCRBookPayload struct {
	UserID  string `json:"user_id"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	PDFPath string `json:"pdf_path"`
}

type IndexBookPayload struct {
	BookID string `json:"book_id"`
}

type SummarizeBookPayload struct {
	BookID string `json:"book_id"`
}

type Handler struct {
	cfg          config.Config
	asynqClient  *asynq.Client
	ingestSvc    *ingest.Service
	indexSvc     *index.Service
	summarizeSvc *summarize.Service
}

func NewHandler(cfg config.Config, asynqClient *asynq.Client, ingestSvc *ingest.Service, indexSvc *index.Service, summarizeSvc *summarize.Service) *Handler {
	return &Handler{
		cfg:          cfg,
		asynqClient:  asynqClient,
		ingestSvc:    ingestSvc,
		indexSvc:     indexSvc,
		summarizeSvc: summarizeSvc,
	}
}

func (h *Handler) HandleOCRBook(ctx context.Context, t *asynq.Task) error {
	var p OCRBookPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal ocr payload: %w", err)
	}
	log.Printf("job ocr:book started pdf=%s", p.PDFPath)

	bookID, err := h.ingestSvc.IngestPDF(ctx, p.UserID, p.Title, p.Author, p.PDFPath)
	if err != nil {
		log.Printf("job ocr:book failed pdf=%s err=%v", p.PDFPath, err)
		return err
	}

	indexPayload := IndexBookPayload{BookID: bookID}
	b, _ := json.Marshal(indexPayload)
	task := asynq.NewTask(TaskIndexBook, b)

	taskID := fmt.Sprintf("index:%s:%s", bookID, h.cfg.EmbeddingModel)
	_, err = h.asynqClient.Enqueue(
		task,
		asynq.TaskID(taskID),
		asynq.MaxRetry(10),
		asynq.Timeout(2*time.Hour),
	)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			log.Printf("job ocr:book index enqueue skipped (already exists) book_id=%s task_id=%s", bookID, taskID)
			log.Printf("job ocr:book completed book_id=%s", bookID)
			return nil
		}
		return fmt.Errorf("enqueue index job: %w", err)
	}

	log.Printf("job ocr:book completed book_id=%s", bookID)
	return nil
}

func (h *Handler) HandleIndexBook(ctx context.Context, t *asynq.Task) error {
	var p IndexBookPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal index payload: %w", err)
	}
	log.Printf("job index:book started book_id=%s", p.BookID)

	if err := h.indexSvc.IndexBook(ctx, p.BookID); err != nil {
		log.Printf("job index:book failed book_id=%s err=%v", p.BookID, err)
		return err
	}

	sumPayload := SummarizeBookPayload{BookID: p.BookID}
	b, _ := json.Marshal(sumPayload)
	task := asynq.NewTask(TaskSummarizeBook, b)

	taskID := fmt.Sprintf("summarize:%s:%s:%s", p.BookID, h.cfg.LLMModel, h.cfg.SummaryPromptVersion)
	_, err := h.asynqClient.Enqueue(
		task,
		asynq.TaskID(taskID),
		asynq.MaxRetry(10),
		asynq.Timeout(2*time.Hour),
	)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			log.Printf("job index:book summarize enqueue skipped (already exists) book_id=%s task_id=%s", p.BookID, taskID)
			log.Printf("job index:book completed book_id=%s", p.BookID)
			return nil
		}
		return fmt.Errorf("enqueue summarize job: %w", err)
	}

	log.Printf("job index:book completed book_id=%s", p.BookID)
	return nil
}

func (h *Handler) HandleSummarizeBook(ctx context.Context, t *asynq.Task) error {
	var p SummarizeBookPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal summarize payload: %w", err)
	}
	log.Printf("job summarize:book started book_id=%s", p.BookID)

	if err := h.summarizeSvc.SummarizeBook(ctx, p.BookID); err != nil {
		log.Printf("job summarize:book failed book_id=%s err=%v", p.BookID, err)
		return err
	}

	log.Printf("job summarize:book completed book_id=%s", p.BookID)
	return nil
}
