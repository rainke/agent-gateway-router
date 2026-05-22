package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agr/config"
	"agr/router"
	"agr/transformer"
)

// Proxy 代理处理器
type Proxy struct {
	router *router.Router
	cfg    *config.Config
	client *http.Client
}

// New 创建代理实例
func New(cfg *config.Config, r *router.Router) *Proxy {
	return &Proxy{
		router: r,
		cfg:    cfg,
		client: &http.Client{
			Timeout: 0, // 流式请求不设超时
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// HandleMessages 处理 /v1/messages 请求（Claude Code）
func (p *Proxy) HandleMessages(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/messages")
}

// HandleMessagesCountTokens 处理 /v1/messages/count_tokens 请求（Claude Code）
func (p *Proxy) HandleMessagesCountTokens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	defer r.Body.Close()

	clientModel, err := extractModel(body, "/v1/messages/count_tokens")
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "提取模型名失败: "+err.Error())
		return
	}

	result, err := p.router.Route(clientModel)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "路由失败: "+err.Error())
		return
	}

	inputTokens, err := countClaudeMessageTokens(body, result.Model)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "计算 token 失败: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"input_tokens": inputTokens,
	})
}

// HandleResponses 处理 /v1/responses 请求（Codex）
func (p *Proxy) HandleResponses(w http.ResponseWriter, r *http.Request) {
	p.handleProxy(w, r, "/v1/responses")
}

// HandleNotImplemented 处理二期才实现的 Ollama 端点
func (p *Proxy) HandleNotImplemented(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "feature_not_implemented",
			"message": "Ollama compatibility is planned for phase 2.",
		},
	})
}

// handleProxy 通用代理处理逻辑
func (p *Proxy) handleProxy(w http.ResponseWriter, r *http.Request, path string) {
	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	defer r.Body.Close()

	// 提取客户端请求的模型名
	clientModel, err := extractModel(body, path)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "提取模型名失败: "+err.Error())
		return
	}

	slog.Info("收到代理请求", "path", path, "model", clientModel)

	// 路由到目标 Provider
	result, err := p.router.Route(clientModel)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "路由失败: "+err.Error())
		return
	}

	slog.Info("路由结果", "provider", result.Provider.Name, "upstream_model", result.Model)

	// 创建 Transformer 链
	chain, err := transformer.NewChain(result.Provider.Transformer)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "创建 Transformer 链失败: "+err.Error())
		return
	}

	// 构建 context，传递请求路径、上游模型名和客户端模型名
	ctx := context.WithValue(r.Context(), transformer.RequestPathKey, path)
	ctx = context.WithValue(ctx, transformer.UpstreamModelKey, result.Model)
	ctx = context.WithValue(ctx, transformer.ClientModelKey, clientModel)

	slog.Debug("转换前的请求体", "body", string(body))

	// 执行请求转换
	transformedBody, err := chain.TransformRequest(ctx, body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "请求转换失败: "+err.Error())
		return
	}

	slog.Debug("转换后的请求体", "body", string(transformedBody))

	// 构建上游请求
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, result.Provider.APIBaseURL, strings.NewReader(string(transformedBody)))
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "构建上游请求失败: "+err.Error())
		return
	}

	// 设置请求头
	upstreamReq.Header.Set("Content-Type", "application/json")
	if result.Provider.APIKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+result.Provider.APIKey)
	}

	// 转发原始请求中的部分头信息
	if accept := r.Header.Get("Accept"); accept != "" {
		upstreamReq.Header.Set("Accept", accept)
	}

	// 发送请求到上游
	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "请求上游失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 判断是否为流式响应
	contentType := resp.Header.Get("Content-Type")
	isStream := strings.Contains(contentType, "text/event-stream") ||
		strings.Contains(contentType, "text/stream")

	if isStream {
		// 根据客户端协议选择不同的流式响应处理
		if strings.Contains(path, "/v1/messages") {
			p.handleClaudeStreamResponse(ctx, w, resp, chain, clientModel)
		} else {
			p.handleStreamResponse(ctx, w, resp, chain)
		}
	} else {
		p.handleNormalResponse(ctx, w, resp, chain)
	}
}

// handleClaudeStreamResponse 处理 Claude 客户端的流式响应，输出 Anthropic SSE 格式
func (p *Proxy) handleClaudeStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain, clientModel string) {
	// 设置流式响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("ResponseWriter 不支持 Flusher")
		return
	}

	// 创建流式状态追踪
	state := &transformer.StreamState{BlockIndex: -1, OpenBlocks: make(map[int]bool)}
	ctx = context.WithValue(ctx, transformer.StreamStateKey, state)

	// 发送 message_start 事件
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":      msgID,
			"type":    "message",
			"role":    "assistant",
			"model":   clientModel,
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	}
	msgStartJSON, _ := json.Marshal(messageStart)
	fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", msgStartJSON)
	flusher.Flush()

	// 追踪 stop reason
	stopReason := "end_turn"

	// 读取上游流式响应并转换
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// 提取 data 内容，兼容 "data:" 和 "data: " 两种格式
		var data string
		if strings.HasPrefix(line, "data: ") {
			data = line[6:]
		} else if strings.HasPrefix(line, "data:") {
			data = line[5:]
		} else {
			continue
		}

		// 检查流结束标记
		if data == "[DONE]" {
			break
		}

		slog.Debug("上游原始 SSE chunk", "data", data)

		// 检查 finish_reason 以确定 stop_reason
		var rawChunk map[string]any
		if err := json.Unmarshal([]byte(data), &rawChunk); err == nil {
			if choices, ok := rawChunk["choices"].([]any); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]any); ok {
					if fr, _ := choice["finish_reason"].(string); fr != "" {
						switch fr {
						case "tool_calls", "function_call":
							stopReason = "tool_use"
						case "length":
							stopReason = "max_tokens"
						case "stop":
							stopReason = "end_turn"
						}
					}
				}
			}
		}

		// 通过 Transformer 链转换 chunk
		transformed, err := chain.TransformStream(ctx, []byte(data))
		if err != nil {
			slog.Error("流式 chunk 转换失败", "error", err)
			continue
		}

		// TransformStream 返回 nil 表示跳过该 chunk
		if transformed == nil {
			continue
		}

		// 检查返回的是单个事件还是事件数组
		transformedStr := string(transformed)
		if strings.HasPrefix(transformedStr, "[") {
			// 事件数组，拆分输出
			var events []map[string]any
			if err := json.Unmarshal(transformed, &events); err == nil {
				for _, event := range events {
					eventType, _ := event["type"].(string)
					eventJSON, _ := json.Marshal(event)

					// 根据事件类型选择 SSE event name
					sseEvent := "content_block_delta"
					if strings.Contains(eventType, "start") {
						sseEvent = "content_block_start"
					} else if strings.Contains(eventType, "stop") {
						sseEvent = "content_block_stop"
					}

					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEvent, eventJSON)
					flusher.Flush()
				}
			}
		} else {
			// 单个事件
			var event map[string]any
			if err := json.Unmarshal(transformed, &event); err == nil {
				eventType, _ := event["type"].(string)
				sseEvent := "content_block_delta"
				if strings.Contains(eventType, "start") {
					sseEvent = "content_block_start"
				} else if strings.Contains(eventType, "stop") {
					sseEvent = "content_block_stop"
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEvent, transformedStr)
			} else {
				fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", transformedStr)
			}
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("读取流式响应失败", "error", err)
	}

	// 关闭仍打开的 content blocks
	if state.BlockIndex >= 0 {
		for i := 0; i <= state.BlockIndex; i++ {
			if !state.OpenBlocks[i] {
				continue
			}
			blockStop := map[string]any{"type": "content_block_stop", "index": i}
			blockStopJSON, _ := json.Marshal(blockStop)
			fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", blockStopJSON)
			flusher.Flush()
			state.OpenBlocks[i] = false
		}
	}

	// 发送 message_delta 事件
	msgDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": 0,
		},
	}
	msgDeltaJSON, _ := json.Marshal(msgDelta)
	fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", msgDeltaJSON)
	flusher.Flush()

	// 发送 message_stop 事件
	msgStop := map[string]any{"type": "message_stop"}
	msgStopJSON, _ := json.Marshal(msgStop)
	fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", msgStopJSON)
	flusher.Flush()
}

// handleStreamResponse 处理通用流式响应（非 Claude 客户端）
func (p *Proxy) handleStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain) {
	// 设置流式响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("ResponseWriter 不支持 Flusher")
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// 提取 data 内容，兼容 "data:" 和 "data: " 两种格式
		var data string
		if strings.HasPrefix(line, "data: ") {
			data = line[6:]
		} else if strings.HasPrefix(line, "data:") {
			data = line[5:]
		} else if line == "" {
			continue
		} else {
			// 其他行（如 event: 等），直接透传
			fmt.Fprintf(w, "%s\n", line)
			flusher.Flush()
			continue
		}

		// 检查流结束标记
		if data == "[DONE]" {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			continue
		}

		// 通过 Transformer 链转换 chunk
		transformed, err := chain.TransformStream(ctx, []byte(data))
		if err != nil {
			slog.Error("流式 chunk 转换失败", "error", err)
			continue
		}

		if transformed == nil {
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", string(transformed))
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		slog.Error("读取流式响应失败", "error", err)
	}
}

// handleNormalResponse 处理非流式响应
func (p *Proxy) handleNormalResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "读取上游响应失败: "+err.Error())
		return
	}

	slog.Debug("上游原始响应", "body", string(body))

	// 通过 Transformer 链转换响应
	transformed, err := chain.TransformResponse(ctx, body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "响应转换失败: "+err.Error())
		return
	}

	// 设置响应头
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(transformed)
}

// writeError 写入错误响应
func (p *Proxy) writeError(w http.ResponseWriter, status int, message string) {
	slog.Error("代理错误", "status", status, "message", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "proxy_error",
			"message": message,
		},
	})
}

// extractModel 从请求体中提取模型名
func extractModel(body []byte, path string) (string, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return "", fmt.Errorf("解析请求体 JSON 失败: %w", err)
	}

	model, ok := req["model"]
	if !ok {
		return "", fmt.Errorf("请求体中缺少 model 字段")
	}

	modelStr, ok := model.(string)
	if !ok {
		return "", fmt.Errorf("model 字段不是字符串类型")
	}

	return modelStr, nil
}
