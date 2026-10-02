// 重放实验：在真实页面环境里，对 probe2 已有 conversation 发续接轮。
// 请求形状 = 真实轮 2 的逐字段复刻（含 sandbox_token/snapshot/userId）。
// 答对"菠萝油" → 协议无误，问题在网关传输层；失忆 → 协议仍缺字段。
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");
const path = require("path");

const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
const cookieStr = (accts.accounts || []).map((a) => a.cookies || "").join("; ");
const H0 = JSON.parse(fs.readFileSync(path.join(__dirname, "webui_probe3_handles.json"), "utf-8"));

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
    args: ["--window-size=1100,700", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));
  const page = await ctx.newPage();
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(10000);
  let sdk = false;
  for (let i = 0; i < 30; i++) {
    sdk = await page.evaluate(() => typeof window.SentinelSDK === "object" && !!window.SentinelSDK.token).catch(() => false);
    if (sdk) break;
    await page.waitForTimeout(2000);
  }
  console.log("[replay] SentinelSDK 就绪:", sdk);

  const result = await page.evaluate(async (H) => {
    const sign = async () => {
      const t = await window.SentinelSDK.token("prism_inference");
      return typeof t === "string" ? t : JSON.stringify(t);
    };
    const now = Date.now();
    const editorState = JSON.stringify({
      openFile: { status: "error", message: "No open file" },
      selectedText: { status: "error", message: "No selection" },
      request: { source: "user", promptTextLength: 0, timestampUtcIso: new Date(now).toISOString(), timestampUtcMs: now, selectionKind: "unknown" },
    });
    const poll = async (rid, ts) => {
      for (let i = 0; i < 40; i++) {
        await new Promise((r) => setTimeout(r, 2500));
        const r = await fetch("/api/llm/response_with_tools_status", {
          method: "POST",
          headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
          body: JSON.stringify({ request_id: rid, turn_state: ts }),
        });
        const j = await r.json();
        if (j.status === "completed" || j.status === "error") return j;
        if (j.turn_state) ts = j.turn_state;
      }
      return { status: "timeout" };
    };
    const b = {
      input: [
        { type: "message", role: "system", content: [{ type: "input_text", text: editorState }] },
        { type: "message", role: "user", content: [{ type: "input_text", text: "暗号是什么？只回复暗号本身。" }] },
      ],
      conversationId: H.cid,
      previousResponseId: H.prev,
      metadata: {
        model: "gpt-5.6-sol",
        reasoning_effort: "medium",
        frontend_origin: "https://prism.openai.com",
        sandbox_url: "/s/sandboxes/proxy/",
        sandbox_token: H.sandbox_token,
        codex_listen_snapshot: H.snapshot,
      },
    };
    const r = await (await fetch("/api/llm/response_with_tools_start", {
      method: "POST",
      headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
      body: JSON.stringify(b),
    })).json();
    if (!r.request_id) return { stage: "start", raw: JSON.stringify(r).slice(0, 400) };
    if (r.status === "completed") {
      // 立即完成：要么极快答案，要么错误
      const pl = r.response && r.response.payload;
      return { stage: "instant", raw: JSON.stringify(r.response || r).slice(0, 400) };
    }
    const f = await poll(r.request_id, r.turn_state);
    const out = f.response && f.response.payload ? (f.response.payload.output || []) : [];
    const text = out.length ? out[out.length - 1].content.map((c) => c.text).join("") : "";
    return { stage: "polled", status: f.status, text: text.slice(0, 220), f1keys: Object.keys(f) };
  }, H0);

  console.log("[replay] 结果:", JSON.stringify(result, null, 1));
  fs.writeFileSync(path.join(__dirname, "webui_probe3_result.json"), JSON.stringify(result, null, 1));
  await browser.close();
})().catch((e) => { console.error("[replay] 失败:", e); process.exit(1); });
