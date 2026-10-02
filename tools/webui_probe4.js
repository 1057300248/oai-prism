// 决定性实验：UI 发轮 1（完整原生链路），监听抓全部句柄，
// 然后在页面内用 fetch 发轮 2 增量请求（cid+prev+snapshot+sandbox_token）。
// 轮 2 答对暗号 = 增量协议字段无误 → 网关问题在传输层；失忆 = 协议仍缺东西。
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");
const path = require("path");

const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
const cookieStr = (accts.accounts || []).map((a) => a.cookies || "").join("; ");
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
    args: ["--window-size=1280,900", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));

  // 句柄收集器
  const H = { sandbox_token: null, projectId: null, userId: null, cid: null, respId: null, snapshot: null };
  ctx.on("request", (req) => {
    const url = req.url();
    if (url.includes("response_with_tools_start") && req.method() === "POST") {
      try {
        const b = JSON.parse(req.postData());
        const md = b.metadata || {};
        if (md.sandbox_token) H.sandbox_token = md.sandbox_token;
        if (md.projectId) H.projectId = md.projectId;
        if (md.userId) H.userId = md.userId;
        if (b.conversationId) H.cid = b.conversationId;
      } catch {}
    }
  });
  ctx.on("response", async (res) => {
    const url = res.url();
    if (!url.includes("response_with_tools")) return;
    try {
      const b = await res.json();
      if (b.conversation_id && !H.cid) H.cid = b.conversation_id;
      const pl = b.response && b.response.payload;
      if (pl && pl.id) H.respId = pl.id;
      if (b.codex_listen_snapshot) {
        H.snapshot = typeof b.codex_listen_snapshot === "string"
          ? b.codex_listen_snapshot : JSON.stringify(b.codex_listen_snapshot);
      }
    } catch {}
  });

  const page = await ctx.newPage();
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(9000);
  await page.locator('div:text-is("oaiprism")').locator("visible=true").first().click({ timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(18000); // 沙箱初始化

  // UI 发轮 1
  const box = page.locator("textarea").first();
  await box.click();
  await page.keyboard.type("记住暗号：芒果布丁。收到只需回复已记住。", { delay: 15 });
  await page.keyboard.press("Enter");
  console.log("[probe4] 轮 1 已发送，等待完成 ...");
  await page.waitForTimeout(40000);
  console.log("[probe4] 句柄:", JSON.stringify({
    cid: (H.cid || "").slice(0, 20), respId: (H.respId || "").slice(0, 24),
    hasTok: !!H.sandbox_token, hasSnap: !!H.snapshot, userId: H.userId,
  }));
  if (!H.cid || !H.respId) { console.log("[probe4] 句柄不全，中止"); await browser.close(); return; }

  // 页面内 fetch 轮 2 增量
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
    const b = {
      input: [
        { type: "message", role: "system", content: [{ type: "input_text", text: editorState }] },
        { type: "message", role: "user", content: [{ type: "input_text", text: "暗号是什么？只回复暗号本身。" }] },
      ],
      conversationId: H.cid,
      previousResponseId: H.respId,
      metadata: {
        model: "gpt-5.6-sol",
        reasoning_effort: "medium",
        frontend_origin: "https://prism.openai.com",
        projectId: H.projectId,
        userId: H.userId,
        sandbox_url: "/s/sandboxes/proxy/",
        sandbox_token: H.sandbox_token,
        ...(H.snapshot ? { codex_listen_snapshot: H.snapshot } : {}),
      },
    };
    const r = await (await fetch("/api/llm/response_with_tools_start", {
      method: "POST",
      headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
      body: JSON.stringify(b),
    })).json();
    if (!r.request_id) return { stage: "start", raw: JSON.stringify(r).slice(0, 400) };
    if (r.status === "completed") return { stage: "instant", raw: JSON.stringify(r.response || r).slice(0, 400) };
    let ts = r.turn_state;
    for (let i = 0; i < 40; i++) {
      await new Promise((res) => setTimeout(res, 2500));
      const rr = await fetch("/api/llm/response_with_tools_status", {
        method: "POST",
        headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
        body: JSON.stringify({ request_id: r.request_id, turn_state: ts }),
      });
      const j = await rr.json();
      if (j.status === "completed" || j.status === "error") {
        const out = j.response && j.response.payload ? (j.response.payload.output || []) : [];
        const text = out.length ? out[out.length - 1].content.map((c) => c.text).join("") : "";
        return { stage: "polled", status: j.status, answer: text.slice(0, 220) };
      }
      if (j.turn_state) ts = j.turn_state;
    }
    return { stage: "timeout" };
  }, H);

  console.log("[probe4] 轮 2 结果:", JSON.stringify(result, null, 1));
  fs.writeFileSync(path.join(__dirname, "webui_probe4_result.json"), JSON.stringify({ handles: { cid: H.cid, respId: H.respId }, result }, null, 1));
  await page.screenshot({ path: path.join(__dirname, "webui_probe4_after.png") });
  await browser.close();
})().catch((e) => { console.error("[probe4] 失败:", e); process.exit(1); });
