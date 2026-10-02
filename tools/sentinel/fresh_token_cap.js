// 干净消融：捕获真实 token 但【不让浏览器消费】（abort 业务请求），
// 然后由 Go 首次使用该 token —— 区分"重放检测"与"客户端指纹"。
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");

(async () => {
  const browser = await chromium.launch({ headless: false, channel: "chrome" });
  const ctx = await browser.newContext({ viewport: null });
  const page = await ctx.newPage();

  const cap = { sentinelReq: null, sentinelResp: null, apiReq: null };

  await ctx.route("**/backend-api/sentinel/req", async (route) => {
    const req = route.request();
    cap.sentinelReq = { body: req.postData() };
    const resp = await route.fetch();
    cap.sentinelResp = { status: resp.status(), body: await resp.text() };
    await route.fulfill({ response: resp });
  });

  // 关键：读头后 abort —— token 签发了但从未被服务器消费
  await ctx.route("**/api/projects", async (route) => {
    const req = route.request();
    cap.apiReq = {
      body: req.postData(),
      sentinelToken: (await req.allHeaders())["openai-sentinel-token"] ?? null,
      cookie: (await req.allHeaders())["cookie"] ?? null,
      ua: (await req.allHeaders())["user-agent"] ?? null,
      allHeaders: await req.allHeaders(),
    };
    await route.abort("failed");
  });

  const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
  const at = accts.accounts[0].cookies.match(/prism_oai_access_token=([^;\s]+)/)[1];
  await ctx.addCookies([{
    name: "prism_oai_access_token", value: at,
    domain: "prism.openai.com", path: "/",
  }]);

  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(12000);

  const r = await page.evaluate(async () => {
    try {
      const rr = await fetch("/api/projects", {
        method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project_uuid: crypto.randomUUID(), title: "x" }),
      });
      return { status: rr.status };
    } catch (e) { return { err: String(e).slice(0, 80) }; }
  });
  console.log("浏览器请求（应为 aborted）:", JSON.stringify(r));
  fs.writeFileSync("fresh_token.json", JSON.stringify(cap, null, 1));
  console.log("token 已捕获且未消费:", cap.apiReq?.sentinelToken ? "YES" : "NO");
  await browser.close();
})().catch((e) => { console.error("ERR:", String(e).slice(0, 300)); process.exit(1); });
