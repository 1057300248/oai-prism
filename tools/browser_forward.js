// Browser Forward —— 全请求浏览器代发（2026-10-02 增量续接实验的落地件）。
//
// 背景：Go tls-client 指纹能过推理（200），但上游对"非浏览器级信任"
// 的请求按无状态会话处理 —— conversation 状态不读不写，增量续接失效。
// 真实浏览器内重放同一请求则续接成功（webui_probe4 实证，答对暗号）。
// 因此把整个上游通道搬进真实浏览器：页面内 fetch，Sentinel 每请求现签。
//
// 接口：POST /<upstream-path>（原样透传 query/body），返回上游 JSON。
//       GET  /healthz
// 用法：node browser_forward.js [port=8790]
// 网关配置：upstream.base_url: http://127.0.0.1:8790（替换 tlsbridge）。
//
// 串行化：单页面单队列 —— 沙箱/推理请求之间有隐式时序依赖，乱序会踩
// 资源令牌的 1 小时窗口。并发需求出现时再开多 tab 分片。

const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const http = require("http");
const fs = require("fs");

const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
const cookieStr = (accts.accounts || []).map((a) => a.cookies || "").join("; ");
const PORT = parseInt(process.argv[2] || "8790", 10);

function parseCookies(str) {
  const out = [];
  for (const pair of str.split("; ")) {
    const eq = pair.indexOf("=");
    if (eq < 0) continue;
    out.push({ name: pair.slice(0, eq), value: pair.slice(eq + 1), domain: "prism.openai.com", path: "/" });
  }
  return out;
}

(async () => {
  const browser = await chromium.launch({
    headless: false, channel: "chrome",
    args: ["--window-size=420,320", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));
  const page = await ctx.newPage();
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(12000);
  console.log("[forward] 页面就绪:", await page.title());

  let sdk = false;
  for (let i = 0; i < 30; i++) {
    sdk = await page.evaluate(() => typeof window.SentinelSDK === "object" && !!window.SentinelSDK.token).catch(() => false);
    if (sdk) break;
    await page.waitForTimeout(2000);
  }
  console.log("[forward] SentinelSDK 就绪:", sdk);

  // 进第一个项目：所有后续 fetch 的 Referer/页面状态与真实 Web 一致
  // （首页 Referer 下增量续接失败的疑似环境因素，probe4 在项目页成功）。
  try {
    await page.locator('div:text-is("oaiprism")').locator("visible=true").first().click({ timeout: 10000 });
    await page.waitForTimeout(15000);
    console.log("[forward] 已进入项目页:", page.url().slice(0, 80));
  } catch (e) {
    console.log("[forward] 进项目失败（继续用首页）:", String(e).slice(0, 100));
  }

  let chain = Promise.resolve(); // 全局串行
  const server = http.createServer((req, res) => {
    if (req.url === "/healthz") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok", mode: "browser-forward" }));
      return;
    }
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      chain = chain.then(async () => {
        try {
          const upstreamPath = req.url; // 带 query 原样透传
          // 透传网关的业务头（沙箱 X-Crixet 鉴权等）；Cookie/Host 由浏览器自理，
          // sentinel 由页面现签覆盖 —— 客户端带的旧 token 一律丢弃。
          const passHeaders = {};
          for (const [k, v] of Object.entries(req.headers)) {
            const lk = k.toLowerCase();
            if (["host", "cookie", "content-length", "connection", "openai-sentinel-token", "x-openai-sentinel-token", "accept-encoding"].includes(lk)) continue;
            passHeaders[lk] = v;
          }
          const out = await page.evaluate(async ({ p, method, payload, extraHeaders }) => {
            const headers = { ...extraHeaders };
            if (method === "POST") headers["content-type"] = headers["content-type"] || "application/json";
            try {
              const t = await window.SentinelSDK.token("prism_inference");
              headers["openai-sentinel-token"] = typeof t === "string" ? t : JSON.stringify(t);
            } catch (e) { /* token 失败照样发 —— 只读接口不需要 */ }
            const r = await fetch("https://prism.openai.com" + p, {
              method, headers, body: method === "POST" ? (payload || null) : undefined,
            });
            const text = await r.text();
            return { status: r.status, text };
          }, { p: upstreamPath, method: req.method, payload: body || null, extraHeaders: passHeaders });
          res.writeHead(out.status, { "Content-Type": "application/json" });
          res.end(out.text);
        } catch (e) {
          res.writeHead(502, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ error: { message: "browser-forward: " + String(e).slice(0, 200), type: "upstream_error", code: "502" } }));
        }
      });
    });
  });
  server.listen(PORT, "127.0.0.1", () => console.log(`[forward] 监听 http://127.0.0.1:${PORT} → 浏览器内 fetch → prism.openai.com`));
})().catch((e) => { console.error("[forward] 启动失败:", e); process.exit(1); });
