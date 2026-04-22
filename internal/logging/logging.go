package logging

import (
	"aibooks/internal/config"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
)

type Cleanup func(ctx context.Context) error

func Setup(cfg config.Config, appName string) (Cleanup, error) {
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return nil, err
	}

	logFilePath := filepath.Join(cfg.LogDir, appName+".log")
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	writers := []io.Writer{os.Stdout, logFile}
	var kafkaWriter *kafka.Writer
	if len(cfg.LogKafkaBrokers) > 0 {
		kafkaWriter = &kafka.Writer{
			Addr:         kafka.TCP(cfg.LogKafkaBrokers...),
			Topic:        cfg.LogKafkaTopic,
			RequiredAcks: kafka.RequireOne,
			Async:        true,
			Balancer:     &kafka.LeastBytes{},
			BatchTimeout: 250 * time.Millisecond,
		}
		writers = append(writers, &kafkaLogWriter{
			appName: appName,
			topic:   cfg.LogKafkaTopic,
			writer:  kafkaWriter,
		})
	}

	log.SetOutput(io.MultiWriter(writers...))

	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	log.Printf("logging initialized app=%s file=%s kafka_enabled=%t kafka_topic=%s", appName, logFilePath, kafkaWriter != nil, cfg.LogKafkaTopic)

	return func(ctx context.Context) error {
		var firstErr error
		if kafkaWriter != nil {
			if err := kafkaWriter.Close(); err != nil {
				firstErr = err
			}
		}
		if err := logFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}, nil
}

type kafkaLogWriter struct {
	appName string
	topic   string
	writer  *kafka.Writer
	mu      sync.Mutex
}

type kafkaLogRecord struct {
	Timestamp string `json:"timestamp"`
	App       string `json:"app"`
	Message   string `json:"message"`
}

func (w *kafkaLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	record := kafkaLogRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		App:       w.appName,
		Message:   msg,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return len(p), nil
	}

	w.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	err = w.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(w.appName),
		Value: payload,
		Time:  time.Now().UTC(),
	})
	cancel()
	w.mu.Unlock()

	if err != nil {
		return len(p), nil
	}
	return len(p), nil
}
