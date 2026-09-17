/**
 * 进入项目页并点击"文件" tab，mitm 抓文件列表请求。
 * 用法：node prism_mitm_files_tab.js <projectUuid>
 */
const fs = require("fs");
const path = require("path");
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const COOKIE_FILE = "F:/Code/Active/OAIprism/secrets/cookies.txt";
const PID = process.argv[2] || "f16e0bf9-30fc-4e8d-b092-158adcc46581";

function parseCookies(text) {
  const out = [];
  for (const pair of text.split(";")) {
    const i = pair.indexOf("=");
    if (i <= 0) continue;
    const n = pair.slice(0, i).trim(), v = pair.slice(i + 1).trim();
    if (n && v) out.push({ name: n, value: v, domain: "prism.openai.com", path: "/", secure: true, sameSite: "Lax" });
  }
  return out;
}

(async () => {
  const browser = await chromium.launch({
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true, proxy: { server: "http://127.0.0.1:18080" },
    args: ["--ignore-certificate-errors", "--no-first-run"],
  });
  const ctx = await browser.newContext({
    ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 },
    userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36",
  });
  await ctx.addCookies(parseCookies(fs.readFileSync(COOKIE_FILE, "utf-8").trim()));
  const page = await ctx.newPage();
  page.on("request", (r) => {
    const u = r.url();
    if (/prism\.openai\.com/.test(u) && /\/api\/|\/s\/sandboxes/.test(u) && !u.includes("_next") && !u.includes("datadog") && !u.includes("/ff/")) {
      console.log("[req]", r.method(), u.replace("https://prism.openai.com", "").slice(0, 140));
      if (r.postData()) console.log("      body:", r.postData().slice(0, 200));
    }
  });

  await page.goto("https://prism.openai.com/?u=" + PID + "&pg=1", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(18000);

  const clicked = await page.evaluate(() => {
    const els = Array.from(document.querySelectorAll("[role=tab],button,[role=button],a,div"))
      .filter((el) => (el.innerText || "").trim() === "文件");
    if (els.length) { els[0].click(); return true; }
    return false;
  });
  console.log("== 点击文件 tab:", clicked, "==");
  await page.waitForTimeout(12000);
  await page.screenshot({ path: "C:/Users/13080/AppData/Local/Temp/prism_shots/4_files_tab.png" });
  const txt = (await page.evaluate(() => document.body.innerText)).slice(0, 1000);
  console.log("== 文件 tab 页面文本 ==\n" + txt);
  await browser.close();
})().catch((e) => { console.error("FATAL", e); process.exit(1); });
