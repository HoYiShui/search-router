package main

import (
	"log"
	"net/http"

	"search-router/internal/breaker"
	"search-router/internal/config"
	"search-router/internal/keypool"
	"search-router/internal/model"
	"search-router/internal/providers"
	"search-router/internal/router"
	"search-router/internal/server"
)

func main() {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	entries := buildEntries(cfg.Providers)
	r := router.NewRouter(entries, cfg.Routing.Mode)
	handler := server.NewHandler(r)

	log.Printf("search-router listening on :8080 (mode=%s, providers=%d)", cfg.Routing.Mode, len(entries))
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// buildEntries constructs a ProviderEntry per configured provider: adapter +
// key pool + circuit breaker.
func buildEntries(providers []model.ProviderConfig) []router.ProviderEntry {
	entries := make([]router.ProviderEntry, 0, len(providers))
	for _, pc := range providers {
		adapter := buildAdapter(pc)
		if adapter == nil {
			log.Printf("unknown provider id %q, skipping", pc.ID)
			continue
		}
		entries = append(entries, router.ProviderEntry{
			Config:  pc,
			Adapter: adapter,
			Pool:    keypool.NewPool(pc.Keys, keypool.PoolOptions{}),
			Breaker: breaker.NewBreaker(breaker.BreakerOptions{}),
		})
	}
	return entries
}

// buildAdapter maps a provider id to its adapter implementation.
func buildAdapter(pc model.ProviderConfig) providers.Provider {
	switch pc.ID {
	case "serper":
		return providers.NewSerper(pc)
	case "tavily":
		return providers.NewTavily(pc)
	case "brave":
		return providers.NewBrave(pc)
	case "brightdata":
		return providers.NewBrightData(pc)
	default:
		return nil
	}
}
