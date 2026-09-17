package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agr/config"
)

// Exercise TOML loading, registered HTTP routes, and two independent upstreams.
func TestAnthropicBaseURLHTTP(t *testing.T) {
	for _, override := range []bool{false, true} {
		for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions", "/v1/responses"} {
			for _, reply := range []struct {
				name, contentType, body string
				status                  int
			}{
				{"json", "application/json", `{"input_tokens":36,"custom":true}`, http.StatusOK},
				{"error", "application/json", `{"error":{"type":"rate_limit"}}`, http.StatusTooManyRequests},
				{"sse", "text/event-stream", ": ping\r\nevent: custom\r\ndata: {\"untouched\":true}\r\n\r\n", http.StatusOK},
			} {
				if reply.name == "sse" && path == "/v1/messages/count_tokens" {
					continue
				}
				t.Run(fmt.Sprintf("override=%t%s/%s", override, path, reply.name), func(t *testing.T) {
					wantAnthropic := override && (path == "/v1/messages" || path == "/v1/messages/count_tokens")
					upstream := func(anthropic bool) http.HandlerFunc {
						return func(w http.ResponseWriter, r *http.Request) {
							if anthropic != wantAnthropic {
								t.Error("request sent to the wrong upstream")
							}
							wantPath := path
							if anthropic {
								wantPath = "/anthropic" + path
							}
							if r.URL.Path != wantPath || r.Method != http.MethodPost {
								t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
							}
							if r.Header.Get("Authorization") != "Bearer sk-test" || r.Header.Get("X-Api-Key") != "sk-test" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
								t.Error("provider credentials or protocol headers lost")
							}
							var body map[string]json.RawMessage
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
								t.Error(err)
							}
							if string(body["model"]) != `"model-a"` || string(body["custom"]) != "9007199254740993" {
								t.Errorf("unexpected forwarded body = %s", body)
							}
							w.Header().Set("Content-Type", reply.contentType)
							w.Header().Set("X-Request-Id", "upstream-id")
							w.Header().Set("Retry-After", "15")
							w.WriteHeader(reply.status)
							io.WriteString(w, reply.body)
						}
					}
					defaultServer := httptest.NewServer(upstream(false))
					defer defaultServer.Close()
					anthropicServer := httptest.NewServer(upstream(true))
					defer anthropicServer.Close()
					content := fmt.Sprintf("[server]\nport = 9999\n[[providers]]\nname = 'test'\napi_base_url = %q\napi_key = 'sk-test'\nmodels = ['model-a']\n", defaultServer.URL)
					if override {
						content += fmt.Sprintf("anthropic_base_url = %q\n", anthropicServer.URL+"/anthropic")
					}
					configPath := filepath.Join(t.TempDir(), "config.toml")
					if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
					cfg, err := config.Load(configPath)
					if err != nil {
						t.Fatal(err)
					}
					gateway := httptest.NewServer(New(cfg).httpServer.Handler)
					defer gateway.Close()
					req, err := http.NewRequest(http.MethodPost, gateway.URL+path, strings.NewReader(`{"model":"test/model-a","custom":9007199254740993}`))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Api-Key", "client-placeholder")
					req.Header.Set("Anthropic-Version", "2023-06-01")
					resp, err := gateway.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Fatal(err)
					}
					if resp.StatusCode != reply.status || string(body) != reply.body || resp.Header.Get("Content-Type") != reply.contentType || resp.Header.Get("X-Request-Id") != "upstream-id" || resp.Header.Get("Retry-After") != "15" {
						t.Fatalf("response = %d %s %s", resp.StatusCode, resp.Header, body)
					}
				})
			}
		}
	}
}
