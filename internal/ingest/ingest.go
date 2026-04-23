package ingest

import (
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/ocr"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
)

type Service struct {
	cfg    config.Config
	store  *db.Store
	ocrEng *ocr.Engine
}

func NewService(cfg config.Config, pool *pgxpool.Pool) *Service {
	return &Service{
		cfg:    cfg,
		store:  db.NewStore(pool),
		ocrEng: ocr.NewEngine(cfg.OCRLang, cfg.PDFDPI),
	}
}

func (s *Service) Close() {
	s.store.Close()
}

type ocrJob struct {
	pageNumber int
	imgPath    string
}

type ocrResult struct {
	pageNumber int
	text       string
	ok         bool
}

// проводит OCR для всех страниц и записывает результаты в `ocr_pages`
func (s *Service) IngestPDF(ctx context.Context, userID, title, author, inputPDFPath string) (bookID string, err error) {
	if err := validateIngestInput(userID, title, inputPDFPath); err != nil {
		return "", err
	}

	tmpFile, err := s.prepareTempUploadPath()
	if err != nil {
		return "", err
	}
	defer func() {
		removeFileBestEffort(tmpFile)
	}()

	bookID, storedPDFPath, err := s.upsertBookAndStorePDF(ctx, userID, title, author, inputPDFPath, tmpFile)
	if err != nil {
		return "", err
	}

	paths, cleanupOCRDir, err := s.prepareOCRImages(ctx, bookID, storedPDFPath)
	if err != nil {
		return "", err
	}
	defer cleanupOCRDir()

	jobs, err := s.buildOCRJobs(ctx, bookID, paths)
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		log.Printf("OCR: book_id=%s nothing to do (all pages exist)", bookID)
		return bookID, nil
	}

	if err := s.processOCRJobs(ctx, bookID, jobs); err != nil {
		return "", err
	}
	return bookID, nil
}

func validateIngestInput(userID, title, inputPDFPath string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("userID is required")
	}
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if _, err := os.Stat(inputPDFPath); err != nil {
		return fmt.Errorf("pdf does not exist: %s: %w", inputPDFPath, err)
	}
	return nil
}

func (s *Service) prepareTempUploadPath() (string, error) {
	// создаем директории для хранения данных
	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.cfg.TempDir, 0o755); err != nil {
		return "", err
	}
	tmpDir := filepath.Join(s.cfg.TempDir, "ingest", "pdf")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	// создаем временный файл для хранения загруженного PDF
	return filepath.Join(tmpDir, fmt.Sprintf("upload-%d.pdf", time.Now().UnixNano())), nil
}

func (s *Service) upsertBookAndStorePDF(ctx context.Context, userID, title, author, inputPDFPath, tmpFile string) (string, string, error) {
	// копируем PDF файл в временную директорию и считаем чексуму
	sourceChecksum, err := copyFileAndChecksumHex(inputPDFPath, tmpFile)
	if err != nil {
		return "", "", fmt.Errorf("copy+checksum: %w", err)
	}

	// создаем книгу в базе данных
	bookID, err := s.store.CreateBook(ctx, db.BookInput{
		OwnerUserID:    userID,
		Title:          title,
		Author:         author,
		SourcePath:     tmpFile,
		SourceChecksum: sourceChecksum,
	})
	if err != nil {
		return "", "", fmt.Errorf("create book: %w", err)
	}

	// сохраняем PDF файл в директорию книги
	storedPDFPath, err := s.persistBookPDF(tmpFile, bookID)
	if err != nil {
		return "", "", err
	}
	if _, err := s.store.UpdateBookSourcePath(ctx, bookID, storedPDFPath); err != nil {
		return "", "", fmt.Errorf("update book source_path: %w", err)
	}
	return bookID, storedPDFPath, nil
}

func (s *Service) persistBookPDF(tmpFile, bookID string) (string, error) {
	// создаем директорию для книги
	bookDir := filepath.Join(s.cfg.DataDir, "books", bookID)
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		return "", err
	}
	// создаем путь для сохраненного PDF
	storedPDFPath := filepath.Join(bookDir, "original.pdf")

	if _, statErr := os.Stat(storedPDFPath); statErr == nil {
		removeFileBestEffort(tmpFile)
		return storedPDFPath, nil
	}
	if err := os.Rename(tmpFile, storedPDFPath); err != nil {
		if copyErr := copyFile(tmpFile, storedPDFPath); copyErr != nil {
			if _, statErr2 := os.Stat(storedPDFPath); statErr2 == nil {
				removeFileBestEffort(tmpFile)
			} else {
				return "", fmt.Errorf("move tmp pdf: rename failed=%v, copy fallback failed=%w", err, copyErr)
			}
		}
		removeFileBestEffort(tmpFile)
	}
	return storedPDFPath, nil
}

func (s *Service) prepareOCRImages(ctx context.Context, bookID, storedPDFPath string) ([]string, func(), error) {
	// проверяем контекст на ошибки
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// создаем директорию для хранения изображений OCR
	tmpDir := filepath.Join(s.cfg.TempDir, "ocr", bookID)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, nil, err
	}

	// функция для очистки директории
	cleanup := func() { removeAllBestEffort(tmpDir) }

	imagesDir := filepath.Join(tmpDir, "images")

	// конвертируем PDF в изображения
	paths, err := s.ocrEng.ConvertPDFToPNGs(storedPDFPath, imagesDir)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	// проверяем контекст на ошибки
	if err := ctx.Err(); err != nil {
		cleanup()
		return nil, nil, err
	}

	log.Printf("OCR: book_id=%s pages=%d", bookID, len(paths))

	// возвращаем изображения, функцию для очистки и ошибку
	return paths, cleanup, nil
}

func (s *Service) buildOCRJobs(ctx context.Context, bookID string, paths []string) ([]ocrJob, error) {
	var (
		existing map[int]struct{}
		err      error
	)

	if !s.cfg.ForceOCR {
		existing, err = s.store.GetExistingOCRPageNumbers(ctx, bookID)
		if err != nil {
			return nil, err
		}
	}

	// создаем задачи для OCR
	jobs := make([]ocrJob, 0, len(paths))
	for idx, imgPath := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		pageNumber := idx + 1
		if !s.cfg.ForceOCR {
			if _, ok := existing[pageNumber]; ok {
				removeFileBestEffort(imgPath)
				continue
			}
		}

		jobs = append(jobs, ocrJob{pageNumber: pageNumber, imgPath: imgPath})
	}
	return jobs, nil
}

func (s *Service) processOCRJobs(ctx context.Context, bookID string, jobs []ocrJob) error {
	ocrCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	workers := s.cfg.OCRConcurrency
	if workers <= 0 {
		workers = 1
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	// устанавливаем размер пакета для сохранения в базу данных
	batchSize := s.cfg.OCRDBBatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	// создаем каналы для задач и результатов
	jobsCh := make(chan ocrJob)
	resCh := make(chan ocrResult, workers*2)
	errSignalCh := make(chan struct{}, 1)

	// создаем переменные для ошибок
	var workerErr error
	var errMu sync.Mutex
	var errOnce sync.Once

	// функция для установки ошибки воркера
	setWorkerErr := func(err error) {
		errMu.Lock()
		if workerErr == nil {
			workerErr = err
		}
		errMu.Unlock()
	}

	// функция для получения ошибки воркера
	getWorkerErr := func() error {
		errMu.Lock()
		defer errMu.Unlock()
		return workerErr
	}

	// функция для выполнения задачи OCR

	workerFn := func() {
		for j := range jobsCh {
			if ocrCtx.Err() != nil {
				return
			}

			res, err := s.runOCRJob(ocrCtx, bookID, j)
			if err != nil {
				errOnce.Do(func() {
					setWorkerErr(err)
					errSignalCh <- struct{}{}
					cancel()
				})
				return
			}

			// отправляем результат в канал
			select {
			case resCh <- res:
			case <-ocrCtx.Done():
				return
			}
		}
	}

	// создаем группу для ожидания завершения воркеров
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			workerFn()
		}()
	}

	go func() {
		defer close(jobsCh)
		for _, j := range jobs {
			select {
			case jobsCh <- j:
			case <-ocrCtx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resCh)
	}()

	failures := 0
	batch := make([]db.OCRPageInput, 0, batchSize)

	// функция для очистки пакета в базе данных
	flush := func() error {
		// если пакет пустой, возвращаем nil
		if len(batch) == 0 {
			return nil
		}
		// если forceOCR включен, обновляем страницы в базе данных
		if s.cfg.ForceOCR {
			if err := s.store.BatchUpsertOCRPages(ocrCtx, bookID, s.cfg.OCRLang, batch); err != nil {
				return err
			}
		} else {
			// если forceOCR выключен, вставляем страницы в базу данных
			if err := s.store.BatchInsertOCRPagesIfMissing(ocrCtx, bookID, s.cfg.OCRLang, batch); err != nil {
				return err
			}
		}
		// очищаем пакет
		batch = batch[:0]
		return nil
	}

	// читаем результаты из канала
	for res := range resCh {
		select {
		case <-errSignalCh:
			if err := getWorkerErr(); err != nil {
				return err
			}
		default:
		}

		// если результат не ок, увеличивкем счетчик ошибок
		if !res.ok {
			failures++
			continue
		}

		batch = append(batch, db.OCRPageInput{PageNumber: res.pageNumber, Text: res.text})
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := getWorkerErr(); err != nil {
		return err
	}
	if err := ocrCtx.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if failures > 0 {
		log.Printf("OCR: book_id=%s failures=%d mode=%s", bookID, failures, s.cfg.OCRErrorMode)
	}
	return nil
}

func (s *Service) runOCRJob(ctx context.Context, bookID string, j ocrJob) (ocrResult, error) {
	var (
		text    string
		lastErr error
	)

	// пытаемся выполнить задачу OCR несколько раз
	for attempt := 1; attempt <= s.cfg.OCRMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ocrResult{}, ctx.Err()
		}
		t, ocrErr := s.ocrEng.OCRImageText(j.imgPath)
		if ocrErr == nil {
			text = t
			lastErr = nil
			break
		}
		lastErr = ocrErr
		log.Printf("OCR error: book_id=%s page=%d attempt=%d image=%s err=%v", bookID, j.pageNumber, attempt, j.imgPath, ocrErr)
		select {
		case <-ctx.Done():
			return ocrResult{}, ctx.Err()
		case <-time.After(time.Duration(attempt*attempt) * 200 * time.Millisecond):
		}
	}

	removeFileBestEffort(j.imgPath)

	if lastErr != nil {
		if s.cfg.OCRErrorMode == "skip" {
			return ocrResult{pageNumber: j.pageNumber, ok: false}, nil
		}
		return ocrResult{}, fmt.Errorf("ocr page %d failed (image=%s): %w", j.pageNumber, j.imgPath, lastErr)
	}

	// если ошибки нет, возвращаем результат с текстом
	return ocrResult{pageNumber: j.pageNumber, text: text, ok: true}, nil
}

func copyFileAndChecksumHex(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = out.Close()
	}()

	h := sha256.New()
	w := io.MultiWriter(out, h)
	if _, err := io.Copy(w, in); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func removeFileBestEffort(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("cleanup warning: remove file path=%s err=%v", path, err)
	}
}

func removeAllBestEffort(path string) {
	if path == "" {
		return
	}
	if err := os.RemoveAll(path); err != nil {
		log.Printf("cleanup warning: remove dir path=%s err=%v", path, err)
	}
}
