package main

import (
	"net/url"
	"strings"
)

type ProviderCapabilities struct {
	Name              string
	Size              bool
	Quality           bool
	N                 bool
	Background        bool
	OutputFormat      bool
	Complexity        bool
	InputFidelity     bool
	OutputCompression bool
}

func providerCapabilities(cfg Config) ProviderCapabilities {
	profile := strings.ToLower(strings.TrimSpace(cfg.Profile))
	if profile == "" {
		profile = defaultProfile
	}
	if profile == "auto" {
		profile = "compatible"
		if u, err := url.Parse(cfg.BaseURL); err == nil && strings.EqualFold(u.Hostname(), "api.openai.com") {
			profile = "openai"
		}
	}

	switch profile {
	case "openai":
		return ProviderCapabilities{
			Name:              "openai",
			Size:              true,
			Quality:           true,
			N:                 true,
			Background:        true,
			OutputFormat:      true,
			Complexity:        true,
			InputFidelity:     true,
			OutputCompression: true,
		}
	case "minimal":
		return ProviderCapabilities{Name: "minimal"}
	default:
		return ProviderCapabilities{
			Name:    "compatible",
			Size:    true,
			Quality: true,
			N:       true,
		}
	}
}
