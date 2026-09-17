package adaptor

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func mustReq(t *testing.T, body string) *Request {
	t.Helper()
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &Request{Body: req, Headers: make(http.Header), Path: "/v1/chat/completions", Method: http.MethodPost}
}

func TestRegistry_MiniMaxRegistered(t *testing.T) {
	if !Known("minimax") || !Known("MiniMax") || !Known("MINIMAX") {
		t.Fatalf("minimax 应已注册，names=%v", Names())
	}
	if Known("nope") {
		t.Error("nope 不应已注册")
	}
	if len(Names()) == 0 {
		t.Error("Names 不应为空")
	}
}

func TestApply_NoNames(t *testing.T) {
	req := mustReq(t, `{"model":"m"}`)
	if err := Apply(req, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := req.Body["reasoning_split"]; exists {
		t.Error("空 names 不应改动 req")
	}
}

func TestApply_UnknownName(t *testing.T) {
	err := Apply(mustReq(t, `{}`), []string{"unknown-adaptor"})
	if err == nil || !strings.Contains(err.Error(), "未知 adaptor") {
		t.Fatalf("期望未知 adaptor 错误，实际 %v", err)
	}
}

func TestApply_MiniMax(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
		want string // "" 表示不存在
	}{
		{
			name: "chat_completions 注入默认 true",
			path: "/v1/chat/completions",
			body: `{"model":"MiniMax-M3","messages":[{"role":"user","content":"hi"}]}`,
			want: "true",
		},
		{
			name: "已有 true 时保留",
			path: "/v1/chat/completions",
			body: `{"model":"MiniMax-M3","reasoning_split":true,"messages":[]}`,
			want: "true",
		},
		{
			name: "已有 false 时保留客户端意图",
			path: "/v1/chat/completions",
			body: `{"model":"MiniMax-M3","reasoning_split":false,"messages":[]}`,
			want: "false",
		},
		{
			name: "messages 不注入",
			path: "/v1/messages",
			body: `{"model":"MiniMax-M3","messages":[]}`,
			want: "",
		},
		{
			name: "responses 不注入",
			path: "/v1/responses",
			body: `{"model":"MiniMax-M3","input":"hi"}`,
			want: "",
		},
		{
			name: "count_tokens 不注入",
			path: "/v1/messages/count_tokens",
			body: `{"model":"MiniMax-M3"}`,
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := mustReq(t, tc.body)
			req.Path = tc.path
			if err := Apply(req, []string{"minimax"}); err != nil {
				t.Fatal(err)
			}
			raw, exists := req.Body["reasoning_split"]
			if tc.want == "" {
				if exists {
					t.Errorf("不应注入 reasoning_split，实际 %s", raw)
				}
				return
			}
			if !exists {
				t.Fatal("缺少 reasoning_split")
			}
			if string(raw) != tc.want {
				t.Errorf("reasoning_split = %s, want %s", raw, tc.want)
			}
			if _, ok := req.Body["model"]; !ok {
				t.Error("丢失 model 字段")
			}
		})
	}
}

func TestApply_SharedReqAcrossLayers(t *testing.T) {
	req := mustReq(t, `{"model":"MiniMax-M3","custom":1,"messages":[]}`)
	if err := Apply(req, []string{"minimax", "minimax"}); err != nil {
		t.Fatal(err)
	}
	if string(req.Body["reasoning_split"]) != "true" {
		t.Errorf("reasoning_split = %s", req.Body["reasoning_split"])
	}
	if string(req.Body["custom"]) != "1" {
		t.Errorf("custom = %s", req.Body["custom"])
	}
	// 序列化后仍保留大整数与嵌套结构
	req.Body["big"] = json.RawMessage("9007199254740993")
	out, err := json.Marshal(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "9007199254740993") {
		t.Errorf("大整数丢失: %s", out)
	}
}

func BenchmarkMiniMaxApply(b *testing.B) {
	msg := `{"role":"user","content":"` + strings.Repeat("token ", 200) + `"}`
	body := `{"model":"MiniMax-M3","messages":[` + strings.TrimSuffix(strings.Repeat(msg+",", 50), ",") + `]}`
	var req map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		b.Fatal(err)
	}
	request := &Request{Body: req, Path: "/v1/chat/completions"}
	m := minimax{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delete(req, "reasoning_split")
		if err := m.Apply(request); err != nil {
			b.Fatal(err)
		}
	}
}

type adaptorFunc func(*Request) error

func (f adaptorFunc) Apply(req *Request) error { return f(req) }

func TestApply_RequestChain(t *testing.T) {
	req := mustReq(t, `{"model":"m"}`)
	sentinel := errors.New("adaptation failed")
	Register("test-first", adaptorFunc(func(r *Request) error {
		if r != req {
			t.Fatal("request identity changed")
		}
		r.Body = map[string]json.RawMessage{"model": json.RawMessage(`"changed"`)}
		r.Headers = http.Header{"X-Test": []string{"one", "two"}}
		r.Path = "/v1/responses"
		r.Method = http.MethodPut
		r.RawQuery = "mode=test"
		return nil
	}))
	Register("test-second", adaptorFunc(func(r *Request) error {
		if string(r.Body["model"]) != `"changed"` || len(r.Headers.Values("X-Test")) != 2 || r.Path != "/v1/responses" || r.Method != http.MethodPut || r.RawQuery != "mode=test" {
			t.Fatalf("request changes lost: %+v", r)
		}
		return sentinel
	}))
	Register("test-third", adaptorFunc(func(r *Request) error { t.Fatal("chain continued after error"); return nil }))
	t.Cleanup(func() {
		delete(registry, "test-first")
		delete(registry, "test-second")
		delete(registry, "test-third")
	})
	err := Apply(req, []string{"test-first", "test-second", "test-third"})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "test-second") {
		t.Fatalf("error = %v", err)
	}
}
