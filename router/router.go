package router

import (
	"fmt"
	"strings"

	"agr/config"
)

// RouteResult 路由结果
type RouteResult struct {
	Provider *config.Provider
	Model    string
}

// Router 模型路由器
type Router struct {
	// providerMap 按名称索引 Provider
	providerMap map[string]*config.Provider
}

// New 创建路由器实例
func New(cfg *config.Config) *Router {
	pm := make(map[string]*config.Provider)
	for i := range cfg.Providers {
		pm[cfg.Providers[i].Name] = &cfg.Providers[i]
	}
	return &Router{
		providerMap: pm,
	}
}

// Route 根据客户端请求的模型名查找目标 Provider 和真实模型
func (r *Router) Route(clientModel string) (*RouteResult, error) {
	providerName, modelName, ok := strings.Cut(clientModel, "/")
	if !ok || providerName == "" || modelName == "" {
		return nil, fmt.Errorf("模型名 %q 格式错误，应为 <provider>/<model>", clientModel)
	}

	provider, exists := r.providerMap[providerName]
	if !exists {
		return nil, fmt.Errorf("路由引用了不存在的 provider: %s", providerName)
	}

	found := false
	for _, model := range provider.Models {
		if model == modelName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("模型 %s 不在 provider %s 的 models 列表中", modelName, providerName)
	}

	return &RouteResult{
		Provider: provider,
		Model:    modelName,
	}, nil
}
