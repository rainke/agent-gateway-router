package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"agr/config"
	"agr/router"
)

// Proxy 按模型路由请求，保持客户端和上游的 API 格式不变。
type Proxy struct {
	router    *router.Router
	cfg       *config.Config
	transport *http.Transport
}

func New(cfg *config.Config, r *router.Router) *Proxy {
	return &Proxy{router: r, cfg: cfg, transport: &http.Transport{
		MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
		DisableCompression: true, // 不自动解压或重写上游响应。
	}}
}

func (p *Proxy) HandleMessages(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/messages")
}
func (p *Proxy) HandleMessagesCountTokens(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/messages/count_tokens")
}
func (p *Proxy) HandleResponses(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/responses")
}
func (p *Proxy) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/chat/completions")
}

// HandleNotImplemented 保留尚未支持的 Ollama 端点。
func (p *Proxy) HandleNotImplemented(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "feature_not_implemented", "message": "Ollama compatibility is not implemented."}})
}

func (p *Proxy) handleProxy(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		p.writeError(w, http.StatusMethodNotAllowed, "仅支持 POST 请求")
		return
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	clientModel, err := extractModel(body, path)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "提取模型名失败: "+err.Error())
		return
	}
	result, err := p.router.Route(clientModel)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "路由失败: "+err.Error())
		return
	}
	if clientModel != result.Model {
		body, err = replaceModelInBody(body, result.Model)
		if err != nil {
			p.writeError(w, http.StatusBadRequest, "替换模型名失败: "+err.Error())
			return
		}
	}
	target, err := upstreamURL(result.Provider.APIBaseURL, path)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "上游地址无效: "+err.Error())
		return
	}
	slog.Info("代理请求", "path", path, "provider", result.Provider.Name, "model", result.Model)
	reverse := &httputil.ReverseProxy{
		Transport:     p.transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = target
			if target.RawQuery != "" && pr.In.URL.RawQuery != "" {
				pr.Out.URL.RawQuery += "&"
			}
			pr.Out.URL.RawQuery += pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			pr.Out.Body = io.NopCloser(bytes.NewReader(body))
			pr.Out.ContentLength = int64(len(body))
			pr.Out.Header.Del("Content-Length")
			if key := result.Provider.APIKey; key != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+key)
				if pr.In.Header.Get("X-Api-Key") != "" || strings.HasPrefix(path, "/v1/messages") {
					pr.Out.Header.Set("X-Api-Key", key)
				}
			}
		},

		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				slog.Warn("上游返回非正常状态", "provider", result.Provider.Name, "path", path, "status", resp.StatusCode)
			} else if path != "/v1/messages/count_tokens" && resp.Header.Get("Content-Encoding") == "" {
				contentType := resp.Header.Get("Content-Type")
				stream := strings.Contains(contentType, "text/event-stream") || strings.Contains(contentType, "text/stream")
				if stream || strings.Contains(contentType, "json") {
					resp.Body = &usageObserver{ReadCloser: resp.Body, provider: result.Provider.Name, model: result.Model, stream: stream}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("上游请求失败", "provider", result.Provider.Name, "error", err)
			p.writeError(w, http.StatusBadGateway, "请求上游失败")
		},
	}
	reverse.ServeHTTP(w, r)
}

// upstreamURL 支持主机地址、版本前缀及旧配置中的完整端点地址。
func upstreamURL(base, path string) (*url.URL, error) {
	target, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, fmt.Errorf("需要 HTTP(S) 地址")
	}
	prefix := strings.TrimRight(target.Path, "/")
	legacyEndpoint := false
	for _, suffix := range []string{"/messages/count_tokens", "/chat/completions", "/messages", "/responses"} {
		if strings.HasSuffix(prefix, suffix) {
			prefix = strings.TrimSuffix(prefix, suffix)
			legacyEndpoint = true
			break
		}
	}
	if legacyEndpoint || strings.HasSuffix(prefix, "/v1") {
		path = strings.TrimPrefix(path, "/v1")
	}
	target.Path = prefix + path
	target.RawPath = ""
	target.Fragment = ""
	return target, nil
}

// 使用 RawMessage 保留未知字段和大整数，模型名之外不调整协议字段。
func replaceModelInBody(body []byte, model string) ([]byte, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("请求体必须是 JSON 对象")
	}
	req["model"], _ = json.Marshal(model)
	return json.Marshal(req)
}

func extractModel(body []byte, path string) (string, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return "", fmt.Errorf("解析请求体 JSON 失败: %w", err)
	}
	var model string
	if err := json.Unmarshal(req["model"], &model); err != nil || model == "" {
		return "", fmt.Errorf("请求体中需要非空字符串 model 字段")
	}
	return model, nil
}

func (p *Proxy) writeError(w http.ResponseWriter, status int, message string) {
	slog.Error("代理错误", "status", status, "message", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "proxy_error", "message": message}})
}
