// Package config loads the yaml configuration into model.Config.
//
// Env is used only to locate the config file path (SEARCH_ROUTER_CONFIG,
// defaulting to ./config.yaml); key values and quotas stay out of env.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"search-router/internal/model"
)

// DefaultPath resolves the config file path from env SEARCH_ROUTER_CONFIG,
// falling back to ./config.yaml.
func DefaultPath() string {
	if p := os.Getenv("SEARCH_ROUTER_CONFIG"); p != "" {
		return p
	}
	return "./config.yaml"
}

// Load reads, validates, and fills defaults for the yaml config at path.
func Load(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg model.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validate(cfg *model.Config) error {
	if cfg.Routing.Mode == "" {
		cfg.Routing.Mode = model.ModeSplit
	}
	if cfg.Routing.Mode != model.ModeSplit && cfg.Routing.Mode != model.ModeFailover {
		return fmt.Errorf("routing.mode: invalid value %q (want %q or %q)",
			cfg.Routing.Mode, model.ModeSplit, model.ModeFailover)
	}

	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.ID == "" {
			return fmt.Errorf("providers[%d].id: required", i)
		}

		if p.Weight < 0 {
			return fmt.Errorf("providers[%q].weight: must be >= 0", p.ID)
		}
		if p.Weight == 0 {
			p.Weight = 1
		}

		if p.Priority < 0 {
			return fmt.Errorf("providers[%q].priority: must be >= 0", p.ID)
		}
		if p.Priority == 0 {
			p.Priority = 99
		}

		if p.MaxKeyAttempts <= 0 {
			p.MaxKeyAttempts = 3
		}

		if p.ContentType != model.ContentTypeAbstract && p.ContentType != model.ContentTypeBody {
			return fmt.Errorf("providers[%q].contentType: invalid value %q (want %q or %q)",
				p.ID, p.ContentType, model.ContentTypeAbstract, model.ContentTypeBody)
		}

		for j := range p.Keys {
			k := &p.Keys[j]
			if k.ID == "" {
				return fmt.Errorf("providers[%q].keys[%d].id: required", p.ID, j)
			}
			if k.QPS <= 0 {
				return fmt.Errorf("providers[%q].keys[%q].qps: must be > 0", p.ID, k.ID)
			}
		}
	}

	return nil
}
