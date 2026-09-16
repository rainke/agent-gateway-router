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
	Server    ServerConfig `mapstructure:"server"`
	Providers []Provider   `mapstructure:"providers"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
	PIDFile  string `mapstructure:"pid_file"`
}

// Provider 上游供应商配置
type Provider struct {
	Name       string   `mapstructure:"name"`
	APIBaseURL string   `mapstructure:"api_base_url"`
	APIKey     string   `mapstructure:"api_key"`
	Models     []string `mapstructure:"models"`
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

	// 解析 api_key 中的 env: 环境变量引用
	for i := range cfg.Providers {
		resolved, err := resolveEnvAPIKey(cfg.Providers[i].Name, cfg.Providers[i].APIKey)
		if err != nil {
			return nil, err
		}
		cfg.Providers[i].APIKey = resolved
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
		if strings.Contains(p.Name, "/") {
			return fmt.Errorf("配置错误: providers[%d].name 不能包含 /", i)
		}
		if _, exists := providerMap[p.Name]; exists {
			return fmt.Errorf("配置错误: providers.name 重复: %s", p.Name)
		}
		providerMap[p.Name] = p
	}

	return nil
}

// resolveEnvAPIKey 将 api_key 中 "env:VAR_NAME" 形式的值解析为环境变量值。
// 不以 "env:" 开头的 api_key 原样返回。
func resolveEnvAPIKey(providerName, apiKey string) (string, error) {
	const prefix = "env:"
	if !strings.HasPrefix(apiKey, prefix) {
		return apiKey, nil
	}
	varName := strings.TrimPrefix(apiKey, prefix)
	if varName == "" {
		return "", fmt.Errorf("配置错误: provider %s 的 api_key 使用了 env: 前缀但未指定环境变量名称", providerName)
	}
	value := os.Getenv(varName)
	if value == "" {
		return "", fmt.Errorf("配置错误: provider %s 的 api_key 引用的环境变量 %s 未设置或为空", providerName, varName)
	}
	return value, nil
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
