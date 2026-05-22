package router

import (
	"testing"

	"agr/config"
)

func newTestConfig() *config.Config {
	return &config.Config{
		Providers: []config.Provider{
			{
				Name:        "provider-a",
				APIBaseURL:  "http://a.com/v1",
				APIKey:      "sk-a",
				Models:      []string{"model-1", "model-2"},
				Transformer: []string{"openai-to-custom"},
			},
			{
				Name:        "provider-b",
				APIBaseURL:  "http://b.com/v1",
				APIKey:      "sk-b",
				Models:      []string{"model-3"},
				Transformer: []string{"openai-to-custom"},
			},
		},
		Router: map[string]string{
			"default":   "provider-a,model-1",
			"claude-3":  "provider-a,model-2",
			"special":   "provider-b,model-3",
		},
	}
}

func TestNew(t *testing.T) {
	cfg := newTestConfig()
	r := New(cfg)
	if r == nil {
		t.Fatal("New 返回 nil")
	}
	if len(r.providerMap) != 2 {
		t.Errorf("providerMap 长度期望 2，实际 %d", len(r.providerMap))
	}
}

func TestRoute_ExactMatch(t *testing.T) {
	cfg := newTestConfig()
	r := New(cfg)

	result, err := r.Route("claude-3")
	if err != nil {
		t.Fatalf("精确匹配路由失败: %v", err)
	}
	if result.Provider.Name != "provider-a" {
		t.Errorf("Provider 期望 provider-a，实际 %s", result.Provider.Name)
	}
	if result.Model != "model-2" {
		t.Errorf("模型期望 model-2，实际 %s", result.Model)
	}
}

func TestRoute_DefaultFallback(t *testing.T) {
	cfg := newTestConfig()
	r := New(cfg)

	result, err := r.Route("unknown-model")
	if err != nil {
		t.Fatalf("默认路由失败: %v", err)
	}
	if result.Provider.Name != "provider-a" {
		t.Errorf("Provider 期望 provider-a，实际 %s", result.Provider.Name)
	}
	if result.Model != "model-1" {
		t.Errorf("模型期望 model-1，实际 %s", result.Model)
	}
}

func TestRoute_NoDefaultRoute(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "p1", Models: []string{"m1"}},
		},
		Router: map[string]string{
			"specific": "p1,m1",
		},
	}
	r := New(cfg)

	_, err := r.Route("unknown-model")
	if err == nil {
		t.Fatal("期望无默认路由时返回错误")
	}
}

func TestRoute_SpecificProvider(t *testing.T) {
	cfg := newTestConfig()
	r := New(cfg)

	result, err := r.Route("special")
	if err != nil {
		t.Fatalf("路由到 provider-b 失败: %v", err)
	}
	if result.Provider.Name != "provider-b" {
		t.Errorf("Provider 期望 provider-b，实际 %s", result.Provider.Name)
	}
	if result.Model != "model-3" {
		t.Errorf("模型期望 model-3，实际 %s", result.Model)
	}
}

func TestRoute_InvalidRouteFormat(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "p1", Models: []string{"m1"}},
		},
		Router: map[string]string{
			"bad": "no-comma-here",
		},
	}
	r := New(cfg)

	_, err := r.Route("bad")
	if err == nil {
		t.Fatal("期望路由格式错误时返回错误")
	}
}

func TestRoute_NonexistentProvider(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{
			{Name: "p1", Models: []string{"m1"}},
		},
		Router: map[string]string{
			"test": "nonexistent,m1",
		},
	}
	r := New(cfg)

	_, err := r.Route("test")
	if err == nil {
		t.Fatal("期望引用不存在的 Provider 时返回错误")
	}
}
