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
	cfg *config.Config
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
		cfg:         cfg,
		providerMap: pm,
	}
}

// Route 根据客户端请求的模型名查找目标 Provider 和真实模型
func (r *Router) Route(clientModel string) (*RouteResult, error) {
	// 先精确匹配
	route, ok := r.cfg.Router[clientModel]
	if !ok {
		// 使用默认路由
		route, ok = r.cfg.Router["default"]
		if !ok {
			return nil, fmt.Errorf("未找到模型 %s 的路由映射，且未配置默认路由", clientModel)
		}
	}

	parts := strings.SplitN(route, ",", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("路由配置格式错误: %s", route)
	}

	providerName := strings.TrimSpace(parts[0])
	modelName := strings.TrimSpace(parts[1])

	provider, exists := r.providerMap[providerName]
	if !exists {
		return nil, fmt.Errorf("路由引用了不存在的 provider: %s", providerName)
	}

	return &RouteResult{
		Provider: provider,
		Model:    modelName,
	}, nil
}
