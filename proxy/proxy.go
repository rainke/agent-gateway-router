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

	"agr/adaptor"
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
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	// 请求体只解析一次；模型替换与各层 adaptor 共用同一 req，结束时再序列化。
	payload, err := parseRequestBody(raw)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req := &adaptor.Request{Body: payload, Headers: r.Header.Clone(), Path: path, Method: r.Method, RawQuery: r.URL.RawQuery}
	clientModel, err := extractModel(req.Body)
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
		encoded, mErr := json.Marshal(result.Model)
		if mErr != nil {
			p.writeError(w, http.StatusBadRequest, "替换模型名失败: "+mErr.Error())
			return
		}
		req.Body["model"] = encoded
	}
	if err := adaptor.Apply(req, result.Provider.Adaptors); err != nil {
		p.writeError(w, http.StatusBadRequest, "适配请求失败: "+err.Error())
		return
	}
	body, err := json.Marshal(req.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "序列化请求体失败: "+err.Error())
		return
	}
	target, err := upstreamURL(result.Provider.APIBaseURL, req.Path)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "上游地址无效: "+err.Error())
		return
	}
	slog.Info("代理请求", "path", path, "provider", result.Provider.Name, "model", result.Model)
	logRequestParameters(body, path, result.Provider.Name, result.Model)
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
			// 请求未压缩响应，便于旁路读取 SSE / JSON usage。
			pr.Out.Header.Set("Accept-Encoding", "identity")
			if key := result.Provider.APIKey; key != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+key)
				if pr.In.Header.Get("X-Api-Key") != "" || strings.HasPrefix(req.Path, "/v1/messages") {
					pr.Out.Header.Set("X-Api-Key", key)
				}
			}
		},

		ModifyResponse: func(resp *http.Response) error {
			contentType := strings.ToLower(resp.Header.Get("Content-Type"))
			encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
			slog.Debug("上游响应", "provider", result.Provider.Name, "model", result.Model, "path", path,
				"status", resp.StatusCode, "content_type", resp.Header.Get("Content-Type"), "content_encoding", encoding)

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				slog.Warn("上游返回非正常状态", "provider", result.Provider.Name, "path", path, "status", resp.StatusCode)
			} else if path != "/v1/messages/count_tokens" {
				if encoding != "" && encoding != "identity" {
					slog.Warn("跳过 usage 统计", "provider", result.Provider.Name, "model", result.Model, "reason", "compressed_response", "content_encoding", encoding)
					return nil
				}
				stream := strings.Contains(contentType, "text/event-stream") || strings.Contains(contentType, "text/stream")
				if stream || strings.Contains(contentType, "json") {
					resp.Body = &usageObserver{ReadCloser: resp.Body, provider: result.Provider.Name, model: result.Model, stream: stream}
				} else {
					slog.Warn("跳过 usage 统计", "provider", result.Provider.Name, "model", result.Model, "reason", "unsupported_content_type", "content_type", contentType)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("上游请求失败", "provider", result.Provider.Name, "error", err)
			p.writeError(w, http.StatusBadGateway, "请求上游失败")
		},
	}
	// 在 ReverseProxy 清理逐跳头之前传入适配后的请求头，避免重新引入逐跳头。
	forward := r.Clone(r.Context())
	forward.Header = req.Headers.Clone()
	forward.Method = req.Method
	forward.URL.RawQuery = req.RawQuery
	reverse.ServeHTTP(w, forward)
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

// parseRequestBody 解析 JSON 对象请求体。值保留为 RawMessage，大整数与未知字段不丢失。
func parseRequestBody(body []byte) (map[string]json.RawMessage, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析请求体 JSON 失败: %w", err)
	}
	if req == nil {
		return nil, fmt.Errorf("请求体必须是 JSON 对象")
	}
	return req, nil
}

func extractModel(req map[string]json.RawMessage) (string, error) {
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
