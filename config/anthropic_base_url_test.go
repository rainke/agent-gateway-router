package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadAnthropicBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		omit bool
		bad  bool
	}{
		{name: "omitted", omit: true},
		{name: "empty"},
		{name: "host", url: "https://api.example.com"},
		{name: "prefix", url: "https://api.example.com/anthropic"},
		{name: "version", url: "https://api.example.com/anthropic/v1/"},
		{name: "legacy endpoint", url: "https://api.example.com/anthropic/v1/messages"},
		{name: "http with query", url: "http://localhost:8000/anthropic?region=cn"},
		{name: "relative", url: "/anthropic", bad: true},
		{name: "no scheme", url: "api.example.com/anthropic", bad: true},
		{name: "unsupported scheme", url: "ftp://api.example.com", bad: true},
		{name: "no host", url: "https:///anthropic", bad: true},
		{name: "empty hostname", url: "https://:443/anthropic", bad: true},
		{name: "bad escape", url: "https://api.example.com/%zz", bad: true},
		{name: "bad port", url: "https://api.example.com:abc/anthropic", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := `[server]
port = 9999
[[providers]]
name = "test"
api_base_url = "https://api.example.com/v1"
api_key = "sk-test"
models = ["model-a"]
`
			if !tc.omit {
				content += fmt.Sprintf("anthropic_base_url = %q\n", tc.url)
			}
			cfg, err := Load(writeTempConfig(t, content))
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), "anthropic_base_url") || !strings.Contains(err.Error(), "test") {
					t.Fatalf("expected provider-specific anthropic_base_url error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := cfg.Providers[0]
			if p.AnthropicBaseURL != tc.url || p.APIBaseURL != "https://api.example.com/v1" {
				t.Fatalf("base URLs = %q, %q", p.APIBaseURL, p.AnthropicBaseURL)
			}
		})
	}
}
