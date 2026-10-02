// Sentinel Token Oracle —— 浏览器只做一件事：在真实页面环境里执行
// SentinelSDK.token(flow)，把签好的 token 通过本地 HTTP 交给 Go 网关。
//
// 与旧版 browser_sidecar.js（请求转发）的区别：
//   - 不转发任何 API 流量：Go 网关用自己的 TLS 指纹直连 prism
//   - 浏览器只执行签名（每请求 ~100ms-2s，SDK 内部含 sentinel/req）
//   - 无长连接、无流转发，崩溃面大幅缩小
//
// 用法：node sentinel_oracle.js <access_token|"auto"> [port=8791]
// 接口：GET /token            -> { "token": "...", "flow": "..." }
//       GET /healthz          -> ok
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const http = require("http");

// ---------- 参数：auto = 从 secrets/accounts.json 提取 access_token ----------
let AT = process.argv[2] || "";
const PORT = parseInt(process.argv[3] || "8791", 10);
if (AT === "auto" || AT === "") {
  try {
    const fs2 = require("fs");
    const accts = JSON.parse(fs2.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
    const cookies = (accts.accounts || []).map((a) => a.cookies || "").join("; ");
    const m = cookies.match(/prism_oai_access_token=([^;\s]+)/);
    if (m) { AT = m[1]; console.log("[oracle] 已从 accounts.json 提取 access_token（" + AT.length + " chars）"); }
    else { console.log("[oracle] accounts.json 未找到 prism_oai_access_token"); }
  } catch (e) {
    console.log("[oracle] 读取 accounts.json 失败:", String(e).slice(0, 120));
  }
}

const TARGET = "https://prism.openai.com";
const FLOW = "prism_inference";

(async () => {
  // ---------- 浏览器：预置 access_token → 自动换发 session（有头：无头签发被拒过） ----------
  const browser = await chromium.launch({
    headless: false,
    channel: "chrome",
    args: ["--window-size=420,320", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  const page = await ctx.newPage();
  await ctx.addCookies([{ name: "prism_oai_access_token", value: AT, domain: "prism.openai.com", path: "/" }]);
  await page.goto(TARGET + "/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(15000);
  console.log("[oracle] 页面就绪:", await page.title());

  // 等待 SentinelSDK 全局可用
  let sdkReady = false;
  for (let i = 0; i < 30; i++) {
    sdkReady = await page.evaluate(() => typeof window.SentinelSDK === "object" && !!window.SentinelSDK.token).catch(() => false);
    if (sdkReady) break;
    await page.waitForTimeout(2000);
  }
  console.log("[oracle] SentinelSDK 就绪:", sdkReady);
  if (!sdkReady) { console.error("[oracle] ✗ SDK 未加载"); process.exit(1); }

  // ---------- 签发函数 ----------
  let signing = null; // 串行化（SDK 内部有 9 分钟 requirements 缓存 + PoW，不能并发）
  async function signToken() {
    if (signing) return signing;
    signing = page.evaluate(async (flow) => {
      const r = await window.SentinelSDK.token(flow);
      return { token: r, at: Date.now() };
    }, FLOW).finally(() => { signing = null; });
    return signing;
  }

  // ---------- 本地 HTTP ----------
  const server = http.createServer(async (req, res) => {
    if (req.method === "GET" && req.url === "/healthz") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok", flow: FLOW }));
      return;
    }
    if (req.method === "POST" && req.url === "/token") {
      try {
        const { token } = await signToken();
        if (!token || typeof token !== "string" || token.indexOf('"p"') < 0) {
          throw new Error("token 形状异常: " + String(token).slice(0, 60));
        }
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ token, flow: FLOW }));
      } catch (e) {
        res.writeHead(502, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: String(e).slice(0, 200) }));
      }
      return;
    }
    res.writeHead(404);
    res.end();
  });
  server.listen(PORT, () => console.log("[oracle] 监听 http://127.0.0.1:" + PORT + "（POST /token）"));
})().catch((e) => { console.error("[oracle] 启动失败:", String(e).slice(0, 300)); process.exit(1); });
