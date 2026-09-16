package router

import (
	"agr/config"
	"testing"
)

func TestRoute(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		{Name: "a", Models: []string{"shared", "org/model"}},
		{Name: "b", Models: []string{"shared"}},
	}}
	r := New(cfg)
	for _, tc := range []struct{ input, provider, model string }{
		{"a/shared", "a", "shared"}, {"b/shared", "b", "shared"},
		{"a/org/model", "a", "org/model"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := r.Route(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Provider.Name != tc.provider || got.Model != tc.model {
				t.Fatalf("unexpected route: %+v", got)
			}
		})
	}
	for _, input := range []string{"", "shared", "default", "/shared", "a/", "missing/shared", "b/org/model", "a/unknown", " a/shared", "a/shared "} {
		t.Run("invalid_"+input, func(t *testing.T) {
			if _, err := r.Route(input); err == nil {
				t.Fatalf("expected error for %q", input)
			}
		})
	}
}
