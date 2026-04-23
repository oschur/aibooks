package main

import (
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/index"
	"aibooks/internal/ingest"
	"aibooks/internal/jobs"
	"aibooks/internal/logging"
	"aibooks/internal/providers/factory"
	"aibooks/internal/summarize"
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v4/pgxpool"
)

func main() {
	concurrency := flag.Int("concurrency", 4, "Worker concurrency")
	flag.Parse()
	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cleanupLogs, err := logging.Setup(cfg, "aibooks-worker")
	if err != nil {
		log.Fatalf("setup logging: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := cleanupLogs(closeCtx); err != nil {
			log.Printf("close logging: %v", err)
		}
	}()

	dbConnectCtx, cancelDBConnect := context.WithTimeout(rootCtx, 15*time.Second)
	defer cancelDBConnect()
	pool, err := pgxpool.Connect(dbConnectCtx, cfg.DBURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	store := db.NewStore(pool)
	embedder, err := factory.NewEmbeddingProvider(cfg)
	if err != nil {
		log.Fatal(err)
	}
	llmProvider, err := factory.NewLLMProvider(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ingestSvc := ingest.NewService(cfg, pool)
	indexSvc, err := index.NewService(cfg, pool, embedder)
	if err != nil {
		log.Fatal(err)
	}
	summarySvc := summarize.NewService(cfg, store, llmProvider)

	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: "",
		DB:       cfg.RedisDB,
	}

	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: *concurrency,
		},
	)

	mux := asynq.NewServeMux()

	h := jobs.NewHandler(cfg, asynqClient, ingestSvc, indexSvc, summarySvc)

	mux.Handle(jobs.TaskOCRBook, asynq.HandlerFunc(h.HandleOCRBook))
	mux.Handle(jobs.TaskIndexBook, asynq.HandlerFunc(h.HandleIndexBook))
	mux.Handle(jobs.TaskSummarizeBook, asynq.HandlerFunc(h.HandleSummarizeBook))

	log.Printf("asynq worker started redis=%s/%d concurrency=%d", cfg.RedisAddr, cfg.RedisDB, *concurrency)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("worker run error: %v", err)
	}

	<-rootCtx.Done()
	log.Printf("shutdown signal received, stopping worker")

	// Stop pulling new tasks, then wait for in-flight tasks to finish.
	srv.Stop()
	shutdownDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		log.Printf("worker stopped gracefully")
	case <-time.After(30 * time.Second):
		log.Printf("worker shutdown timeout reached")
	}
}
