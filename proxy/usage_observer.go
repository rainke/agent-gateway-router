package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
)

// 统计只旁路观察响应；超过上限时跳过统计，转发不受影响。
const maxUsageBuffer = 4 << 20

type usageObserver struct {
	bytesRead, events, skippedEvents int
	endReason                        string
	io.ReadCloser
	provider, model                                 string
	stream                                          bool
	buffer                                          []byte
	event                                           []byte
	skippingLine, skippingEvent, overflow, finished bool
	lastUsage                                       map[string]any
}

func (o *usageObserver) Read(p []byte) (int, error) {
	n, err := o.ReadCloser.Read(p)
	if o.stream {
		o.bytesRead += n
		if err == io.EOF {
			o.endReason = "eof"
		} else if err != nil {
			o.endReason = "read_error"
		}
		o.observeStream(p[:n])
	} else if !o.overflow {
		if len(o.buffer)+n > maxUsageBuffer {
			o.buffer = nil
			o.overflow = true
		} else {
			o.buffer = append(o.buffer, p[:n]...)
		}
	}
	if err == io.EOF {
		o.finish()
	}
	return n, err
}

func (o *usageObserver) Close() error {
	// 不把中断的普通 JSON 当作完整响应；保留已收到的流式 usage。
	if o.stream {
		o.finish()
	}
	return o.ReadCloser.Close()
}

func (o *usageObserver) observeStream(data []byte) {
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		part := data
		if end >= 0 {
			part = data[:end]
		}
		if !o.skippingLine {
			if len(o.buffer)+len(part) > maxUsageBuffer {
				o.buffer = nil
				o.skippingLine = true
				o.skippingEvent = true
				o.event = nil
			} else {
				o.buffer = append(o.buffer, part...)
			}
		}
		if end < 0 {
			return
		}
		if !o.skippingLine {
			o.line(strings.TrimSuffix(string(o.buffer), "\r"))
		}
		o.buffer = o.buffer[:0]
		o.skippingLine = false
		data = data[end+1:]
	}
}

func (o *usageObserver) line(line string) {
	if line == "" {
		if !o.skippingEvent && len(o.event) > 0 {
			o.events++
			extractAndRecordUsageFromChunk(o.event, o.provider, o.model, &o.lastUsage)
			o.logEvent()
		}
		if o.skippingEvent {
			o.skippedEvents++
		}
		o.event = o.event[:0]
		o.skippingEvent = false
		return
	}
	if o.skippingEvent || !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
	if len(o.event)+len(data)+1 > maxUsageBuffer {
		o.event = nil
		o.skippingEvent = true
		return
	}
	o.event = append(o.event, data...)
	o.event = append(o.event, '\n')
}

func (o *usageObserver) finish() {
	if o.finished {
		return
	}
	o.finished = true
	if o.stream {
		flushUsageRecord(o.lastUsage, o.provider, o.model)
		if o.endReason == "" {
			o.endReason = "closed"
		}
		record := extractUsageFromMap(o.lastUsage, o.provider, o.model)
		slog.Info("流式响应结束", "provider", o.provider, "model", o.model,
			"reason", o.endReason, "bytes", o.bytesRead, "events", o.events,
			"skipped_events", o.skippedEvents, "incomplete_event", len(o.buffer) > 0 || len(o.event) > 0 || o.skippingEvent,
			"usage_found", o.lastUsage != nil, "input_tokens", record.InputTokens,
			"output_tokens", record.OutputTokens, "cached_tokens", record.CachedTokens,
			"reasoning_tokens", record.OutputReasoningTokens, "total_tokens", record.TotalTokens)
	} else if !o.overflow {
		extractUsageFromBody(o.buffer, o.provider, o.model)
	}
}

// 只记录固定事件类型和计数，避免把上游正文或任意字段写入日志。
func (o *usageObserver) logEvent() {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	kind := "unknown"
	if bytes.Equal(bytes.TrimSpace(o.event), []byte("[DONE]")) {
		kind = "done"
	} else {
		var event struct {
			Type    string          `json:"type"`
			Choices json.RawMessage `json:"choices"`
		}
		if json.Unmarshal(o.event, &event) != nil {
			kind = "invalid_json"
		} else {
			switch event.Type {
			case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "ping", "error", "response.created", "response.in_progress", "response.completed", "response.failed", "response.incomplete", "response.output_text.delta", "response.output_text.done":
				kind = event.Type
			default:
				if len(event.Choices) > 0 {
					kind = "chat.completion.chunk"
				}
			}
		}
	}
	slog.Debug("流式响应事件", "provider", o.provider, "model", o.model, "event_index", o.events, "event_type", kind, "data_bytes", len(o.event), "usage_found", o.lastUsage != nil)
}
