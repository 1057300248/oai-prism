// tlsbridge —— Sentinel token-oracle 架构的 Go 传输桥。
//
// 架构（2026-10-02）：
//
//	codex/网关 --HTTP--> tlsbridge:8790 --TLS(Chrome指纹)--> prism.openai.com
//	                        |
//	                        +--> oracle:8791（浏览器只执行 SentinelSDK.token()，不碰数据面）
//
// 职责边界：
//   - 从 oracle 取签名 token（每次请求一个，token 一次性）
//   - 自持会话：用 access_token 定期经 /auth/session 换发 session cookie
//   - 用 bogdanfinn/tls-client（Chrome_152 全套指纹，实测 200）转发全部流量
//
// 为什么引入 tls-client：Cloudflare 校验 JA3/JA4，标准库 TLS 无法伪造——
// 这是破坏"依赖数=1"约定的充分理由（决策记录见 tools/sentinel/README.md）。
//
// 用法：oaiprism tlsbridge -port 8790 -oracle http://127.0.0.1:8791
package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const upstream = "https://prism.openai.com"

// Bridge 持有 TLS 客户端、会话 cookie 缓存与 oracle 地址。
type Bridge struct {
	client tls_client.HttpClient
	oracle string
	ua     string
	mu     sync.Mutex
	cookie string // 完整 cookie 串（含 access_token + 新鲜 session）
	sessAt time.Time
	log    *log.Logger
}

// New 创建桥并完成首次会话换发。
func New(oracle, accessToken string, verbose bool) (*Bridge, error) {
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(180),
		tls_client.WithClientProfile(profiles.Chrome_152),
	)
	if err != nil {
		return nil, fmt.Errorf("tls-client 创建失败: %w", err)
	}
	b := &Bridge{
		client: client,
		oracle: strings.TrimRight(oracle, "/"),
		ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36",
		log: log.New(os.Stderr, "[tlsbridge] ", log.LstdFlags),
	}
	b.cookie = "prism_oai_access_token=" + accessToken
	if err := b.refreshSession(); err != nil {
		return nil, err
	}
	_ = verbose
	return b, nil
}

// refreshSession 用 access_token 换发新 session cookie（实测可用路径）。
func (b *Bridge) refreshSession() error {
	req, err := fhttp.NewRequest(http.MethodGet, upstream+"/auth/session", nil)
	if err != nil {
		return err
	}
	b.applyBase(req, b.cookie, "")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	newSess := ""
	for _, c := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "prism_session_token=") {
			if i := strings.Index(c, ";"); i > 0 {
				newSess = c[len("prism_session_token="):i]
			}
		}
	}
	if resp.StatusCode != 200 || newSess == "" {
		return fmt.Errorf("session 换发失败: HTTP %d", resp.StatusCode)
	}
	b.cookie = putCookie(b.cookie, "prism_session_token", newSess)
	b.sessAt = time.Now()
	b.log.Printf("session 换发成功（%d 字符）", len(newSess))
	return nil
}

// ensureSession：每 6 小时强制换发一次（session 实际寿命 ~12h，留余量）。
func (b *Bridge) ensureSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.sessAt) > 6*time.Hour {
		if err := b.refreshSession(); err != nil {
			b.log.Printf("session 定期换发失败（沿用旧值）: %v", err)
		}
	}
}

// fetchToken 从 oracle 取一个新签名 token。
func (b *Bridge) fetchToken() (string, error) {
	req, err := fhttp.NewRequest(http.MethodPost, b.oracle+"/token", nil)
	if err != nil {
		return "", err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("oracle HTTP %d: %s", resp.StatusCode, clip(string(body), 160))
	}
	var or struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &or); err != nil || or.Token == "" {
		return "", fmt.Errorf("oracle 响应异常: %s", clip(string(body), 160))
	}
	return or.Token, nil
}

// ServeHTTP 实现桥的转发主体。
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.ensureSession()
	token, err := b.fetchToken()
	if err != nil {
		http.Error(w, `{"error":"tlsbridge: oracle 签发失败: `+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}`, http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, `{"error":"tlsbridge: 读请求体失败"}`, http.StatusBadRequest)
		return
	}
	req, err := fhttp.NewRequest(r.Method, upstream+r.URL.RequestURI(), bodyReader(body))
	if err != nil {
		http.Error(w, `{"error":"tlsbridge: 构造请求失败"}`, http.StatusInternalServerError)
		return
	}
	b.applyBase(req, b.cookie, token)
	// 透传业务头（过滤逐跳头与调用方自带的凭据——绝不接受调用方覆盖账号池）
	for k, vals := range r.Header {
		lk := strings.ToLower(k)
		if lk == "cookie" || lk == "authorization" || lk == "openai-sentinel-token" ||
			lk == "host" || lk == "content-length" || strings.HasPrefix(lk, "sec-") {
			continue
		}
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
	if r.Method != http.MethodGet && r.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := b.client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"tlsbridge: 上游请求失败: `+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	// 回写响应头（跳过 Set-Cookie：绝不向下游泄漏上游凭据；跳过逐跳头）
	for k, vals := range resp.Header {
		lk := strings.ToLower(k)
		if lk == "set-cookie" || lk == "content-length" || lk == "transfer-encoding" || lk == "connection" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// applyBase：写基础请求头（UA/来源/凭据/哨兵）。
func (b *Bridge) applyBase(req *fhttp.Request, cookie, token string) {
	h := map[string]string{
		"Accept":             "*/*",
		"Accept-Language":    "zh-CN,zh;q=0.9",
		"Origin":             upstream,
		"Referer":            upstream + "/",
		"User-Agent":         b.ua,
		"sec-ch-ua":          `"Chromium";v="152", "Google Chrome";v="152", "Not?A_Brand";v="24"`,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
	}
	if cookie != "" {
		h["Cookie"] = cookie
	}
	if token != "" {
		h["openai-sentinel-token"] = token
	}
	if req.Method == http.MethodPost && req.Header.Get("Content-Type") == "" {
		h["Content-Type"] = "application/json"
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
}

func bodyReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return strings.NewReader(string(b))
}

func putCookie(all, name, val string) string {
	var out []string
	found := false
	for _, p := range strings.Split(all, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, name+"=") {
			out = append(out, name+"="+val)
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		out = append(out, name+"="+val)
	}
	return strings.Join(out, "; ")
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
