package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config 是 agr 的顶层配置结构
type Config struct {
	Server    ServerConfig      `mapstructure:"server"`
	Providers []Provider        `mapstructure:"providers"`
	Router    map[string]string `mapstructure:"router"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
	PIDFile  string `mapstructure:"pid_file"`
}

// Provider 上游供应商配置
type Provider struct {
	Name        string   `mapstructure:"name"`
	APIBaseURL  string   `mapstructure:"api_base_url"`
	APIKey      string   `mapstructure:"api_key"`
	Models      []string `mapstructure:"models"`
	Transformer []string `mapstructure:"transformer"`
}

// Load 加载并校验配置文件
func Load(configPath string) (*Config, error) {
	v := viper.NewWithOptions(viper.KeyDelimiter("::"))
	v.SetConfigType("toml")

	if configPath != "" {
		v.SetConfigFile(expandPath(configPath))
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("获取用户目录失败: %w", err)
		}
		v.SetConfigFile(filepath.Join(home, ".agr", "config.toml"))
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 展开路径中的 ~
	cfg.Server.PIDFile = expandPath(cfg.Server.PIDFile)

	// 校验配置
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate 校验配置合法性
func validate(cfg *Config) error {
	// 校验端口
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("配置错误: server.port 必须在 1-65535 之间，当前值: %d", cfg.Server.Port)
	}

	// 校验日志级别（优先级顺序：debug > trace > info > warn > error）
	validLevels := map[string]bool{"debug": true, "trace": true, "info": true, "warn": true, "error": true}
	if cfg.Server.LogLevel != "" && !validLevels[cfg.Server.LogLevel] {
		return fmt.Errorf("配置错误: server.log_level 必须是 debug/trace/info/warn/error 之一，当前值: %s", cfg.Server.LogLevel)
	}

	// 校验 Provider 名称唯一性
	providerMap := make(map[string]*Provider)
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Name == "" {
			return fmt.Errorf("配置错误: providers[%d].name 不能为空", i)
		}
		if _, exists := providerMap[p.Name]; exists {
			return fmt.Errorf("配置错误: providers.name 重复: %s", p.Name)
		}
		providerMap[p.Name] = p

		// 校验 Transformer 名称
		for _, t := range p.Transformer {
			if !IsValidTransformer(t) {
				return fmt.Errorf("配置错误: provider %s 引用了未知的 transformer: %s", p.Name, t)
			}
		}
	}

	// 校验路由映射
	for model, route := range cfg.Router {
		if model == "default" {
			// default 路由也需要校验
		}
		parts := strings.SplitN(route, ",", 2)
		if len(parts) != 2 {
			return fmt.Errorf("配置错误: router.%s 格式错误，应为 'provider_name,model_name'，当前值: %s", model, route)
		}
		providerName := strings.TrimSpace(parts[0])
		modelName := strings.TrimSpace(parts[1])

		provider, exists := providerMap[providerName]
		if !exists {
			return fmt.Errorf("配置错误: router.%s 引用了不存在的 provider: %s", model, providerName)
		}

		// 校验模型是否在 Provider 的 models 列表中
		found := false
		for _, m := range provider.Models {
			if m == modelName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("配置错误: router.%s 引用的模型 %s 不在 provider %s 的 models 列表中", model, modelName, providerName)
		}
	}

	return nil
}

// IsValidTransformer 检查 Transformer 名称是否在注册表中
func IsValidTransformer(name string) bool {
	// 内置 Transformer 注册表
	registry := map[string]bool{
		"openai":           true,
		"deepseek":         true,
		"anthropic":        true,
		"openai-responses": true,
	}
	return registry[name]
}

// expandPath 展开路径中的 ~ 为用户目录
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
