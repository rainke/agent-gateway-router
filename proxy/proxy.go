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
	"agr/models"
	"agr/router"
	"agr/transformer"
	"agr/transformer/anthropic"
	"agr/transformer/openai"
	"agr/transformer/tctx"
)

// chainHasAnthropic 判断 transformer 链中是否包含 anthropic transformer。
// 用于在 /v1/responses 流式场景中判断是否需要额外设置 anthropic 流式状态。
func chainHasAnthropic(chain *transformer.Chain) bool {
	if chain == nil {
		return false
	}
	for _, t := range chain.Transformers() {
		if _, ok := t.(*anthropic.Transformer); ok {
			return true
		}
	}
	return false
}

// providerHasAnthropicTransformer 判断 provider 的 transformer 配置中
// 是否包含 "anthropic"。用于 /v1/messages/count_tokens 决定是否代理到上游。
func providerHasAnthropicTransformer(provider *config.Provider) bool {
	if provider == nil {
		return false
	}
	for _, name := range provider.Transformer {
		if name == "anthropic" {
			return true
		}
	}
	return false
}

// findAnthropicProviderForModel 在所有 provider 中查找一个同时满足以下条件的 provider：
//   - transformer 链中包含 "anthropic"
//   - models 列表中包含指定的上游模型名
//
// 用于 /v1/messages/count_tokens：当主路由的 provider 不支持 anthropic transformer 时，
// 回退查找一个提供相同上游模型的 anthropic provider 来代理 count_tokens 请求。
// 找不到时返回 nil。
func (p *Proxy) findAnthropicProviderForModel(upstreamModel string) *config.Provider {
	for i := range p.cfg.Providers {
		prov := &p.cfg.Providers[i]
		if !providerHasAnthropicTransformer(prov) {
			continue
		}
		for _, m := range prov.Models {
			if m == upstreamModel {
				return prov
			}
		}
	}
	return nil
}

// proxyCountTokensUpstream 将 count_tokens 请求代理到上游
// {provider.APIBaseURL}/count_tokens 端点，并原样透传上游响应。
func (p *Proxy) proxyCountTokensUpstream(w http.ResponseWriter, r *http.Request, body []byte, provider *config.Provider, upstreamModel string) {
	// 构建上游 count_tokens URL：在 api_base_url 后追加 /count_tokens
	upstreamURL := strings.TrimRight(provider.APIBaseURL, "/") + "/count_tokens"

	ctx := r.Context()

	// 将请求体中的 model 替换为路由后的上游模型名，使上游能识别
	transformedBody, err := replaceModelInBody(body, upstreamModel)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "替换模型名失败: "+err.Error())
		return
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, strings.NewReader(string(transformedBody)))
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "构建上游请求失败: "+err.Error())
		return
	}

	upstreamReq.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}

	slog.Info("代理 count_tokens 请求", "upstream_url", upstreamURL, "provider", provider.Name, "model", upstreamModel)

	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "请求上游失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "读取上游响应失败: "+err.Error())
		return
	}

	// 透传上游状态码和响应体
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// replaceModelInBody 将请求体 JSON 中的 model 字段替换为上游模型名。
func replaceModelInBody(body []byte, upstreamModel string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析请求体 JSON 失败: %w", err)
	}
	req["model"] = upstreamModel
	return json.Marshal(req)
}

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
//
// 路由策略：
//   - 优先使用主路由的 provider：如果它的 transformer 链中包含 anthropic，则代理到上游
//     {api_base_url}/count_tokens 端点。
//   - 否则在所有 provider 中查找一个同时满足 "使用 anthropic transformer" 且 "提供相同
//     上游模型" 的 provider，代理到它的 {api_base_url}/count_tokens 端点。
//   - 如果找不到任何 anthropic provider，则使用本地 tiktoken 估算 input_tokens。
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

	// 确定用于 count_tokens 代理的 provider 和上游模型：
	// 1. 主路由 provider 支持 anthropic → 直接使用
	// 2. 否则回退查找提供相同上游模型的 anthropic provider
	var countProvider *config.Provider = result.Provider
	upstreamModel := result.Model
	if !providerHasAnthropicTransformer(countProvider) {
		if fallback := p.findAnthropicProviderForModel(upstreamModel); fallback != nil {
			countProvider = fallback
		}
	}

	// 如果选定的 provider 使用 anthropic transformer，代理到上游 count_tokens 端点
	if providerHasAnthropicTransformer(countProvider) {
		p.proxyCountTokensUpstream(w, r, body, countProvider, upstreamModel)
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

// HandleModels 处理 /v1/models 请求，返回模型元数据列表
func (p *Proxy) HandleModels(w http.ResponseWriter, r *http.Request) {
	slog.Info("收到 models 请求", "method", r.Method, "path", r.URL.Path)
	resp, err := models.LoadModels(p.cfg)
	if err != nil {
		slog.Error("加载模型列表失败", "error", err)
		p.writeError(w, http.StatusInternalServerError, "加载模型列表失败: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	ctx := context.WithValue(r.Context(), tctx.RequestPathKey, path)
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, result.Model)
	ctx = context.WithValue(ctx, tctx.ClientModelKey, clientModel)
	ctx = context.WithValue(ctx, tctx.RequestMetadataKey, &tctx.RequestMetadata{})

	go func() {
		<-ctx.Done()
		if ctx.Err() == context.Canceled {
			slog.Warn("客户端取消了请求",
				"path", path,
				"model", clientModel,
				"provider", result.Provider.Name,
				"reason", "客户端主动断开连接",
			)
		}
	}()

	slog.Debug("转换前的请求体", "body", string(body))

	// 执行请求转换
	transformedBody, err := chain.TransformRequest(ctx, body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "请求转换失败: "+err.Error())
		return
	}

	slog.Debug("转换后的请求体", "body", string(transformedBody))

	// 只在 Responses API 路径下检查 reasoning 标记
	if strings.Contains(path, "/v1/responses") {
		markReasoningFromTransformedBody(ctx, transformedBody)
	}

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

	providerName := result.Provider.Name
	upstreamModel := result.Model

	if isStream {
		// 根据客户端协议选择不同的流式响应处理
		if strings.Contains(path, "/v1/messages") {
			p.handleClaudeStreamResponse(ctx, w, resp, chain, clientModel, providerName, upstreamModel)
		} else if strings.Contains(path, "/v1/responses") {
			p.handleCodexStreamResponse(ctx, w, resp, chain, clientModel, providerName, upstreamModel)
		} else {
			p.handleStreamResponse(ctx, w, resp, chain, providerName, upstreamModel)
		}
	} else {
		p.handleNormalResponse(ctx, w, resp, chain, providerName, upstreamModel)
	}
}

func writeReasoningHeader(ctx context.Context, w http.ResponseWriter) {
	metadata, ok := ctx.Value(tctx.RequestMetadataKey).(*tctx.RequestMetadata)
	if !ok || metadata == nil || !metadata.ReasoningIncluded {
		return
	}
	w.Header().Set("x-reasoning-included", "true")
}

func markReasoningFromTransformedBody(ctx context.Context, body []byte) {
	metadata, ok := ctx.Value(tctx.RequestMetadataKey).(*tctx.RequestMetadata)
	if !ok || metadata == nil || metadata.ReasoningIncluded {
		return
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return
	}

	msgs, _ := req["messages"].([]any)
	for _, msg := range msgs {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		if reasoning, ok := m["reasoning_content"].(string); ok && reasoning != "" {
			metadata.ReasoningIncluded = true
			return
		}
	}
}

// handleClaudeStreamResponse 处理 Claude 客户端的流式响应，输出 Anthropic SSE 格式
func (p *Proxy) handleClaudeStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain, clientModel, providerName, upstreamModel string) {
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
	state := &tctx.StreamState{BlockIndex: -1, OpenBlocks: make(map[int]bool)}
	ctx = context.WithValue(ctx, tctx.StreamStateKey, state)

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

	var lastUsage map[string]any

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

		// 提取上游 usage（在 transformer 转换之前）
		extractAndRecordUsageFromChunk([]byte(data), providerName, upstreamModel, &lastUsage)

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

		// 记录转换后的 SSE chunk
		slog.Debug("转换后的 SSE chunk (claude)", "data", string(transformed))

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
			"input_tokens":  state.InputTokens,
			"output_tokens": state.OutputTokens,
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

	// 记录 usage
	flushUsageRecord(lastUsage, providerName, upstreamModel)
}

// handleCodexStreamResponse 处理 Codex (Responses API) 客户端的流式响应
func (p *Proxy) handleCodexStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain, clientModel, providerName, upstreamModel string) {
	// 设置流式响应头
	writeReasoningHeader(ctx, w)
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

	// 创建 Codex 流式状态
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	state := &openai.CodexStreamState{
		ResponseID: responseID,
		Model:      clientModel,
	}
	ctx = context.WithValue(ctx, openai.CodexStreamStateKey, state)

	// 如果 chain 中包含 anthropic transformer，也创建其专属状态
	// （anthropic 转换器需要自己的 StreamState 以追踪 thinking/tool_calls 等）
	if chainHasAnthropic(chain) {
		anthState := &anthropic.StreamState{
			ResponseID: responseID,
			Model:      clientModel,
		}
		ctx = context.WithValue(ctx, anthropic.StreamStateContextKey, anthState)
	}

	// 发送 response.created 事件
	createdEvent := openai.BuildCodexCreatedEvent(state)
	createdJSON, _ := json.Marshal(createdEvent)
	fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", createdJSON)
	flusher.Flush()

	// 发送 response.in_progress 事件
	inProgressEvent := openai.BuildCodexInProgressEvent(state)
	inProgressJSON, _ := json.Marshal(inProgressEvent)
	fmt.Fprintf(w, "event: response.in_progress\ndata: %s\n\n", inProgressJSON)
	flusher.Flush()

	// 读取上游流式响应并转换
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastUsage map[string]any

	for scanner.Scan() {
		line := scanner.Text()

		// 提取 data 内容
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

		slog.Debug("上游原始 SSE chunk (codex)", "data", data)

		// 提取上游 usage（在 transformer 转换之前）
		extractAndRecordUsageFromChunk([]byte(data), providerName, upstreamModel, &lastUsage)

		// 通过 Transformer 链转换 chunk，得到多个 Responses API 事件
		events, err := chain.TransformCodexStream(ctx, []byte(data))
		if err != nil {
			slog.Error("Codex 流式 chunk 转换失败", "error", err)
			continue
		}

		// 输出每个事件为独立的 SSE
		for _, eventData := range events {
			// 从事件 JSON 中提取 type 作为 SSE event name
			var evt map[string]any
			if err := json.Unmarshal(eventData, &evt); err != nil {
				continue
			}
			eventType, _ := evt["type"].(string)
			if eventType == "" {
				continue
			}
			slog.Debug("转换后的 SSE chunk (codex)", "event", eventType, "data", string(eventData))
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, eventData)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("读取流式响应失败 (codex)", "error", err)
	}

	// 兜底：如果流已结束但 response.completed 尚未发送（provider 没有返回单独的 usage chunk）
	if state.Finished {
		state.SequenceNumber++
		completed := openai.BuildCodexFinalCompletedEvent(state)
		completedJSON, _ := json.Marshal(completed)
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", completedJSON)
		flusher.Flush()
		state.Finished = false
	}

	// 记录 usage
	flushUsageRecord(lastUsage, providerName, upstreamModel)

	// 发送 [DONE] 标记
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleStreamResponse 处理通用流式响应（非 Claude 客户端）
func (p *Proxy) handleStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain, providerName, upstreamModel string) {
	// 设置流式响应头
	writeReasoningHeader(ctx, w)
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

	var lastUsage map[string]any

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

		// 提取上游 usage（在 transformer 转换之前）
		if data != "[DONE]" {
			extractAndRecordUsageFromChunk([]byte(data), providerName, upstreamModel, &lastUsage)
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

		slog.Debug("转换后的 SSE chunk (generic)", "data", string(transformed))
		fmt.Fprintf(w, "data: %s\n\n", string(transformed))
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		slog.Error("读取流式响应失败", "error", err)
	}

	// 记录 usage
	flushUsageRecord(lastUsage, providerName, upstreamModel)
}

// handleNormalResponse 处理非流式响应
func (p *Proxy) handleNormalResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, chain *transformer.Chain, providerName, upstreamModel string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "读取上游响应失败: "+err.Error())
		return
	}

	slog.Debug("上游原始响应", "body", string(body))

	// 提取上游 usage（在 transformer 转换之前，兼容 OpenAI 和 Anthropic 格式）
	extractUsageFromBody(body, providerName, upstreamModel)

	// 通过 Transformer 链转换响应
	transformed, err := chain.TransformResponse(ctx, body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "响应转换失败: "+err.Error())
		return
	}

	// 设置响应头
	writeReasoningHeader(ctx, w)
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
