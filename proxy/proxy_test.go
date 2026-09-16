package proxy

import (
	"os"
	"path/filepath"
	"testing"

	"agr/config"
	"agr/router"
)

func TestMain(m *testing.M) {
	// 全局设置 usageDir 为临时目录，避免测试写入生产 ~/.agr/usage/
	dir := filepath.Join("/tmp", "agr-usage-test", "global")
	os.RemoveAll(dir)
	usageDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newTestProxy(upstreamURL string) *Proxy {
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:       "test-provider",
				APIBaseURL: upstreamURL,
				APIKey:     "sk-test",
				Models:     []string{"model-a"},
			},
		},
	}
	r := router.New(cfg)
	return New(cfg, r)
}
