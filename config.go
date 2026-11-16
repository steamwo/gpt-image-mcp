package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.openai.com/v1"
	defaultModel         = "gpt-image-2.6-sunburst"
	defaultOutputDir     = "generated-images"
	defaultTimeout       = 10 * time.Minute
	defaultEmbedMaxBytes = int64(8 << 20)
)

type Config struct {
	APIKey        string
	BaseURL       string
	Organization  string
	Project       string
	DefaultModel  string
	OutputDir     string
	AllowedDirs   []string
	Timeout       time.Duration
	EmbedMaxBytes int64
}

func loadConfig() (Config, error) {
	cfg := Config{
		APIKey:        strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		BaseURL:       envOr("OPENAI_BASE_URL", defaultBaseURL),
		Organization:  strings.TrimSpace(os.Getenv("OPENAI_ORGANIZATION")),
		Project:       strings.TrimSpace(os.Getenv("OPENAI_PROJECT")),
		DefaultModel:  envOr("GPT_IMAGE_MODEL", defaultModel),
		OutputDir:     envOr("GPT_IMAGE_OUTPUT_DIR", defaultOutputDir),
		Timeout:       defaultTimeout,
		EmbedMaxBytes: defaultEmbedMaxBytes,
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, fmt.Errorf("OPENAI_BASE_URL cannot be empty")
	}
	if !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return Config{}, fmt.Errorf("OPENAI_BASE_URL must start with http:// or https://")
	}

	if raw := strings.TrimSpace(os.Getenv("GPT_IMAGE_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("invalid GPT_IMAGE_TIMEOUT %q", raw)
		}
		cfg.Timeout = d
	}

	if raw := strings.TrimSpace(os.Getenv("GPT_IMAGE_EMBED_MAX_BYTES")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("invalid GPT_IMAGE_EMBED_MAX_BYTES %q", raw)
		}
		cfg.EmbedMaxBytes = n
	}

	absOut, err := filepath.Abs(cfg.OutputDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve output directory: %w", err)
	}
	cfg.OutputDir = absOut

	if raw := strings.TrimSpace(os.Getenv("GPT_IMAGE_ALLOWED_DIRS")); raw != "" {
		parts := filepath.SplitList(raw)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			abs, err := filepath.Abs(part)
			if err != nil {
				return Config{}, fmt.Errorf("resolve allowed directory %q: %w", part, err)
			}
			cfg.AllowedDirs = append(cfg.AllowedDirs, filepath.Clean(abs))
		}
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
