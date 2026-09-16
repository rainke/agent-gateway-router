package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 辅助函数：创建临时 TOML 配置文件
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "test-provider"
api_base_url = "http://localhost:8000"
api_key = "sk-test"
models = ["model-a", "model-b"]

[router]
default = "test-provider,model-a"
my-model = "test-provider,model-b"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载有效配置失败: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("端口期望 8080，实际 %d", cfg.Server.Port)
	}
	if cfg.Server.LogLevel != "info" {
		t.Errorf("日志级别期望 info，实际 %s", cfg.Server.LogLevel)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("Provider 数量期望 1，实际 %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "test-provider" {
		t.Errorf("Provider 名称期望 test-provider，实际 %s", cfg.Providers[0].Name)
	}
	if cfg.Providers[0].APIBaseURL != "http://localhost:8000" {
		t.Errorf("API Base URL 不匹配")
	}
	if len(cfg.Providers[0].Models) != 2 {
		t.Errorf("模型数量期望 2，实际 %d", len(cfg.Providers[0].Models))
	}
	if cfg.Router["default"] != "test-provider,model-a" {
		t.Errorf("默认路由不匹配")
	}
	if cfg.Router["my-model"] != "test-provider,model-b" {
		t.Errorf("模型路由不匹配")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("期望加载不存在的文件时返回错误")
	}
}

func TestLoad_InvalidPort_Zero(t *testing.T) {
	content := `
[server]
port = 0
log_level = "info"
pid_file = "/tmp/test.pid"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望端口为 0 时返回错误")
	}
}

func TestLoad_InvalidPort_TooLarge(t *testing.T) {
	content := `
[server]
port = 99999
log_level = "info"
pid_file = "/tmp/test.pid"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望端口超出范围时返回错误")
	}
}

func TestLoad_ValidLogLevel_Trace(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "trace"
pid_file = "/tmp/test.pid"

[[providers]]
name = "test-provider"
api_base_url = "http://localhost:8000"
api_key = "sk-test"
models = ["model-a"]

[router]
default = "test-provider,model-a"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("trace 日志级别应该通过校验: %v", err)
	}
	if cfg.Server.LogLevel != "trace" {
		t.Errorf("日志级别期望 trace，实际 %s", cfg.Server.LogLevel)
	}
}

func TestLoad_InvalidLogLevel_ErrorMentionsTrace(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "verbose"
pid_file = "/tmp/test.pid"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望无效日志级别时返回错误")
	}
	if !strings.Contains(err.Error(), "trace") {
		t.Errorf("错误信息应包含可选级别 trace，实际: %v", err)
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "verbose"
pid_file = "/tmp/test.pid"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望无效日志级别时返回错误")
	}
}

func TestLoad_EmptyLogLevel(t *testing.T) {
	content := `
[server]
port = 8080
log_level = ""
pid_file = "/tmp/test.pid"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("空日志级别应该通过校验: %v", err)
	}
}

func TestLoad_DuplicateProviderName(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "dup"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[[providers]]
name = "dup"
api_base_url = "http://b.com"
api_key = "sk-2"
models = ["m2"]

[router]
default = "dup,m1"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望重复 Provider 名称时返回错误")
	}
}

func TestLoad_EmptyProviderName(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = ""
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[router]
default = ",m1"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望空 Provider 名称时返回错误")
	}
}

func TestLoad_IgnoresLegacyTransformer(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "p1"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]
transformer = ["nonexistent-transformer"]

[router]
default = "p1,m1"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err != nil {
		t.Fatalf("旧 transformer 配置应被忽略: %v", err)
	}
}

func TestLoad_RouterReferencesNonexistentProvider(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "p1"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[router]
default = "nonexistent,m1"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望路由引用不存在的 Provider 时返回错误")
	}
}

func TestLoad_RouterReferencesNonexistentModel(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "p1"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[router]
default = "p1,nonexistent-model"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望路由引用不存在的模型时返回错误")
	}
}

func TestLoad_RouterInvalidFormat(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "p1"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[router]
default = "invalid-format-no-comma"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望路由格式错误时返回错误")
	}
}

func TestLoad_ExpandTildePath(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "~/test/agr.pid"

[[providers]]
name = "p1"
api_base_url = "http://a.com"
api_key = "sk-1"
models = ["m1"]

[router]
default = "p1,m1"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "test/agr.pid")
	if cfg.Server.PIDFile != expected {
		t.Errorf("PID 文件路径展开错误，期望 %s，实际 %s", expected, cfg.Server.PIDFile)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", filepath.Join(home, "test")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~notexpand", "~notexpand"},
	}

	for _, tt := range tests {
		result := expandPath(tt.input)
		if result != tt.expected {
			t.Errorf("expandPath(%q) = %q，期望 %q", tt.input, result, tt.expected)
		}
	}
}

func TestLoad_MultipleProviders(t *testing.T) {
	content := `
[server]
port = 9999
log_level = "debug"
pid_file = "/tmp/test.pid"

[[providers]]
name = "provider-a"
api_base_url = "http://a.com/v1"
api_key = "sk-a"
models = ["model-1", "model-2"]

[[providers]]
name = "provider-b"
api_base_url = "http://b.com/v1"
api_key = "sk-b"
models = ["model-3"]

[router]
default = "provider-a,model-1"
special = "provider-b,model-3"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载多 Provider 配置失败: %v", err)
	}

	if len(cfg.Providers) != 2 {
		t.Errorf("Provider 数量期望 2，实际 %d", len(cfg.Providers))
	}
}

func TestLoad_APIKeyFromEnv(t *testing.T) {
	const envVar = "AGR_TEST_API_KEY"
	t.Setenv(envVar, "sk-env-secret")

	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "env-provider"
api_base_url = "http://localhost:8000"
api_key = "env:` + envVar + `"
models = ["model-a"]

[router]
default = "env-provider,model-a"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载 env api_key 配置失败: %v", err)
	}
	if cfg.Providers[0].APIKey != "sk-env-secret" {
		t.Errorf("api_key 期望从环境变量解析为 sk-env-secret，实际 %q", cfg.Providers[0].APIKey)
	}
}

func TestLoad_APIKeyFromEnv_Missing(t *testing.T) {
	const envVar = "AGR_TEST_API_KEY_UNSET"
	t.Setenv(envVar, "")

	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "env-provider"
api_base_url = "http://localhost:8000"
api_key = "env:` + envVar + `"
models = ["model-a"]

[router]
default = "env-provider,model-a"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望环境变量未设置时返回错误")
	}
	if !strings.Contains(err.Error(), "env-provider") || !strings.Contains(err.Error(), envVar) {
		t.Errorf("错误信息应包含 provider 名称和环境变量名称，实际: %v", err)
	}
}

func TestLoad_APIKeyFromEnv_EmptyVarName(t *testing.T) {
	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "env-provider"
api_base_url = "http://localhost:8000"
api_key = "env:"
models = ["model-a"]

[router]
default = "env-provider,model-a"
`
	path := writeTempConfig(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("期望 env: 未指定环境变量名称时返回错误")
	}
}

func TestLoad_APIKeyFromEnv_MixedProviders(t *testing.T) {
	const envVar = "AGR_TEST_API_KEY_MIXED"
	t.Setenv(envVar, "sk-env-mixed")

	content := `
[server]
port = 8080
log_level = "info"
pid_file = "/tmp/test.pid"

[[providers]]
name = "plain-provider"
api_base_url = "http://a.com"
api_key = "sk-plain"
models = ["model-a"]

[[providers]]
name = "env-provider"
api_base_url = "http://b.com"
api_key = "env:` + envVar + `"
models = ["model-b"]

[router]
default = "plain-provider,model-a"
special = "env-provider,model-b"
`
	path := writeTempConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载混合 api_key 配置失败: %v", err)
	}
	if cfg.Providers[0].APIKey != "sk-plain" {
		t.Errorf("普通 api_key 期望保持 sk-plain，实际 %q", cfg.Providers[0].APIKey)
	}
	if cfg.Providers[1].APIKey != "sk-env-mixed" {
		t.Errorf("env api_key 期望解析为 sk-env-mixed，实际 %q", cfg.Providers[1].APIKey)
	}
}
