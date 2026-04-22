package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DBURL    string
	OCRLang  string
	PDFDPI   int
	DataDir  string
	TempDir  string
	ForceOCR bool

	ChunkTokens  int
	ChunkOverlap int

	EmbeddingModel   string
	EmbeddingBatchSz int

	EmbeddingProviderType string

	DeepSeekAPIKey  string
	DeepSeekBaseURL string

	GigaChatAuthKey            string
	GigaChatScope              string
	GigaChatOAuthURL           string
	GigaChatBaseURL            string
	GigaChatInsecureSkipVerify bool

	LLMModel             string
	LLMMaxOutputTokens   int
	LLMProviderType      string
	SummaryPromptVersion string
	SummaryMapMaxChunks  int

	SearchTopKChunks       int
	SearchTopBooks         int
	SearchExcerptsMaxChars int
	SearchPromptVersion    string

	EmbeddingMinRequestIntervalMs int
	LLMMinRequestIntervalMs       int

	OCRConcurrency int
	OCRMaxAttempts int
	OCRErrorMode   string
	OCRDBBatchSize int

	RedisAddr string
	RedisDB   int

	EmbeddingDimension int

	EmbeddingCostPer1MTokensRUB float64
	LLMInputCostPer1MTokensRUB  float64
	LLMOutputCostPer1MTokensRUB float64

	AuthSecret      string
	AuthTokenTTLMin int

	HTTPRateLimitMaxRequests int
	HTTPRateLimitWindowSec   int

	HTTPAuthRateLimitMaxRequests int
	HTTPAuthRateLimitWindowSec   int

	LogDir          string
	LogKafkaBrokers []string
	LogKafkaTopic   string
}

func LoadFromEnv() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg := Config{
		OCRLang: "rus",
		PDFDPI:  300,
		DataDir: "data",
		TempDir: "tmp",

		ChunkTokens:  800,
		ChunkOverlap: 120,

		EmbeddingModel:   "Embeddings",
		EmbeddingBatchSz: 16,

		EmbeddingProviderType: "gigachat",

		DeepSeekBaseURL:    "https://api.deepseek.com",
		GigaChatScope:      "GIGACHAT_API_PERS",
		GigaChatOAuthURL:   "https://ngw.devices.sberbank.ru:9443/api/v2/oauth",
		GigaChatBaseURL:    "https://gigachat.devices.sberbank.ru/api/v1",
		EmbeddingDimension: 1536,

		LLMModel:             "deepseek-chat",
		LLMMaxOutputTokens:   2048,
		SummaryPromptVersion: "v1",
		SummaryMapMaxChunks:  20,

		LLMProviderType: "deepseek",

		SearchTopKChunks:       40,
		SearchTopBooks:         3,
		SearchExcerptsMaxChars: 6000,
		SearchPromptVersion:    "v1",

		EmbeddingMinRequestIntervalMs: 200,
		LLMMinRequestIntervalMs:       400,

		RedisAddr: "127.0.0.1:6379",
		RedisDB:   0,

		OCRConcurrency: 4,
		OCRMaxAttempts: 3,
		OCRErrorMode:   "fail",
		OCRDBBatchSize: 50,

		EmbeddingCostPer1MTokensRUB: 0,
		LLMInputCostPer1MTokensRUB:  0,
		LLMOutputCostPer1MTokensRUB: 0,
		AuthTokenTTLMin:             1440,

		HTTPRateLimitMaxRequests:     300,
		HTTPRateLimitWindowSec:       60,
		HTTPAuthRateLimitMaxRequests: 15,
		HTTPAuthRateLimitWindowSec:   60,

		LogDir:        "logs",
		LogKafkaTopic: "aibooks.logs",
	}

	if v := os.Getenv("AIBOOKS_DB_URL"); v != "" {
		cfg.DBURL = v
	}
	if v := os.Getenv("AIBOOKS_OCR_LANG"); v != "" {
		cfg.OCRLang = v
	}
	if v := os.Getenv("AIBOOKS_PDF_DPI"); v != "" {
		dpi, err := parseEnvInt(v)
		if err != nil || dpi <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_PDF_DPI: %q", v)
		}
		cfg.PDFDPI = dpi
	}
	if v := os.Getenv("AIBOOKS_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("AIBOOKS_TEMP_DIR"); v != "" {
		cfg.TempDir = v
	}
	if v := os.Getenv("AIBOOKS_FORCE_OCR"); v != "" {
		cfg.ForceOCR = true
	}

	if v := os.Getenv("AIBOOKS_CHUNK_TOKENS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_CHUNK_TOKENS: %q", v)
		}
		cfg.ChunkTokens = n
	}
	if v := os.Getenv("AIBOOKS_CHUNK_OVERLAP"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_CHUNK_OVERLAP: %q", v)
		}
		cfg.ChunkOverlap = n
	}

	if v := os.Getenv("AIBOOKS_DEEPSEEK_API_KEY"); v != "" {
		cfg.DeepSeekAPIKey = v
	}
	if v := os.Getenv("AIBOOKS_DEEPSEEK_BASE_URL"); v != "" {
		cfg.DeepSeekBaseURL = v
	}
	if v := os.Getenv("AIBOOKS_GIGACHAT_AUTH_KEY"); v != "" {
		cfg.GigaChatAuthKey = v
	}
	if v := os.Getenv("AIBOOKS_GIGACHAT_SCOPE"); v != "" {
		cfg.GigaChatScope = v
	}
	if v := os.Getenv("AIBOOKS_GIGACHAT_OAUTH_URL"); v != "" {
		cfg.GigaChatOAuthURL = v
	}
	if v := os.Getenv("AIBOOKS_GIGACHAT_BASE_URL"); v != "" {
		cfg.GigaChatBaseURL = v
	}
	if v := os.Getenv("AIBOOKS_GIGACHAT_INSECURE_SKIP_VERIFY"); v != "" {
		v = strings.TrimSpace(strings.ToLower(v))
		cfg.GigaChatInsecureSkipVerify = v == "1" || v == "true" || v == "yes"
	}
	if v := os.Getenv("AIBOOKS_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := os.Getenv("AIBOOKS_EMBEDDING_BATCH_SIZE"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_EMBEDDING_BATCH_SIZE: %q", v)
		}
		cfg.EmbeddingBatchSz = n
	}
	if v := os.Getenv("AIBOOKS_EMBEDDING_DIMENSION"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_EMBEDDING_DIMENSION: %q", v)
		}
		cfg.EmbeddingDimension = n
	}

	if v := os.Getenv("AIBOOKS_EMBEDDING_PROVIDER"); v != "" {
		cfg.EmbeddingProviderType = v
	}

	if v := os.Getenv("AIBOOKS_LLM_MODEL"); v != "" {
		cfg.LLMModel = v
	}
	if v := os.Getenv("AIBOOKS_LLM_PROVIDER"); v != "" {
		cfg.LLMProviderType = v
	}
	if v := os.Getenv("AIBOOKS_LLM_MAX_OUTPUT_TOKENS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_LLM_MAX_OUTPUT_TOKENS: %q", v)
		}
		cfg.LLMMaxOutputTokens = n
	}
	if v := os.Getenv("AIBOOKS_SUMMARY_PROMPT_VERSION"); v != "" {
		cfg.SummaryPromptVersion = v
	}
	if v := os.Getenv("AIBOOKS_SUMMARY_MAP_MAX_CHUNKS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_SUMMARY_MAP_MAX_CHUNKS: %q", v)
		}
		cfg.SummaryMapMaxChunks = n
	}

	if v := os.Getenv("AIBOOKS_SEARCH_TOP_K_CHUNKS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_SEARCH_TOP_K_CHUNKS: %q", v)
		}
		cfg.SearchTopKChunks = n
	}
	if v := os.Getenv("AIBOOKS_SEARCH_TOP_BOOKS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_SEARCH_TOP_BOOKS: %q", v)
		}
		cfg.SearchTopBooks = n
	}
	if v := os.Getenv("AIBOOKS_SEARCH_EXCERPTS_MAX_CHARS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_SEARCH_EXCERPTS_MAX_CHARS: %q", v)
		}
		cfg.SearchExcerptsMaxChars = n
	}
	if v := os.Getenv("AIBOOKS_SEARCH_PROMPT_VERSION"); v != "" {
		cfg.SearchPromptVersion = v
	}

	if v := os.Getenv("AIBOOKS_EMBEDDING_MIN_REQUEST_INTERVAL_MS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_EMBEDDING_MIN_REQUEST_INTERVAL_MS: %q", v)
		}
		cfg.EmbeddingMinRequestIntervalMs = n
	}
	if v := os.Getenv("AIBOOKS_LLM_MIN_REQUEST_INTERVAL_MS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_LLM_MIN_REQUEST_INTERVAL_MS: %q", v)
		}
		cfg.LLMMinRequestIntervalMs = n
	}

	if v := os.Getenv("AIBOOKS_REDIS_ADDR"); v != "" {
		cfg.RedisAddr = v
	}
	if v := os.Getenv("AIBOOKS_REDIS_DB"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_REDIS_DB: %q", v)
		}
		cfg.RedisDB = n
	}

	if v := os.Getenv("AIBOOKS_OCR_CONCURRENCY"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_OCR_CONCURRENCY: %q", v)
		}
		cfg.OCRConcurrency = n
	}
	if v := os.Getenv("AIBOOKS_OCR_MAX_ATTEMPTS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_OCR_MAX_ATTEMPTS: %q", v)
		}
		cfg.OCRMaxAttempts = n
	}
	if v := os.Getenv("AIBOOKS_OCR_ERROR_MODE"); v != "" {
		v = strings.TrimSpace(strings.ToLower(v))
		if v != "fail" && v != "skip" {
			return Config{}, fmt.Errorf("invalid AIBOOKS_OCR_ERROR_MODE: %q (expected fail|skip)", v)
		}
		cfg.OCRErrorMode = v
	}
	if v := os.Getenv("AIBOOKS_OCR_DB_BATCH_SIZE"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_OCR_DB_BATCH_SIZE: %q", v)
		}
		cfg.OCRDBBatchSize = n
	}

	if v := os.Getenv("AIBOOKS_EMBEDDING_COST_PER_1M_TOKENS_USD"); v != "" {
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AIBOOKS_EMBEDDING_COST_PER_1M_TOKENS_USD: %q", v)
		}
		cfg.EmbeddingCostPer1MTokensRUB = f
	}
	if v := os.Getenv("AIBOOKS_LLM_COST_PER_1M_INPUT_TOKENS_USD"); v != "" {
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AIBOOKS_LLM_COST_PER_1M_INPUT_TOKENS_USD: %q", v)
		}
		cfg.LLMInputCostPer1MTokensRUB = f
	}
	if v := os.Getenv("AIBOOKS_LLM_COST_PER_1M_OUTPUT_TOKENS_USD"); v != "" {
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AIBOOKS_LLM_COST_PER_1M_OUTPUT_TOKENS_USD: %q", v)
		}
		cfg.LLMOutputCostPer1MTokensRUB = f
	}
	if v := os.Getenv("AIBOOKS_AUTH_SECRET"); v != "" {
		cfg.AuthSecret = v
	}
	if v := os.Getenv("AIBOOKS_AUTH_TOKEN_TTL_MIN"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_AUTH_TOKEN_TTL_MIN: %q", v)
		}
		cfg.AuthTokenTTLMin = n
	}
	if v := os.Getenv("AIBOOKS_HTTP_RATE_LIMIT_MAX_REQUESTS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_HTTP_RATE_LIMIT_MAX_REQUESTS: %q", v)
		}
		cfg.HTTPRateLimitMaxRequests = n
	}
	if v := os.Getenv("AIBOOKS_HTTP_RATE_LIMIT_WINDOW_SEC"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_HTTP_RATE_LIMIT_WINDOW_SEC: %q", v)
		}
		cfg.HTTPRateLimitWindowSec = n
	}
	if v := os.Getenv("AIBOOKS_HTTP_AUTH_RATE_LIMIT_MAX_REQUESTS"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_HTTP_AUTH_RATE_LIMIT_MAX_REQUESTS: %q", v)
		}
		cfg.HTTPAuthRateLimitMaxRequests = n
	}
	if v := os.Getenv("AIBOOKS_HTTP_AUTH_RATE_LIMIT_WINDOW_SEC"); v != "" {
		n, err := parseEnvInt(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid AIBOOKS_HTTP_AUTH_RATE_LIMIT_WINDOW_SEC: %q", v)
		}
		cfg.HTTPAuthRateLimitWindowSec = n
	}
	if v := os.Getenv("AIBOOKS_LOG_DIR"); v != "" {
		cfg.LogDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("AIBOOKS_LOG_KAFKA_BROKERS"); v != "" {
		parts := strings.Split(v, ",")
		brokers := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				brokers = append(brokers, p)
			}
		}
		cfg.LogKafkaBrokers = brokers
	}
	if v := os.Getenv("AIBOOKS_LOG_KAFKA_TOPIC"); v != "" {
		cfg.LogKafkaTopic = strings.TrimSpace(v)
	}

	if strings.TrimSpace(cfg.EmbeddingProviderType) == "" {
		cfg.EmbeddingProviderType = "gigachat"
	}
	if strings.TrimSpace(cfg.LLMProviderType) == "" {
		cfg.LLMProviderType = "deepseek"
	}
	if cfg.EmbeddingBatchSz <= 0 {
		return Config{}, fmt.Errorf("invalid EmbeddingBatchSz: must be > 0")
	}
	if cfg.EmbeddingDimension <= 0 {
		return Config{}, fmt.Errorf("invalid EmbeddingDimension: must be > 0")
	}
	if cfg.EmbeddingMinRequestIntervalMs < 0 {
		return Config{}, fmt.Errorf("invalid EmbeddingMinRequestIntervalMs: must be >= 0")
	}
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		return Config{}, fmt.Errorf("missing EmbeddingModel")
	}
	if strings.TrimSpace(cfg.OCRErrorMode) == "" {
		cfg.OCRErrorMode = "fail"
	}
	if cfg.OCRConcurrency <= 0 {
		return Config{}, fmt.Errorf("invalid OCRConcurrency: must be > 0")
	}
	if cfg.OCRMaxAttempts <= 0 {
		return Config{}, fmt.Errorf("invalid OCRMaxAttempts: must be > 0")
	}
	if cfg.OCRDBBatchSize <= 0 {
		return Config{}, fmt.Errorf("invalid OCRDBBatchSize: must be > 0")
	}
	if cfg.OCRConcurrency < 1 || cfg.OCRConcurrency > 64 {
		return Config{}, fmt.Errorf("invalid OCRConcurrency: expected 1..64")
	}
	if cfg.LLMMaxOutputTokens <= 0 {
		return Config{}, fmt.Errorf("invalid LLMMaxOutputTokens: must be > 0")
	}
	if cfg.LLMMinRequestIntervalMs < 0 {
		return Config{}, fmt.Errorf("invalid LLMMinRequestIntervalMs: must be >= 0")
	}
	if strings.TrimSpace(cfg.LLMModel) == "" {
		return Config{}, fmt.Errorf("missing LLMModel")
	}
	if cfg.SearchTopKChunks < 0 || cfg.SearchTopBooks < 0 || cfg.SearchExcerptsMaxChars < 0 {
		return Config{}, fmt.Errorf("invalid Search* config: negative values are not allowed")
	}
	if strings.TrimSpace(cfg.AuthSecret) == "" {
		return Config{}, fmt.Errorf("missing required env var AIBOOKS_AUTH_SECRET")
	}
	if cfg.AuthTokenTTLMin <= 0 {
		return Config{}, fmt.Errorf("invalid AuthTokenTTLMin: must be > 0")
	}
	if cfg.HTTPRateLimitMaxRequests < 0 || cfg.HTTPAuthRateLimitMaxRequests < 0 {
		return Config{}, fmt.Errorf("invalid HTTP rate limit: negative values are not allowed")
	}
	if cfg.HTTPRateLimitMaxRequests > 0 && cfg.HTTPRateLimitWindowSec <= 0 {
		return Config{}, fmt.Errorf("invalid HTTPRateLimitWindowSec: must be > 0 when HTTPRateLimitMaxRequests > 0")
	}
	if cfg.HTTPAuthRateLimitMaxRequests > 0 && cfg.HTTPAuthRateLimitWindowSec <= 0 {
		return Config{}, fmt.Errorf("invalid HTTPAuthRateLimitWindowSec: must be > 0 when HTTPAuthRateLimitMaxRequests > 0")
	}
	if strings.TrimSpace(cfg.LogDir) == "" {
		return Config{}, fmt.Errorf("invalid LogDir: must be non-empty")
	}
	if len(cfg.LogKafkaBrokers) > 0 && strings.TrimSpace(cfg.LogKafkaTopic) == "" {
		return Config{}, fmt.Errorf("invalid LogKafkaTopic: must be non-empty when AIBOOKS_LOG_KAFKA_BROKERS is set")
	}

	if cfg.DBURL == "" {
		return Config{}, fmt.Errorf("missing required env var AIBOOKS_DB_URL")
	}
	return cfg, nil
}

func parseEnvInt(v string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(v))
}
