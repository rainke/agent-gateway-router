package proxy

import (
	"bytes"
	"io"
	"strings"
)

// 统计只旁路观察响应；超过上限时跳过统计，转发不受影响。
const maxUsageBuffer = 4 << 20

type usageObserver struct {
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
			extractAndRecordUsageFromChunk(o.event, o.provider, o.model, &o.lastUsage)
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
	} else if !o.overflow {
		extractUsageFromBody(o.buffer, o.provider, o.model)
	}
}
