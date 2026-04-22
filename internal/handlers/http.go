package handlers

import (
	"aibooks/internal/auth"
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/jobs"
	"aibooks/internal/search"
	"aibooks/internal/upload"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v4"
)

type HTTP struct {
	cfg       config.Config
	store     *db.Store
	asynq     *asynq.Client
	searchSvc *search.Service
	authSvc   *auth.Service
}

type uploadResponse struct {
	JobID string `json:"job_id"`
	Info  string `json:"info"`
}

type searchRequest struct {
	Q string `json:"q"`
}

type searchResponse struct {
	Answer string `json:"answer"`
}

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

type bookSummaryResponse struct {
	BookID        string `json:"book_id"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	LLMModel      string `json:"llm_model"`
	PromptVersion string `json:"prompt_version"`
	Summary       string `json:"summary"`
}

type listBooksResponse struct {
	Books []db.BookListItem `json:"books"`
}

func NewHTTP(cfg config.Config, store *db.Store, asynqClient *asynq.Client, searchSvc *search.Service, authSvc *auth.Service) *HTTP {
	return &HTTP{
		cfg:       cfg,
		store:     store,
		asynq:     asynqClient,
		searchSvc: searchSvc,
		authSvc:   authSvc,
	}
}

func (h *HTTP) RegisterRoutes(r chi.Router) {
	r.Get("/healthz", h.handleHealthz)

	r.Group(func(api chi.Router) {
		api.Use(rateLimitMiddleware(h.cfg.HTTPRateLimitMaxRequests, h.cfg.HTTPRateLimitWindowSec))

		api.Group(func(auth chi.Router) {
			auth.Use(rateLimitMiddleware(h.cfg.HTTPAuthRateLimitMaxRequests, h.cfg.HTTPAuthRateLimitWindowSec))
			auth.Post("/auth/register", h.handleRegister)
			auth.Post("/auth/login", h.handleLogin)
		})

		api.Group(func(private chi.Router) {
			private.Use(h.authSvc.RequireAuth)
			private.Get("/books", h.handleListBooks)
			private.Post("/books/upload", h.handleUpload)
			private.Post("/search", h.handleSearch)
			private.Delete("/books/{bookID}", h.handleDeleteBook)
			private.Get("/books/{bookID}/summary", h.handleGetBookSummary)
		})
	})
}

func rateLimitMiddleware(maxRequests, windowSec int) func(http.Handler) http.Handler {
	if maxRequests <= 0 || windowSec <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	window := time.Duration(windowSec) * time.Second
	return httprate.Limit(maxRequests, window,
		httprate.WithKeyByRealIP(),
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many requests"})
		}),
	)
}

func (h *HTTP) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *HTTP) handleUpload(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, fmt.Errorf("parse multipart: %w", err).Error(), http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	author := r.FormValue("author")
	if strings.TrimSpace(title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Errorf("missing form file field 'file': %w", err).Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := os.MkdirAll(h.cfg.TempDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	uploadDir := filepath.Join(h.cfg.TempDir, "server-upload")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tempPath, checksum, err := upload.SaveUploadedFileAndChecksum(file, uploadDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payload := jobs.OCRBookPayload{
		UserID:  userID,
		Title:   title,
		Author:  author,
		PDFPath: tempPath,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, fmt.Errorf("marshal job payload: %w", err).Error(), http.StatusInternalServerError)
		return
	}

	task := asynq.NewTask(jobs.TaskOCRBook, b)
	taskID := "ocr:" + userID + ":" + checksum

	_, err = h.asynq.Enqueue(
		task,
		asynq.TaskID(taskID),
		asynq.MaxRetry(10),
		asynq.Timeout(3*time.Hour),
	)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			writeJSON(w, http.StatusAccepted, uploadResponse{
				JobID: taskID,
				Info:  "OCR job already enqueued",
			})
			return
		}
		http.Error(w, fmt.Errorf("enqueue failed: %w", err).Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusAccepted, uploadResponse{
		JobID: taskID,
		Info:  "OCR job enqueued",
	})
}

func (h *HTTP) handleListBooks(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	books, err := h.store.ListBooksByOwner(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listBooksResponse{Books: books})
}

func (h *HTTP) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	ans, err := h.searchSvc.SearchBooks(r.Context(), userID, req.Q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, searchResponse{Answer: ans})
}

func (h *HTTP) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := strings.TrimSpace(chi.URLParam(r, "bookID"))
	if bookID == "" {
		http.Error(w, "missing book id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteBookAndFilesystem(r.Context(), h.cfg.DataDir, bookID, userID); err != nil {
		if err == db.ErrForbidden {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) handleGetBookSummary(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := strings.TrimSpace(chi.URLParam(r, "bookID"))
	if bookID == "" {
		http.Error(w, "missing book id", http.StatusBadRequest)
		return
	}
	summary, err := h.store.GetBookSummary(r.Context(), bookID, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			http.Error(w, "summary not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, bookSummaryResponse{
		BookID:        summary.BookID,
		Title:         summary.Title,
		Author:        summary.Author,
		LLMModel:      summary.LLMModel,
		PromptVersion: summary.PromptVersion,
		Summary:       summary.SummaryText,
	})
}

func (h *HTTP) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	passwordHash, err := auth.HashPassword(strings.TrimSpace(req.Password))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID, err := h.store.CreateUser(r.Context(), strings.TrimSpace(req.Email), passwordHash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := h.authSvc.IssueToken(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{
		Token:  token,
		UserID: userID,
		Email:  strings.TrimSpace(req.Email),
	})
}

func (h *HTTP) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	u, err := h.store.GetUserByEmail(r.Context(), strings.TrimSpace(req.Email))
	if err != nil {
		if err == pgx.ErrNoRows {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := auth.CheckPassword(strings.TrimSpace(req.Password), u.PasswordHash); err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	token, err := h.authSvc.IssueToken(u.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, authResponse{
		Token:  token,
		UserID: u.ID,
		Email:  u.Email,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
