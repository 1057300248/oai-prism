// 第五段：测上游 conversation 真实上下文窗口。
// 方法：UI 发轮 1（埋暗号 + 4k tokens 填充），页面 fetch 增量续接轮 2..N，
// 每轮注入 8k tokens 填充并以"暗号是什么"验证上下文活性。
// 累计到某轮报错或失忆 → 该轮累计 = 窗口上界。
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

// 随机英文词填充（~1.5 token/词）
const WORDS = "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu mango papaya lychee durian guava kiwi peach plum berry melon fig date nut pineapple coconut apricot cherry grape lemon orange banana apple pear quince cipher vault token ledger harbor meadow quartz falcon ember thistle bramble cinder drizzle fathom glisten haddock ingle jasper knoll lumen marten nutmeg opal prism quiver ripple summit tundra umber velvet willow xenon yonder zephyr".split(" ");
function makeFill(targetTokens) {
  const out = [];
  let used = 0;
  while (used < targetTokens) {
    const w = WORDS[Math.floor(Math.random() * WORDS.length)];
    out.push(w);
    used += Math.max(1, Math.round(w.length / 4 + 0.5));
  }
  return out.join(" ");
}
// 本地 token 估算（与网关口径一致：CJK 1 字 1 token，其他 4 字符 1 token）
function estTokens(s) {
  let cjk = 0, other = 0;
  for (const ch of s) (ch.codePointAt(0) >= 0x2e80) ? cjk++ : other++;
  return cjk + Math.floor(other / 4);
}

(async () => {
  const browser = await chromium.launch({
    headless: false, channel: "chrome",
    args: ["--window-size=1280,900", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));

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
  await page.locator('div:text-is("oaiprism")').locator("visible=true").first().click({ timeout: 15000 }).catch(() => {});
  await page.waitForTimeout(18000);

  // 轮 1：UI 发（fill 大文本一步填入）：暗号 + 4k tokens 填充
  const secret = "桂花酒酿圆子";
  const fill1 = makeFill(14000);
  const box = page.locator("textarea").first();
  await box.click();
  await box.fill(`记住暗号：${secret}。\n\n背景资料（请忽略内容本身）：\n${fill1}\n\n资料结束。收到只需回复已记住。`);
  await page.keyboard.press("Enter");
  console.log("[probe5] 轮 1 已发（暗号 + 4k 填充）");
  await page.waitForTimeout(45000);
  console.log("[probe5] 句柄:", JSON.stringify({
    cid: (H.cid || "").slice(0, 18), respId: (H.respId || "").slice(0, 22),
    hasTok: !!H.sandbox_token, hasSnap: !!H.snapshot,
  }));
  if (!H.cid || !H.respId || !H.sandbox_token) {
    console.log("[probe5] 句柄不全，中止");
    await browser.close();
    return;
  }

  // 轮 2..N：页面 fetch 增量，每轮 +8k tokens 填充 + 暗号验证
  let cumulative = estTokens(fill1) + estTokens(`记住暗号：${secret}。收到只需回复已记住。`) + 8;
  const log = [];
  let verdict = "未触界（轮数用尽）";
  for (let round = 2; round <= 30; round++) {
    const fillN = makeFill(14000);
    const question = `（以下是本轮背景资料，请忽略）：\n${fillN}\n（资料结束）。现在回答：暗号是什么？只回复暗号本身；如果这个对话记录里没有暗号，回复：不知道。`;
    const r = await page.evaluate(async ({ H, question }) => {
      const sign = async () => {
        const t = await window.SentinelSDK.token("prism_inference");
        return typeof t === "string" ? t : JSON.stringify(t);
      };
      const now = Date.now();
      const editorState = JSON.stringify({
        openFile: { status: "error", message: "No open file" },
        selectedText: { status: "error", message: "No selection" },
        request: { source: "user", promptTextLength: question.length, timestampUtcIso: new Date(now).toISOString(), timestampUtcMs: now, selectionKind: "unknown" },
      });
      const b = {
        input: [
          { type: "message", role: "system", content: [{ type: "input_text", text: editorState }] },
          { type: "message", role: "user", content: [{ type: "input_text", text: question }] },
        ],
        conversationId: H.cid,
        previousResponseId: H.respId,
        metadata: {
          model: "gpt-5.6-sol", reasoning_effort: "medium",
          frontend_origin: "https://prism.openai.com",
          projectId: H.projectId, userId: H.userId,
          sandbox_url: "/s/sandboxes/proxy/", sandbox_token: H.sandbox_token,
          ...(H.snapshot ? { codex_listen_snapshot: H.snapshot } : {}),
        },
      };
      const r = await (await fetch("/api/llm/response_with_tools_start", {
        method: "POST",
        headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
        body: JSON.stringify(b),
      })).json();
      if (!r.request_id) return { stage: "start-fail", raw: JSON.stringify(r).slice(0, 300) };
      if (r.status === "completed") return { stage: "instant", raw: JSON.stringify(r.response || r).slice(0, 300) };
      let ts = r.turn_state;
      for (let i = 0; i < 50; i++) {
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
          return { status: j.status, answer: text.slice(0, 120), snap: j.codex_listen_snapshot || null };
        }
        if (j.turn_state) ts = j.turn_state;
      }
      return { status: "timeout" };
    }, { H, question });

    // 更新句柄
    if (r.snap) H.snapshot = typeof r.snap === "string" ? r.snap : JSON.stringify(r.snap);
    cumulative += 14000 + estTokens(question) + 10;

    const ans = (r.answer || r.raw || "").slice(0, 60);
    const alive = ans.includes(secret);
    const entry = { round, cumulativeEst: cumulative, status: r.status || r.stage, answer: ans, alive };
    log.push(entry);
    console.log(`[probe5] 轮 ${round} 累计≈${cumulative} tokens → ${JSON.stringify(entry)}`);

    if (r.stage === "start-fail" || r.stage === "instant" || r.status === "error" || r.status === "timeout") {
      verdict = `第 ${round} 轮报错（累计≈${cumulative} tokens）：${ans}`;
      break;
    }
    if (!alive) {
      verdict = `第 ${round} 轮上下文失忆（累计≈${cumulative} tokens，答案="${ans}"）`;
      break;
    }
    await page.waitForTimeout(8000);
  }
  console.log("\n[probe5] 结论:", verdict);
  fs.writeFileSync(path.join(__dirname, "webui_probe5_result.json"),
    JSON.stringify({ verdict, log }, null, 1));
  await browser.close();
})().catch((e) => { console.error("[probe5] 失败:", e); process.exit(1); });
