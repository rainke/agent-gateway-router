package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agr/adaptor"
	"agr/config"
)

func TestAnthropicBaseURLPaths(t *testing.T) {
	for _, tc := range []struct{ base, prefix string }{
		{"", "/v1"},
		{"/anthropic", "/anthropic/v1"},
		{"/anthropic/", "/anthropic/v1"},
		{"/anthropic/v1/", "/anthropic/v1"},
		{"/anthropic/v1/messages", "/anthropic/v1"},
		{"/anthropic/v1/messages/count_tokens", "/anthropic/v1"},
		{"/anthropic/messages", "/anthropic"},
	} {
		for _, endpoint := range []string{"/messages", "/messages/count_tokens"} {
			t.Run(tc.base+endpoint, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.RequestURI() != tc.prefix+endpoint+"?region=cn&beta=1" {
						t.Errorf("upstream URI = %s", r.URL.RequestURI())
					}
					io.WriteString(w, `{"input_tokens":36}`)
				}))
				defer upstream.Close()
				p := newProxyWithProviders(config.Provider{
					Name: "test", APIBaseURL: "http://localhost:1",
					AnthropicBaseURL: upstream.URL + tc.base + "?region=cn", Models: []string{"model-a"},
				})
				rec := httptest.NewRecorder()
				path := "/v1" + endpoint
				p.handleProxy(rec, httptest.NewRequest(http.MethodPost, path+"?beta=1", strings.NewReader(`{"model":"test/model-a"}`)), path)
				if rec.Code != http.StatusOK || rec.Body.String() != `{"input_tokens":36}` {
					t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestAnthropicBaseURLUsesOriginalProtocol(t *testing.T) {
	for _, tc := range []struct{ original, adapted, prefix string }{
		{"/v1/messages", "/v1/chat/completions", "/anthropic"},
		{"/v1/messages/count_tokens", "/v1/custom", "/anthropic"},
		{"/v1/chat/completions", "/v1/messages", "/default"},
		{"/v1/responses", "/v1/messages/count_tokens", "/default"},
	} {
		t.Run(tc.original, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.prefix+tc.adapted {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.prefix+tc.adapted)
				}
				io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			name := "test-anthropic-base-" + tc.original
			adaptor.Register(name, requestAdaptor(func(req *adaptor.Request) error {
				req.Path = tc.adapted
				return nil
			}))
			p := newProxyWithProviders(config.Provider{
				Name: "test", APIBaseURL: upstream.URL + "/default",
				AnthropicBaseURL: upstream.URL + "/anthropic", Models: []string{"model-a"}, Adaptors: []string{name},
			})
			rec := httptest.NewRecorder()
			p.handleProxy(rec, httptest.NewRequest(http.MethodPost, tc.original, strings.NewReader(`{"model":"test/model-a"}`)), tc.original)
			if rec.Code != http.StatusOK {
				t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
