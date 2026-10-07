package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"search-router/internal/model"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

func TestLoad_QPSZero_Errors(t *testing.T) {
	p := writeTemp(t, `
routing:
  mode: split
providers:
  - id: serper
    enabled: true
    contentType: abstract
    keys:
      - id: k1
        value: v
        enabled: true
        qps: 0
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for qps=0, got nil")
	}
}

func TestLoad_InvalidMode_Errors(t *testing.T) {
	p := writeTemp(t, `
routing:
  mode: bogus
providers:
  - id: serper
    enabled: true
    contentType: abstract
    keys:
      - id: k1
        value: v
        enabled: true
        qps: 5
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}

func TestLoad_InvalidContentType_Errors(t *testing.T) {
	p := writeTemp(t, `
routing:
  mode: split
providers:
  - id: serper
    enabled: true
    contentType: fulltext
    keys:
      - id: k1
        value: v
        enabled: true
        qps: 5
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for invalid contentType, got nil")
	}
}

func TestLoad_Defaults(t *testing.T) {
	p := writeTemp(t, `
routing: {}
providers:
  - id: serper
    enabled: true
    contentType: abstract
    timeout: 10s
    keys:
      - id: k1
        value: v
        enabled: true
        qps: 5
        dailyQuota: 100
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Routing.Mode != model.ModeSplit {
		t.Errorf("mode default: got %q want %q", cfg.Routing.Mode, model.ModeSplit)
	}
	pv := cfg.Providers[0]
	if pv.Weight != 1 {
		t.Errorf("weight default: got %d want 1", pv.Weight)
	}
	if pv.Priority != 99 {
		t.Errorf("priority default: got %d want 99", pv.Priority)
	}
	if pv.MaxKeyAttempts != 3 {
		t.Errorf("maxKeyAttempts default: got %d want 3", pv.MaxKeyAttempts)
	}
	if pv.Timeout != 10*time.Second {
		t.Errorf("timeout: got %v want 10s", pv.Timeout)
	}
	if pv.Keys[0].DailyQuota == nil || *pv.Keys[0].DailyQuota != 100 {
		t.Errorf("dailyQuota: got %v want 100", pv.Keys[0].DailyQuota)
	}
}

func TestLoad_ExampleConfig(t *testing.T) {
	// 解析仓库根目录的 config.example.yaml，作为 Load 解析验证目标。
	p := filepath.Join("..", "..", "config.example.yaml")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load(config.example.yaml): %v", err)
	}
	if len(cfg.Providers) != 4 {
		t.Fatalf("providers: got %d want 4", len(cfg.Providers))
	}
	// 每个 provider 的 contentType 与 data-model 映射一致。
	want := map[string]string{
		"serper":     model.ContentTypeAbstract,
		"tavily":     model.ContentTypeBody,
		"brave":      model.ContentTypeAbstract,
		"brightdata": model.ContentTypeAbstract,
	}
	for _, pv := range cfg.Providers {
		if got := want[pv.ID]; got != "" && pv.ContentType != got {
			t.Errorf("provider %q contentType: got %q want %q", pv.ID, pv.ContentType, got)
		}
		if pv.Weight < 1 || pv.MaxKeyAttempts < 1 {
			t.Errorf("provider %q: defaults not filled (weight=%d maxKeyAttempts=%d)", pv.ID, pv.Weight, pv.MaxKeyAttempts)
		}
		for _, k := range pv.Keys {
			if k.QPS <= 0 {
				t.Errorf("provider %q key %q: qps not > 0", pv.ID, k.ID)
			}
		}
	}
}
