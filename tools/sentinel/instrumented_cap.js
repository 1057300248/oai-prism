// 仪器化捕获：注入钩子到 sentinel sdk.js（iframe 上下文），记录
//   1) 所有 JSON.stringify(object) —— 累积对象原文
//   2) Math.random() 返回值序列
//   3) performance.now() 返回值序列
//   4) 最终 header token
const PW = "F:/Dev/Repository/npm/global/node_modules/@playwright/mcp/node_modules/playwright-core";
const { chromium } = require(PW);
const fs = require("fs");

const HOOK = `
(function(){
  window.__cap = { stringify: [], rand: [], now: [] };
  const _s = JSON.stringify;
  JSON.stringify = function(v, r, s) {
    try { if (v && typeof v === 'object') window.__cap.stringify.push(_s(v)); } catch(e){}
    return _s.apply(this, arguments);
  };
  const _r = Math.random;
  Math.random = function() { const x = _r.call(Math); window.__cap.rand.push(x); return x; };
  const _n = performance.now.bind(performance);
  performance.now = function() { const x = _n(); window.__cap.now.push(x); return x; };
})();
`;

(async () => {
  const browser = await chromium.launch({ headless: false, channel: "chrome" });
  const ctx = await browser.newContext({ viewport: null });
  const page = await ctx.newPage();
  const cap = { apiReq: null, sdkHooked: false, sentinelReq: null, sentinelResp: null };
  await ctx.route("**/backend-api/sentinel/req", async (route) => {
    const req = route.request();
    cap.sentinelReq = { body: req.postData() };
    const resp = await route.fetch();
    cap.sentinelResp = { status: resp.status(), body: await resp.text() };
    await route.fulfill({ response: resp });
  });

  await ctx.route("**/sentinel/*/sdk.js*", async (route) => {
    const resp = await route.fetch();
    let body = await resp.text();
    body = HOOK + body;
    cap.sdkHooked = true;
    await route.fulfill({ response: resp, body });
  });
  await ctx.route("**/api/projects", async (route) => {
    const req = route.request();
    cap.apiReq = {
      sentinelToken: (await req.allHeaders())["openai-sentinel-token"] ?? null,
      cookie: (await req.allHeaders())["cookie"] ?? null,
      ua: (await req.allHeaders())["user-agent"] ?? null,
    };
    await route.abort("failed"); // 不消费 token
  });

  const accts = JSON.parse(fs.readFileSync("F:/Code/Active/OAIprism/secrets/accounts.json", "utf-8"));
  const at = accts.accounts[0].cookies.match(/prism_oai_access_token=([^;\s]+)/)[1];
  await ctx.addCookies([{ name: "prism_oai_access_token", value: at, domain: "prism.openai.com", path: "/" }]);

  await page.goto("https://prism.openai.com/", { waitUntil: "domcontentloaded", timeout: 90000 });
  await page.waitForTimeout(12000);

  // 触发 token 生成（页面内 fetch，会被 abort）
  await page.evaluate(async () => {
    try {
      await fetch("/api/projects", {
        method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ project_uuid: crypto.randomUUID(), title: "x" }),
      });
    } catch (e) {}
  });
  await page.waitForTimeout(1500);

  // 收集所有框架的 __cap，合并 stringify
  cap.vm = { stringify: [], rand: [], now: [] };
  cap.frames = [];
  for (const frame of page.frames()) {
    try {
      const d = await frame.evaluate(() => (window.__cap ? JSON.parse(JSON.stringify(window.__cap)) : null));
      if (d) {
        cap.frames.push(frame.url().slice(0, 80));
        cap.vm.stringify.push(...(d.stringify || []));
        cap.vm.rand.push(...(d.rand || []));
        cap.vm.now.push(...(d.now || []));
      }
    } catch (e) {}
  }
  // 过滤出 sentinel 累积对象（键形如 N.NN）
  cap.acc = (cap.vm.stringify || []).filter((x) =>
    /^\{"\d+\.\d+":/.test(x) && x.length > 200);
  fs.writeFileSync("instrumented_capture.json", JSON.stringify(cap, null, 1));
  console.log("sdk 注入:", cap.sdkHooked, "| __cap:", cap.vm ? "已捕获" : "未捕获",
    "| stringify 调用:", cap.vm?.stringify?.length ?? 0);
  console.log("sentinel 累积对象数:", cap.acc?.length ?? 0);
  if (cap.acc?.length) {
    console.log("最后一个（最终累积对象）:");
    console.log(cap.acc[cap.acc.length - 1].slice(0, 600));
  }
  console.log("Math.random 调用数:", cap.vm.rand.length, "| 前5:", cap.vm.rand.slice(0, 5));
  await browser.close();
})().catch((e) => { console.error("ERR:", String(e).slice(0, 300)); process.exit(1); });
