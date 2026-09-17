package config

import (
	"os"
	"testing"
)

func TestApplyEnv_Aliases(t *testing.T) {
	os.Setenv("PORT", "19090")
	os.Setenv("HOST", "127.0.0.2")
	os.Setenv("PRISM_COOKIE", "cookie-from-env")
	os.Setenv("PROXY_API_KEY", "sk-proxy-alias")
	os.Setenv("CORS_ORIGIN", "https://example.com")
	os.Setenv("PRISM_MODEL", "gpt-5.6-sol-custom")
	os.Setenv("PRISM_BASE_URL", "https://prism.custom.domain")

	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("HOST")
		os.Unsetenv("PRISM_COOKIE")
		os.Unsetenv("PROXY_API_KEY")
		os.Unsetenv("CORS_ORIGIN")
		os.Unsetenv("PRISM_MODEL")
		os.Unsetenv("PRISM_BASE_URL")
	}()

	cfg := Default()
	applyEnv(cfg)

	if cfg.Server.Port != 19090 {
		t.Errorf("PORT 别名未生效: %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.2" {
		t.Errorf("HOST 别名未生效: %q", cfg.Server.Host)
	}
	if cfg.Upstream.BaseURL != "https://prism.custom.domain" {
		t.Errorf("PRISM_BASE_URL 别名未生效: %q", cfg.Upstream.BaseURL)
	}
	if len(cfg.Facade.APIKeys) != 1 || cfg.Facade.APIKeys[0] != "sk-proxy-alias" {
		t.Errorf("PROXY_API_KEY 别名未生效: %+v", cfg.Facade.APIKeys)
	}
	if cfg.Server.CORSOrigin != "https://example.com" {
		t.Errorf("CORS_ORIGIN 别名未生效: %q", cfg.Server.CORSOrigin)
	}
	if cfg.Facade.DefaultModel != "gpt-5.6-sol-custom" {
		t.Errorf("PRISM_MODEL 别名未生效: %q", cfg.Facade.DefaultModel)
	}
	if len(cfg.Creds.Accounts) == 0 || cfg.Creds.Accounts[0].Cookies != "cookie-from-env" {
		t.Errorf("PRISM_COOKIE 别名未生效: %+v", cfg.Creds.Accounts)
	}
}
