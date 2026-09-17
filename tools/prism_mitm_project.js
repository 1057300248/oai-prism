/**
 * 进入指定项目页，让前端加载文件树/会话，mitm 抓全部 API。
 * 用法：node prism_mitm_project.js <projectUuid>
 */
const fs = require("fs");
const path = require("path");

const PW = "F:\\Dev\\Repository\\npm\\global\\node_modules\\@playwright\\mcp\\node_modules\\playwright-core";
const { chromium } = require(PW);

const COOKIE_FILE = "F:\\Code\\Active\\OAIprism\\secrets\\cookies.txt";
const SHOT_DIR = "C:\\Users\\13080\\AppData\\Local\\Temp\\prism_shots";
const PID = process.argv[2] || "23871bb5-d926-4f12-9c82-e23becd828c2";

function parseCookies(text) {
  const out = [];
  for (const pair of text.split(";")) {
    const i = pair.indexOf("=");
    if (i <= 0) continue;
    const name = pair.slice(0, i).trim();
    const value = pair.slice(i + 1).trim();
    if (!name || !value) continue;
    out.push({ name, value, domain: "prism.openai.com", path: "/", secure: true, sameSite: "Lax" });
  }
  return out;
}

(async () => {
  fs.mkdirSync(SHOT_DIR, { recursive: true });
  const browser = await chromium.launch({
    executablePath: "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    headless: true,
    proxy: { server: "http://127.0.0.1:18080" },
    args: ["--ignore-certificate-errors", "--no-first-run"],
  });
  const ctx = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1440, height: 900 },
    userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36",
  });
  await ctx.addCookies(parseCookies(fs.readFileSync(COOKIE_FILE, "utf-8").trim()));

  const page = await ctx.newPage();
  const interesting = [];
  page.on("request", (r) => {
    const u = r.url();
    if (u.includes("prism.openai.com") && /\/api\/|\/s\/sandboxes/.test(u) && !u.includes("_next")) {
      const line = r.method() + " " + u.replace("https://prism.openai.com", "");
      interesting.push(line);
      console.log("[req]", line.slice(0, 160));
      if (u.includes("conversation-history") && r.postData()) console.log("    body:", r.postData().slice(0, 300));
    }
  });
  page.on("response", async (r) => {
    const u = r.url();
    if (u.includes("conversation-history") || (u.includes("/files") && !u.includes("_next"))) {
      let b = "";
      try { b = (await r.text()).slice(0, 800); } catch {}
      console.log("[resp]", r.status(), u.replace("https://prism.openai.com", "").slice(0, 120), "|", b.slice(0, 400));
    }
  });

  console.log("== 进入项目 ==", PID);
  await page.goto("https://prism.openai.com/?u=" + PID + "&pg=1", {
    waitUntil: "domcontentloaded", timeout: 90000,
  });
  await page.waitForTimeout(20000);
  await page.screenshot({ path: path.join(SHOT_DIR, "3_probe_project.png") });

  const txt = (await page.evaluate(() => document.body.innerText)).slice(0, 800);
  console.log("== 页面文本 ==\n" + txt);
  console.log("== 抓到的 API 调用数 ==", interesting.length);
  await browser.close();
})().catch((e) => { console.error("FATAL", e); process.exit(1); });
