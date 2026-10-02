// 第二段：点进项目 → AI 对话面板 → 发两条消息
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");
const path = require("path");

const OUT = path.join(__dirname, "webui_probe_capture.jsonl");
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
const log = (obj) => fs.appendFileSync(OUT, JSON.stringify(obj) + "\n");

(async () => {
  const browser = await chromium.launch({
    headless: false, channel: "chrome",
    args: ["--window-size=1280,900", "--window-position=20,20"],
  });
  const ctx = await browser.newContext({ viewport: null });
  await ctx.addCookies(parseCookies(cookieStr));
  ctx.on("request", (req) => {
    const url = req.url();
    if (!url.includes("prism.openai.com")) return;
    if (/\.(js|css|png|jpg|svg|woff2?|ico|wasm)(\?|$)/.test(url)) return;
    let post = null;
    try { post = req.postData() || null; } catch {}
    log({ kind: "req", time: new Date().toISOString(), method: req.method(), url: url.slice(0, 300), postData: post ? post.slice(0, 150000) : null });
  });
  ctx.on("response", async (res) => {
    const url = res.url();
    if (!url.includes("prism.openai.com/api")) return;
    let body = null;
    try {
      const ct = res.headers()["content-type"] || "";
      if (ct.includes("json") || ct.includes("text")) body = (await res.text()).slice(0, 100000);
    } catch {}
    log({ kind: "res", time: new Date().toISOString(), status: res.status(), url: url.slice(0, 300), body });
  });

  const page = await ctx.newPage();
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(8000);

  // 点第一个项目行（页面有隐藏副本，必须 visible 过滤）
  console.log("[probe2] 点第一个项目 ...");
  const row = page.locator('div:text-is("oaiprism")').locator("visible=true").first();
  await row.click({ timeout: 10000 });
  await page.waitForTimeout(10000);
  console.log("[probe2] URL:", page.url());
  await page.screenshot({ path: path.join(__dirname, "webui_probe_p2_project.png") });

  // dump 可交互元素
  const ui = await page.evaluate(() => {
    const els = [];
    document.querySelectorAll("textarea, [contenteditable='true'], button, [role='button'], [role='tab']").forEach((e) => {
      const t = (e.tagName + " " + (e.getAttribute("aria-label") || e.getAttribute("placeholder") || e.textContent || "").trim().slice(0, 40));
      els.push(t);
    });
    return els.slice(0, 60);
  });
  console.log("[probe2] 交互元素:\n" + ui.join("\n"));

  // 找输入框（textarea 或 contenteditable）
  let box = null;
  for (const sel of ["textarea", "div[contenteditable='true']", "[role='textbox']"]) {
    const loc = page.locator(sel).first();
    if (await loc.count() > 0 && await loc.isVisible().catch(() => false)) { box = loc; console.log("[probe2] 输入框 =", sel); break; }
  }
  if (!box) {
    // 可能需要先点 AI/聊天按钮
    for (const label of ["AI", "Chat", "对话", "助手", "Assistant"]) {
      const btn = page.locator(`button:has-text("${label}"), [role="tab"]:has-text("${label}")`).first();
      if (await btn.count() > 0) {
        await btn.click({ timeout: 4000 }).catch(() => {});
        await page.waitForTimeout(4000);
        box = page.locator("textarea, div[contenteditable='true']").first();
        if (await box.count() > 0 && await box.isVisible().catch(() => false)) { console.log("[probe2] 点击", label, "后出现输入框"); break; }
      }
    }
  }
  if (box && await box.isVisible().catch(() => false)) {
    console.log("[probe2] 发消息 1 ...");
    await box.click();
    await page.keyboard.type("记住暗号：菠萝油。收到只需回复已记住。", { delay: 20 });
    await page.keyboard.press("Enter");
    await page.waitForTimeout(35000);
    console.log("[probe2] 发消息 2（关键样本）...");
    await box.click();
    await page.keyboard.type("暗号是什么？只回复暗号本身。", { delay: 20 });
    await page.keyboard.press("Enter");
    await page.waitForTimeout(40000);
    await page.screenshot({ path: path.join(__dirname, "webui_probe_p2_after.png") });
    console.log("[probe2] 两条消息完成");
  } else {
    console.log("[probe2] 没找到输入框");
  }
  await page.waitForTimeout(3000);
  await browser.close();
})().catch((e) => { console.error("[probe2] 失败:", e); process.exit(1); });
