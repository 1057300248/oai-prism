// 第七段：上下文窗口极限探测（防崩版）。
// - evaluate 内全 try/catch：单轮网络/解析错误不崩整个实验
// - 句柄逐轮落盘 webui_probe7_handles.json → 崩溃后可续跑
// - 每轮 14k tokens 目标填充（实际 est ≈ 11.4k），暗号验证上下文活性
// - 连续 3 次瞬时失败才判终局
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
const WORDS = "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu mango papaya lychee durian guava kiwi peach plum berry melon fig date nut pineapple coconut apricot cherry grape lemon orange banana apple pear quince cipher vault ledger harbor meadow quartz falcon ember thistle bramble cinder drizzle fathom glisten haddock jasper knoll lumen marten nutmeg opal prism quiver ripple summit tundra umber velvet willow xenon yonder zephyr".split(" ");
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
function estTokens(s) {
  let cjk = 0, other = 0;
  for (const ch of s) (ch.codePointAt(0) >= 0x2e80) ? cjk++ : other++;
  return cjk + Math.floor(other / 4);
}
const NL = String.fromCharCode(10);

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

  const secret = "桂花酒酿圆子";
  const saveHandles = () => fs.writeFileSync(path.join(__dirname, "webui_probe7_handles.json"),
    JSON.stringify({ cid: H.cid, respId: H.respId, snapshot: H.snapshot, sandbox_token: H.sandbox_token, projectId: H.projectId, userId: H.userId }, null, 1));

  // 轮 1：UI 发（暗号 + 14k 填充）
  const fill1 = makeFill(14000);
  const box = page.locator("textarea").first();
  await box.click();
  await box.fill(`记住暗号：${secret}。${NL}${NL}背景资料（请忽略内容本身）：${NL}${fill1}${NL}资料结束。收到只需回复已记住。`);
  await page.keyboard.press("Enter");
  console.log("[probe7] 轮 1 已发（暗号 + 14k 填充）", flush = true);
  await page.waitForTimeout(50000);
  saveHandles();
  if (!H.cid || !H.respId || !H.sandbox_token) {
    console.log("[probe7] 句柄不全，中止");
    await browser.close();
    return;
  }

  let cumulative = estTokens(fill1) + 20;
  let consecFails = 0;
  const MAX_ROUNDS = 100;
  for (let round = 2; round <= MAX_ROUNDS; round++) {
    const fillN = makeFill(14000);
    const question = `（以下是本轮背景资料，请忽略）：${NL}${fillN}${NL}（资料结束）。现在回答：暗号是什么？只回复暗号本身；如果这个对话记录里没有暗号，回复：不知道。`;
    const qEst = estTokens(question);
    try {
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
        if (!r.request_id) return { stage: "start-fail", raw: JSON.stringify(r).slice(0, 250) };
        if (r.status === "completed") return { stage: "instant", raw: JSON.stringify(r.response || r).slice(0, 250) };
        let ts = r.turn_state;
        for (let i = 0; i < 50; i++) {
          await new Promise((res) => setTimeout(res, 2500));
          const rr = await fetch("/api/llm/response_with_tools_status", {
            method: "POST",
            headers: { "content-type": "application/json", "openai-sentinel-token": await sign() },
            body: JSON.stringify({ request_id: r.request_id, turn_state: ts }),
          });
          const txt = await rr.text();
          let j;
          try { j = JSON.parse(txt); } catch { return { stage: "nonjson", raw: txt.slice(0, 150) }; }
          if (j.status === "completed" || j.status === "error") {
            const out = j.response && j.response.payload ? (j.response.payload.output || []) : [];
            const text = out.length ? out[out.length - 1].content.map((c) => c.text).join("") : "";
            return { status: j.status, answer: text.slice(0, 120), snap: j.codex_listen_snapshot || null };
          }
          if (j.turn_state) ts = j.turn_state;
        }
        return { status: "timeout" };
      }, { H, question });

      cumulative += 14000 + qEst + 10;
      if (r.snap) H.snapshot = typeof r.snap === "string" ? r.snap : JSON.stringify(r.snap);
      const ans = (r.answer || r.raw || "").slice(0, 60);
      const alive = ans.includes(secret);
      const entry = { round, cumulativeEst: cumulative, status: r.status || r.stage, answer: ans, alive };
      fs.appendFileSync(path.join(__dirname, "webui_probe7_log.jsonl"), JSON.stringify(entry) + NL);
      console.log(`[probe7] 轮 ${round} 累计≈${cumulative} → ${ans.slice(0, 40)}`);

      if (r.stage === "start-fail" || r.stage === "instant" || r.status === "error" || r.stage === "nonjson") {
        consecFails++;
        if (consecFails >= 3) {
          console.log(`[probe7] 连续 3 次失败，终局。最后错误: ${ans}`);
          break;
        }
        await page.waitForTimeout(20000); // 瞬时失败：等 20s 重试（累计不回退）
        continue;
      }
      consecFails = 0;
      if (r.status === "timeout") {
        console.log("[probe7] 轮询超时，跳过本轮");
        continue;
      }
      if (!alive) {
        console.log(`[probe7] 结论: 第 ${round} 轮失忆（累计≈${cumulative} tokens，答案="${ans}"）`);
        break;
      }
      saveHandles();
      await page.waitForTimeout(5000);
    } catch (e) {
      consecFails++;
      console.log(`[probe7] 轮 ${round} 异常: ${String(e).slice(0, 100)}`);
      if (consecFails >= 3) { console.log("[probe7] 连续异常，终局"); break; }
      saveHandles();
      await page.waitForTimeout(20000);
    }
  }
  console.log(`[probe7] 结束：累计≈${cumulative} tokens`);
  saveHandles();
  await browser.close();
})().catch((e) => { console.error("[probe7] 失败:", e); process.exit(1); });
