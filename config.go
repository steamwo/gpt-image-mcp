package main

import (
	"encoding/json"
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
	defaultProfile       = "auto"
)

type ImageDefaults struct {
	Background        string
	OutputFormat      string
	Complexity        string
	InputFidelity     string
	OutputCompression *int
}

type Config struct {
	APIKey                   string
	BaseURL                  string
	Organization             string
	Project                  string
	ExtraHeaders             map[string]string
	DefaultModel             string
	OutputDir                string
	AllowedDirs              []string
	Timeout                  time.Duration
	EmbedMaxBytes            int64
	Profile                  string
	AllowRemoteImages        bool
	AllowPrivateRemoteImages bool
	Defaults                 ImageDefaults
}

func loadConfig() (Config, error) {
	cfg := Config{
		APIKey:            strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		BaseURL:           envOr("OPENAI_BASE_URL", defaultBaseURL),
		Organization:      strings.TrimSpace(os.Getenv("OPENAI_ORGANIZATION")),
		Project:           strings.TrimSpace(os.Getenv("OPENAI_PROJECT")),
		DefaultModel:      envOr("GPT_IMAGE_MODEL", defaultModel),
		OutputDir:         envOr("GPT_IMAGE_OUTPUT_DIR", defaultOutputDir),
		Timeout:           defaultTimeout,
		EmbedMaxBytes:     defaultEmbedMaxBytes,
		Profile:           strings.ToLower(envOr("GPT_IMAGE_PROFILE", defaultProfile)),
		AllowRemoteImages: true,
		Defaults: ImageDefaults{
			Background:    strings.TrimSpace(os.Getenv("GPT_IMAGE_DEFAULT_BACKGROUND")),
			OutputFormat:  strings.TrimSpace(os.Getenv("GPT_IMAGE_DEFAULT_OUTPUT_FORMAT")),
			Complexity:    strings.TrimSpace(os.Getenv("GPT_IMAGE_DEFAULT_COMPLEXITY")),
			InputFidelity: strings.TrimSpace(os.Getenv("GPT_IMAGE_DEFAULT_INPUT_FIDELITY")),
		},
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, fmt.Errorf("OPENAI_BASE_URL cannot be empty")
	}
	if !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return Config{}, fmt.Errorf("OPENAI_BASE_URL must start with http:// or https://")
	}

	switch cfg.Profile {
	case "auto", "openai", "compatible", "minimal":
	default:
		return Config{}, fmt.Errorf("invalid GPT_IMAGE_PROFILE %q; use auto, openai, compatible, or minimal", cfg.Profile)
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

	var err error
	cfg.AllowRemoteImages, err = envBool("GPT_IMAGE_ALLOW_REMOTE_IMAGES", true)
	if err != nil {
		return Config{}, err
	}
	cfg.AllowPrivateRemoteImages, err = envBool("GPT_IMAGE_ALLOW_PRIVATE_REMOTE_IMAGES", false)
	if err != nil {
		return Config{}, err
	}

	if raw := strings.TrimSpace(os.Getenv("GPT_IMAGE_DEFAULT_OUTPUT_COMPRESSION")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return Config{}, fmt.Errorf("invalid GPT_IMAGE_DEFAULT_OUTPUT_COMPRESSION %q; use 0-100", raw)
		}
		cfg.Defaults.OutputCompression = &n
	}

	if raw := strings.TrimSpace(os.Getenv("OPENAI_EXTRA_HEADERS_JSON")); raw != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return Config{}, fmt.Errorf("invalid OPENAI_EXTRA_HEADERS_JSON: %w", err)
		}
		cfg.ExtraHeaders = headers
	}

	absOut, err := filepath.Abs(cfg.OutputDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve output directory: %w", err)
	}
	cfg.OutputDir = filepath.Clean(absOut)

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

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s %q; use true or false", key, raw)
	}
	return value, nil
}
