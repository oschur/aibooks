package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
)

var ErrForbidden = fmt.Errorf("forbidden")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

type BookInput struct {
	OwnerUserID    string
	Title          string
	Author         string
	SourcePath     string
	SourceChecksum string
}

type User struct {
	ID           string
	Email        string
	PasswordHash string
}

type OCRPage struct {
	PageNumber int
	Text       string
}

type ChunkInput struct {
	BookID     string
	PageStart  int
	PageEnd    int
	ChunkIndex int
	Text       string
	TokenCount int
}

type ChunkRow struct {
	ID         string
	PageStart  int
	PageEnd    int
	ChunkIndex int
	Text       string
}

type SearchChunkRow struct {
	BookID     string
	Title      string
	Author     string
	ChunkID    string
	PageStart  int
	PageEnd    int
	ChunkIndex int
	Text       string
	Distance   float64
}

type BookSummary struct {
	BookID        string
	Title         string
	Author        string
	LLMModel      string
	PromptVersion string
	SummaryText   string
}

type BookListItem struct {
	BookID    string `json:"book_id"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreateBook(ctx context.Context, in BookInput) (string, error) {
	if strings.TrimSpace(in.OwnerUserID) == "" {
		return "", fmt.Errorf("owner_user_id is required")
	}
	if in.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if in.SourcePath == "" {
		return "", fmt.Errorf("source_path is required")
	}
	if in.SourceChecksum == "" {
		return "", fmt.Errorf("source_checksum is required")
	}

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO books (owner_user_id, title, author, source_path, source_checksum)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (owner_user_id, source_checksum)
		DO UPDATE SET
		  title = EXCLUDED.title,
		  author = EXCLUDED.author,
		  source_path = EXCLUDED.source_path
		RETURNING id
	`, in.OwnerUserID, in.Title, in.Author, in.SourcePath, in.SourceChecksum).Scan(&id)
	return id, err
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (string, error) {
	if strings.TrimSpace(email) == "" {
		return "", fmt.Errorf("email is required")
	}
	if strings.TrimSpace(passwordHash) == "" {
		return "", fmt.Errorf("password hash is required")
	}
	var userID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id
	`, strings.ToLower(strings.TrimSpace(email)), passwordHash).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", fmt.Errorf("user already exists")
		}
		return "", err
	}
	return userID, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash
		FROM users
		WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email))).Scan(&u.ID, &u.Email, &u.PasswordHash)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) UpdateBookSourcePath(ctx context.Context, bookID, sourcePath string) (int64, error) {
	if bookID == "" {
		return 0, fmt.Errorf("bookID is required")
	}
	if sourcePath == "" {
		return 0, fmt.Errorf("sourcePath is required")
	}
	res, err := s.pool.Exec(ctx, `UPDATE books SET source_path = $2 WHERE id = $1`, bookID, sourcePath)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

func (s *Store) InsertOCRPageIfMissing(ctx context.Context, bookID string, pageNumber int, language, text string) error {
	sum := sha256.Sum256([]byte(text))
	checksum := hex.EncodeToString(sum[:])

	_, err := s.pool.Exec(ctx, `
		INSERT INTO ocr_pages (book_id, page_number, language, text, text_checksum)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (book_id, page_number) DO NOTHING
	`, bookID, pageNumber, language, text, checksum)
	return err
}

type OCRPageInput struct {
	PageNumber int
	Text       string
}

func (s *Store) BatchInsertOCRPagesIfMissing(ctx context.Context, bookID string, language string, pages []OCRPageInput) error {
	if len(pages) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, p := range pages {
		sum := sha256.Sum256([]byte(p.Text))
		checksum := hex.EncodeToString(sum[:])
		batch.Queue(`
			INSERT INTO ocr_pages (book_id, page_number, language, text, text_checksum)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (book_id, page_number) DO NOTHING
		`, bookID, p.PageNumber, language, p.Text, checksum)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range pages {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) BatchUpsertOCRPages(ctx context.Context, bookID string, language string, pages []OCRPageInput) error {
	if len(pages) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, p := range pages {
		sum := sha256.Sum256([]byte(p.Text))
		checksum := hex.EncodeToString(sum[:])
		batch.Queue(`
			INSERT INTO ocr_pages (book_id, page_number, language, text, text_checksum)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (book_id, page_number)
			DO UPDATE SET
			  language = EXCLUDED.language,
			  text = EXCLUDED.text,
			  text_checksum = EXCLUDED.text_checksum
		`, bookID, p.PageNumber, language, p.Text, checksum)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range pages {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetExistingOCRPageNumbers(ctx context.Context, bookID string) (map[int]struct{}, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT page_number FROM ocr_pages WHERE book_id = $1`,
		bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]struct{})
	for rows.Next() {
		var pn int
		if err := rows.Scan(&pn); err != nil {
			return nil, err
		}
		out[pn] = struct{}{}
	}
	return out, rows.Err()
}

func (s *Store) UpsertOCRPage(ctx context.Context, bookID string, pageNumber int, language, text string) error {
	sum := sha256.Sum256([]byte(text))
	checksum := hex.EncodeToString(sum[:])

	_, err := s.pool.Exec(ctx, `
		INSERT INTO ocr_pages (book_id, page_number, language, text, text_checksum)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (book_id, page_number)
		DO UPDATE SET
		  language = EXCLUDED.language,
		  text = EXCLUDED.text,
		  text_checksum = EXCLUDED.text_checksum
	`, bookID, pageNumber, language, text, checksum)
	return err
}

func (s *Store) GetOCRPages(ctx context.Context, bookID string) ([]OCRPage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT page_number, text FROM ocr_pages WHERE book_id = $1 ORDER BY page_number`,
		bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OCRPage
	for rows.Next() {
		var p OCRPage
		if err := rows.Scan(&p.PageNumber, &p.Text); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func vectorToPGString(v []float32) string {
	// приводим вектор к строке вида "[0.1,0.2,0.3]"
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(float64(x), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (s *Store) UpsertChunk(ctx context.Context, in ChunkInput) (string, error) {
	if in.BookID == "" {
		return "", fmt.Errorf("BookID is required")
	}
	if in.Text == "" {
		return "", fmt.Errorf("chunk text is empty")
	}

	sum := sha256.Sum256([]byte(in.Text))
	checksum := hex.EncodeToString(sum[:])

	var chunkID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chunks (book_id, page_start, page_end, chunk_index, text, token_count, text_checksum)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (book_id, page_start, page_end, chunk_index)
		DO UPDATE SET
		  text = EXCLUDED.text,
		  token_count = EXCLUDED.token_count,
		  text_checksum = EXCLUDED.text_checksum
		RETURNING id
	`, in.BookID, in.PageStart, in.PageEnd, in.ChunkIndex, in.Text, in.TokenCount, checksum).Scan(&chunkID)
	return chunkID, err
}

func (s *Store) UpsertChunkEmbedding(ctx context.Context, chunkID, embeddingModel string, embedding []float32) error {
	if chunkID == "" {
		return fmt.Errorf("chunkID is required")
	}
	if embeddingModel == "" {
		return fmt.Errorf("embeddingModel is required")
	}
	if len(embedding) == 0 {
		return fmt.Errorf("embedding is empty")
	}

	embedStr := vectorToPGString(embedding)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chunk_embeddings (chunk_id, embedding_model, embedding)
		VALUES ($1, $2, $3::vector)
		ON CONFLICT (chunk_id, embedding_model)
		DO UPDATE SET embedding = EXCLUDED.embedding
	`, chunkID, embeddingModel, embedStr)
	return err
}

func (s *Store) HasChunkEmbedding(ctx context.Context, chunkID, embeddingModel string) (bool, error) {
	if chunkID == "" {
		return false, fmt.Errorf("chunkID is required")
	}
	if embeddingModel == "" {
		return false, fmt.Errorf("embeddingModel is required")
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM chunk_embeddings
			WHERE chunk_id = $1 AND embedding_model = $2
		)
	`, chunkID, embeddingModel).Scan(&exists)
	return exists, err
}

func (s *Store) GetChunks(ctx context.Context, bookID string) ([]ChunkRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, page_start, page_end, chunk_index, text
		 FROM chunks
		 WHERE book_id = $1
		 ORDER BY page_start, chunk_index`,
		bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChunkRow
	for rows.Next() {
		var r ChunkRow
		if err := rows.Scan(&r.ID, &r.PageStart, &r.PageEnd, &r.ChunkIndex, &r.Text); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertBookSummary(ctx context.Context, bookID, llmModel, promptVersion, summaryText string) error {
	if bookID == "" {
		return fmt.Errorf("bookID is required")
	}
	if summaryText == "" {
		return fmt.Errorf("summaryText is empty")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO book_summaries (book_id, llm_model, prompt_version, summary_text)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (book_id, prompt_version)
		DO UPDATE SET
		  llm_model = EXCLUDED.llm_model,
		  summary_text = EXCLUDED.summary_text,
		  created_at = now()
	`, bookID, llmModel, promptVersion, summaryText)
	return err
}

func (s *Store) GetBookSummary(ctx context.Context, bookID, ownerUserID string) (BookSummary, error) {
	if strings.TrimSpace(bookID) == "" {
		return BookSummary{}, fmt.Errorf("bookID is required")
	}

	var out BookSummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			b.id,
			b.title,
			b.author,
			s.llm_model,
			s.prompt_version,
			s.summary_text
		FROM book_summaries s
		JOIN books b ON b.id = s.book_id
		WHERE s.book_id = $1
		  AND b.owner_user_id = $2
		ORDER BY s.created_at DESC
		LIMIT 1
	`, bookID, ownerUserID).Scan(
		&out.BookID,
		&out.Title,
		&out.Author,
		&out.LLMModel,
		&out.PromptVersion,
		&out.SummaryText,
	)
	if err != nil {
		return BookSummary{}, err
	}
	return out, nil
}

func (s *Store) ListBooksByOwner(ctx context.Context, ownerUserID string) ([]BookListItem, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return nil, fmt.Errorf("owner_user_id is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, author, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM books
		WHERE owner_user_id = $1
		ORDER BY created_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]BookListItem, 0)
	for rows.Next() {
		var b BookListItem
		if err := rows.Scan(&b.BookID, &b.Title, &b.Author, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) SearchChunksByVector(ctx context.Context, ownerUserID string, queryEmbedding []float32, limit int) ([]SearchChunkRow, error) {
	if limit <= 0 {
		return []SearchChunkRow{}, nil
	}
	if len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("queryEmbedding is empty")
	}

	embedStr := vectorToPGString(queryEmbedding)
	// выполняем запрос к базе данных для поиска похожих чанков
	rows, err := s.pool.Query(ctx, `
		SELECT
			b.id AS book_id,
			b.title,
			b.author,
			c.id AS chunk_id,
			c.page_start,
			c.page_end,
			c.chunk_index,
			c.text,
			(ce.embedding <=> $1::vector) AS distance
		FROM chunk_embeddings ce
		JOIN chunks c ON c.id = ce.chunk_id
		JOIN books b ON b.id = c.book_id
		WHERE b.owner_user_id = $2
		ORDER BY ce.embedding <=> $1::vector
		LIMIT $3
	`, embedStr, ownerUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchChunkRow
	// читаем результаты запроса
	for rows.Next() {
		var r SearchChunkRow
		if err := rows.Scan(&r.BookID, &r.Title, &r.Author, &r.ChunkID, &r.PageStart, &r.PageEnd, &r.ChunkIndex, &r.Text, &r.Distance); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBookAndFilesystem(ctx context.Context, dataDir, bookID, ownerUserID string) error {
	if strings.TrimSpace(bookID) == "" {
		return fmt.Errorf("bookID is required")
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM books WHERE id = $1 AND owner_user_id = $2`, bookID, ownerUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrForbidden
	}

	if strings.TrimSpace(dataDir) == "" {
		return nil
	}

	dir := filepath.Join(dataDir, "books", bookID)
	if err := os.RemoveAll(dir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
