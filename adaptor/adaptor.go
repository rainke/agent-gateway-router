// Package adaptor 提供按提供商配置启用的请求适配器。
// 请求体在代理入口解析一次为 map，整条链路共享该引用，结束后再序列化。
package adaptor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Request 是适配器链共享的可变请求。Body 只解析一次，保留原始 JSON 值。
// Headers 使用 map[string][]string 保留多值头；Path 是不含查询串的 API 路径，
// 相对于提供商的 APIBaseURL。RawQuery 保留原始查询串编码。
type Request struct {
	Body     map[string]json.RawMessage
	Headers  http.Header
	Path     string
	Method   string
	RawQuery string
}

// Adaptor 在已解析的请求对象上改写字段，以适配上游协议差异。
// req 由调用方持有并在多层 adaptor 之间传递，实现应原地修改。
type Adaptor interface {
	Apply(req *Request) error
}

var registry = map[string]Adaptor{}

// Register 注册命名适配器，同名覆盖。名称统一小写存储。
func Register(name string, a Adaptor) {
	registry[strings.ToLower(name)] = a
}

// Known 判断名称是否已注册。
func Known(name string) bool {
	_, ok := registry[strings.ToLower(name)]
	return ok
}

// Names 返回已注册适配器名称（排序后）。
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Apply 按 names 顺序在同一 req 上应用适配器；names 为空时直接返回。
// 未知名称返回错误，避免配置拼写错误被静默忽略。
func Apply(req *Request, names []string) error {
	for _, name := range names {
		a, ok := registry[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("未知 adaptor: %s（可用: %s）", name, strings.Join(Names(), ", "))
		}
		if err := a.Apply(req); err != nil {
			return fmt.Errorf("adaptor %s: %w", name, err)
		}
	}
	return nil
}
