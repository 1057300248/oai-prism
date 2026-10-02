package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/oai-prism/oaiprism/internal/bridge"
)

// cmdTLSBridge 启动 Sentinel token-oracle 架构的 Go 传输桥。
//
// 用法：oaiprism tlsbridge -port 8790 -oracle http://127.0.0.1:8791 -accounts secrets/accounts.json
func cmdTLSBridge(args []string) error {
	fs := flag.NewFlagSet("tlsbridge", flag.ContinueOnError)
	port := fs.Int("port", 8790, "本地监听端口（网关 upstream.base_url 指向它）")
	oracle := fs.String("oracle", "http://127.0.0.1:8791", "Sentinel token oracle 地址")
	accounts := fs.String("accounts", "secrets/accounts.json", "账号凭据文件（取 access_token）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := os.ReadFile(*accounts)
	if err != nil {
		return fmt.Errorf("读凭据失败: %w", err)
	}
	var accts struct {
		Accounts []struct {
			Cookies string `json:"cookies"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &accts); err != nil || len(accts.Accounts) == 0 {
		return fmt.Errorf("解析凭据失败: %w", err)
	}
	cookies := accts.Accounts[0].Cookies
	re := regexp.MustCompile(`prism_oai_access_token=([^;\s]+)`)
	m := re.FindStringSubmatch(cookies)
	if len(m) < 2 {
		return fmt.Errorf("凭据中未找到 prism_oai_access_token")
	}

	b, err := bridge.New(*oracle, m[1], false)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           b,
		ReadHeaderTimeout: 30 * time.Second,
	}
	fmt.Printf("[tlsbridge] 监听 127.0.0.1:%d → %s（oracle=%s）\n", *port, "https://prism.openai.com", *oracle)
	return srv.ListenAndServe()
}
