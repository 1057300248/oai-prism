/**
 * 用持久化 cookie 驱动 Chrome 走 mitmdump 代理，加载 prism 前端，
 * 触发真实的 conversation-history 请求。
 *
 * mitmdump 独立抓包（tools/mitm_capture_history.py -> .workbuddy/mitm_capture.jsonl）。
 * 这里同时监听 page 事件作为双保险。
 */
const fs = require("fs");
const path = require("path");

const PW = "F:\\Dev\\Repository\\npm\\global\\node_modules\\@playwright\\mcp\\node_modules\\playwright-core";
const { chromium } = require(path.join(PW, "package.json") ? PW : PW);

const COOKIE_FILE = "F:\\Code\\Active\\OAIprism\\secrets\\cookies.txt";
const SHOT_DIR = "C:\\Users\\13080\\AppData\\Local\\Temp\\prism_shots";

function parseCookies(text) {
  const out = [];
  for (const pair of text.split(";")) {
    const i = pair.indexOf("=");
    if (i <= 0) continue;
    const name = pair.slice(0, i).trim();
    const value = pair.slice(i + 1).trim();
    if (!name || !value) continue;
    out.push({ name, value, domain: "prism.openai.com", path: "/", secure: true, httpOnly: false, sameSite: "Lax" });
  }
  return out;
}

(async () => {
  fs.mkdirSync(SHOT_DIR, { recursive: true });
  const browser = await chromium.launch({
    executablePath: "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    headless: true,
    proxy: { server: "http://127.0.0.1:18080" },
    args: ["--ignore-certificate-errors", "--no-first-run", "--disable-blink-features=AutomationControlled"],
  });
  const ctx = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 900 },
    userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36",
  });
  await ctx.addCookies(parseCookies(fs.readFileSync(COOKIE_FILE, "utf-8").trim()));

  const page = await ctx.newPage();
  const histHits = [];
  page.on("request", (r) => {
    const u = r.url();
    if (u.includes("/api/codex/") || u.includes("/api/llm/") || u.includes("/api/projects")) {
      console.log("[req]", r.method(), u.replace("https://prism.openai.com", ""));
      if (u.includes("conversation-history")) {
        console.log("[HIST-REQ-BODY]", r.postData() || "(no body)");
      }
    }
  });
  page.on("response", async (r) => {
    if (r.url().includes("conversation-history")) {
      let body = "";
      try { body = (await r.text()).slice(0, 1500); } catch (e) { body = "<err " + e.message + ">"; }
      histHits.push({ status: r.status(), body });
      console.log("[HIST-RESP]", r.status(), body.slice(0, 600));
    }
  });

  console.log("== 打开首页 ==");
  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(12000);
  await page.screenshot({ path: path.join(SHOT_DIR, "1_home.png") });

  // 收集可点击线索：项目链接/侧栏
  const links = await page.evaluate(() =>
    Array.from(document.querySelectorAll("a[href]"))
      .map((a) => a.getAttribute("href"))
      .filter((h) => h && !h.startsWith("http") && h !== "/")
      .slice(0, 40)
  );
  console.log("== 首页内部链接 ==");
  console.log(JSON.stringify(links, null, 1));

  const bodyText = (await page.evaluate(() => document.body.innerText)).slice(0, 1200);
  console.log("== 页面文本（前 1200 字）==\n" + bodyText);

  // 找项目入口并点进去（触发会话历史加载）
  const projHref = links.find((h) => /project|doc|chat|c\//i.test(h));
  if (projHref) {
    console.log("== 进入 " + projHref + " ==");
    await page.goto("https://prism.openai.com" + projHref, { waitUntil: "domcontentloaded", timeout: 90000 });
    await page.waitForTimeout(15000);
    await page.screenshot({ path: path.join(SHOT_DIR, "2_project.png") });
  } else {
    console.log("== 没有明显的项目链接，尝试点击含项目名的元素 ==");
    const clicked = await page.evaluate(() => {
      const cands = Array.from(document.querySelectorAll("[role=button],button,a,div"))
        .filter((el) => /oaiprism|sandbox prep|Untitled|项目/i.test(el.innerText || ""))
        .slice(0, 3);
      if (cands.length) { cands[0].click(); return cands[0].innerText.slice(0, 80); }
      return null;
    });
    console.log("clicked:", clicked);
    await page.waitForTimeout(15000);
    await page.screenshot({ path: path.join(SHOT_DIR, "2_project.png") });
  }

  console.log("== 会话结束，history 命中数 ==", histHits.length);
  await browser.close();
})().catch((e) => { console.error("FATAL", e); process.exit(1); });
