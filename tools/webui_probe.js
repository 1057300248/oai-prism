// Prism WebUI 协议考古 —— 用真实浏览器回答三个问题（2026-10-02）：
//   1. Web 端多轮对话：第二次发消息时 start 请求带不带历史？
//   2. 历史加载：打开已有会话时调用什么 API 取回历史？
//   3. 压缩/上下文窗口：有没有暴露 compaction/usage/limit 相关接口？
//
// 产物：tools/webui_probe_capture.jsonl（逐行 JSON：{kind, time, url, method, status, body/postData}）
//       tools/webui_probe_shot.png（页面截图）
// 用法：node tools/webui_probe.js [-- interact]
//   无参数 = 被动监听（打开首页 45s，人工/脚本不操作）
//   --interact = 自动点击会话列表第一项并尝试发送两条消息

const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");
const path = require("path");

const OUT = path.join(__dirname, "webui_probe_capture.jsonl");
const SHOT = path.join(__dirname, "webui_probe_shot.png");
const INTERACT = process.argv.includes("--interact");

const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
const cookieStr = (accts.accounts || []).map((a) => a.cookies || "").join("; ");

function parseCookies(str) {
  const out = [];
  for (const pair of str.split("; ")) {
    const eq = pair.indexOf("=");
    if (eq < 0) continue;
    out.push({
      name: pair.slice(0, eq),
      value: pair.slice(eq + 1),
      domain: "prism.openai.com",
      path: "/",
    });
  }
  return out;
}

const log = (obj) => {
  fs.appendFileSync(OUT, JSON.stringify(obj) + "\n");
};

(async () => {
  fs.writeFileSync(OUT, "");
  const browser = await chromium.launch({
    headless: false,
    channel: "chrome",
    args: ["--window-size=1280,900", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));

  // ---- 流量监听：请求（带 body）----
  ctx.on("request", (req) => {
    const url = req.url();
    if (!url.includes("prism.openai.com")) return;
    if (/\.(js|css|png|jpg|svg|woff2?|ico)(\?|$)/.test(url)) return;
    let post = null;
    try { post = req.postData() || null; } catch {}
    log({
      kind: "req", time: new Date().toISOString(),
      method: req.method(), url: url.slice(0, 300),
      postData: post ? post.slice(0, 120000) : null,
    });
  });
  // ---- 流量监听：响应（/api/ 的 JSON body）----
  ctx.on("response", async (res) => {
    const url = res.url();
    if (!url.includes("prism.openai.com/api")) return;
    let body = null;
    try {
      const ct = res.headers()["content-type"] || "";
      if (ct.includes("json") || ct.includes("text")) {
        body = (await res.text()).slice(0, 80000);
      }
    } catch {}
    log({
      kind: "res", time: new Date().toISOString(),
      status: res.status(), url: url.slice(0, 300),
      body,
    });
  });

  const page = await ctx.newPage();
  page.on("pageerror", (e) => log({ kind: "pageerror", msg: String(e).slice(0, 300) }));

  console.log("[probe] 打开 prism.openai.com ...");
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(12000);
  console.log("[probe] 标题:", await page.title());

  // dump 前端存储里与上下文/压缩相关的键
  const storage = await page.evaluate(() => {
    const pick = (o) => Object.fromEntries(Object.entries(o).filter(([k]) =>
      /context|compress|compact|history|usage|token|limit|window|session|conversation/i.test(k)));
    return {
      localStorage: pick({ ...localStorage }),
      sessionStorage: pick({ ...sessionStorage }),
      allLocalKeys: Object.keys(localStorage),
      allSessionKeys: Object.keys(sessionStorage),
    };
  });
  log({ kind: "storage", data: storage });

  await page.screenshot({ path: SHOT, fullPage: false });
  console.log("[probe] 首页截图:", SHOT);

  if (INTERACT) {
    // 尝试点开侧栏第一个已有会话（历史加载请求会出现）
    try {
      const items = page.locator("aside a, nav a, [class*='session'], [class*='conversation'], [class*='chat-item']");
      const n = await items.count();
      console.log("[probe] 侧栏候选条目:", n);
      if (n > 0) {
        await items.first().click({ timeout: 5000 });
        await page.waitForTimeout(6000);
        console.log("[probe] 已点击第一项");
      }
    } catch (e) {
      console.log("[probe] 点击会话失败:", String(e).slice(0, 120));
    }
    await page.screenshot({ path: SHOT.replace(".png", "_session.png"), fullPage: false });

    // 尝试定位输入框并发两条消息（第二条是关键：看 start 带不带历史）
    const selCandidates = ["textarea", "div[contenteditable='true']", "[placeholder*='Message']", "[placeholder*='问']"];
    let box = null;
    for (const sel of selCandidates) {
      const loc = page.locator(sel).first();
      if (await loc.count() > 0 && await loc.isVisible().catch(() => false)) { box = loc; break; }
    }
    if (box) {
      console.log("[probe] 找到输入框，发送消息 1 ...");
      await box.click();
      await box.fill("记住暗号：菠萝油。收到只需回复已记住。");
      await box.press("Enter");
      await page.waitForTimeout(30000);
      console.log("[probe] 发送消息 2（关键样本）...");
      await box.click();
      await box.fill("暗号是什么？只回复暗号本身。");
      await box.press("Enter");
      await page.waitForTimeout(35000);
      await page.screenshot({ path: SHOT.replace(".png", "_after.png"), fullPage: false });
      console.log("[probe] 交互完成，两次请求均已落盘");
    } else {
      console.log("[probe] 未找到输入框 —— 请看截图定位 selector 后重跑");
    }
  }

  await page.waitForTimeout(5000);
  await browser.close();
  console.log("[probe] 完成，抓取文件:", OUT);
})().catch((e) => { console.error("[probe] 失败:", e); process.exit(1); });
