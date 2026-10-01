/**
 * OAIprism 浏览器通道 sidecar。
 *
 * 背景：Cloudflare 对 prism.openai.com 启用 TLS 指纹校验，Go/Python
 * 客户端（无论凭据多新鲜）一律 403 "Request verification failed"；
 * 真实 Chrome（playwright 驱动）的请求全部放行。
 *
 * 原理：常驻一个 Chrome 实例（登录态 = 预置 access_token cookie，
 * Prism 会自动换发完整 session），本地 8790 收到 OAIprism 的上游请求
 * 后在浏览器页面内 fetch 转发，把状态码/头/体原样返回。
 * 上游协议是 start+poll（纯请求-响应，无 SSE），所以不需要流式透传。
 *
 * 用法：node browser_sidecar.js <access_token> [端口=8790]
 */
const http = require("http");
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);

const AT = process.argv[2] || "";
const PORT = parseInt(process.argv[3] || "8790", 10);
const TARGET = "https://prism.openai.com";
const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36";

// 请求头透传白名单：这些头从 OAIprism 带到浏览器 fetch（其余由浏览器自己定）。
const PASS_HEADERS = new Set(["accept", "x-crixet-sandbox-token", "oai-account-id", "content-type"]);

(async () => {
  const browser = await chromium.launch({
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: false, // 有头：无头模式 session 换发被拒（实测 401），有头正常
    args: ["--no-first-run", "--disable-blink-features=AutomationControlled"],
  });
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    userAgent: UA,
  });
  if (AT) {
    await ctx.addCookies([
      { name: "prism_oai_access_token", value: AT, domain: "prism.openai.com", path: "/", secure: true, sameSite: "Lax" },
      { name: "oai-did", value: "a4f590ce-d1b4-4f4e-9f0a-4c1e2d3b5a6f", domain: ".openai.com", path: "/", secure: true, sameSite: "Lax" },
    ]);
  }
  const page = await ctx.newPage();
  await page.goto(TARGET + "/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(20000);
  // 预热：先用页面内 fetch 触发一次 session 换发确认（GET 只读端点）
  const warm = await page.evaluate(async () => {
    const r = await fetch(location.origin + "/api/maintenance", { credentials: "include" });
    return r.status;
  });
  console.log("[sidecar] 预热 maintenance:", warm);
  console.log("[sidecar] 浏览器已就绪:", await page.title());



  const server = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const ch of req) chunks.push(ch);
    const body = Buffer.concat(chunks).toString("utf-8");

    const headers = {};
    for (const [k, v] of Object.entries(req.headers)) {
      if (PASS_HEADERS.has(k.toLowerCase())) headers[k.toLowerCase()] = Array.isArray(v) ? v[0] : v;
    }

    const spec = { path: req.url, method: req.method, headers, body: body || null };
    try {
      // 与实测成功版本（prism_inbrowser_v2）完全一致的内联 evaluate fetch。
      // 注意：不走 exposeFunction —— 对照实验证明 exposeFunction 链路会被
      // Prism 校验拒绝（401），原因未明（疑似 isolated world 上下文差异）。
      const result = await page.evaluate(async (s) => {
        const r = await fetch("https://prism.openai.com" + s.path, {
          method: s.method,
          headers: s.headers,
          credentials: "include",
          body: s.body === null || s.body === "" ? undefined : s.body,
        });
        const headers = {};
        r.headers.forEach((v, k) => { headers[k] = v; });
        const body = await r.text();
        return { status: r.status, headers, body };
      }, spec);
      res.writeHead(result.status, { "Content-Type": result.headers["content-type"] || "application/json" });
      res.end(result.body);
    } catch (e) {
      res.writeHead(502, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "sidecar: " + String(e).slice(0, 200) }));
    }
  });

  server.listen(PORT, "127.0.0.1", () => {
    console.log("[sidecar] listening http://127.0.0.1:" + PORT + " -> " + TARGET);
  });
})().catch((e) => { console.error("[sidecar] FATAL", e); process.exit(1); });
