package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net"
	"os/signal"
	"syscall"
	"time"

	"aibooks/internal/auth"
	"aibooks/internal/config"
	"aibooks/internal/db"
	"aibooks/internal/handlers"
	"aibooks/internal/logging"
	"aibooks/internal/providers/factory"
	"aibooks/internal/search"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v4/pgxpool"
)

func main() {
	var addr = flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()
	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cleanupLogs, err := logging.Setup(cfg, "aibooks-server")
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
	if cfg.DBURL == "" {
		log.Fatal("missing AIBOOKS_DB_URL")
	}

	dbConnectCtx, cancelDBConnect := context.WithTimeout(rootCtx, 15*time.Second)
	defer cancelDBConnect()
	pool, err := pgxpool.Connect(dbConnectCtx, cfg.DBURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}

	store := db.NewStore(pool)
	defer store.Close()

	embedder, err := factory.NewEmbeddingProvider(cfg)
	if err != nil {
		log.Fatal(err)
	}
	llmProvider, err := factory.NewLLMProvider(cfg)
	if err != nil {
		log.Fatal(err)
	}

	searchSvc := search.NewService(cfg, store, embedder, llmProvider)
	authSvc, err := auth.NewService(cfg.AuthSecret, time.Duration(cfg.AuthTokenTTLMin)*time.Minute)
	if err != nil {
		log.Fatal(err)
	}

	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, DB: cfg.RedisDB}
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://127.0.0.1:5173", "http://localhost:5173"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	httpHandlers := handlers.NewHTTP(cfg, store, asynqClient, searchSvc, authSvc)
	httpHandlers.RegisterRoutes(r)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           r,
		BaseContext: func(_ net.Listener) context.Context {
			return rootCtx
		},
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("aibooks-server listening on %s", *addr)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErrCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server run error: %v", err)
		}
	case <-rootCtx.Done():
		log.Printf("shutdown signal received, stopping server")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(rootCtx, 20*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
}
