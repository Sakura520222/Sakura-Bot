import assert from "node:assert/strict";
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { chromium, devices } from "playwright-core";

const web = fileURLToPath(new URL("../", import.meta.url));
const baseline = process.env.WIKI_BASELINE_DIST;
const output = process.env.WIKI_RESULTS_DIR;
const chrome = process.env.CHROME_PATH;
if (output) fs.mkdirSync(output, { recursive: true });
const channel = { id: "123", title: "Sakura 开发频道", username: "sakura_dev", page_count: 240,
  updated_at: "2026-11-12T16:00:00Z", publication_id: "v1", coverage: {status:"accessible"} };
const cards = Array.from({length: 240}, (_, index) => ({
  id: "p"+index, slug: "article-"+index, title: "Sakura Wiki 知识条目 "+(index+1), topic: ["开发日志","使用指南","更新记录"][index%3],
  kind: "Topic", channel_id: channel.id, channel_title: channel.title, url: "/wiki/123/page/article-"+index,
  updated_at: channel.updated_at, revision_id:"r"+index, publication_id:"v1", evidence_count: 3,
  excerpt: "公开知识条目摘要。这里记录项目发布、配置与日常使用问题。"
}));
const claims = Array.from({length: 18}, (_, index) => ({key:"章节 "+(index+1),text:"**Sakura** 条目正文，包含可核对的来源。" + (index === 0 ? "\n\n![示例](https://example.invalid/image.png)" : ""), status:"current", confidence:.9, source_created_at:channel.updated_at, evidence:[{source_id:"s1",start:0,end:4,url:"https://t.me/sakura_dev/1"}]}));
const profile = {title:channel.title, coverage:channel.coverage, statistics:{message_count:120,avg_message_length:37}, topics:[{topic:"开发日志",message_count:45}],
  months:[{month:"2026-11",count:120}], hours_utc:[{hour:11,count:50}],as_of:channel.updated_at,publication_id:"v1"};
const graph = {nodes:[cards[0],cards[1]],edges:[{id:"e1",source:cards[0].id,target:cards[1].id,kind:"supports",confidence:.9,evidence_count:1}],publication_id:"v1",limit:60,truncated:false};
const compressed = new Map();
const mime = {".html":"text/html",".js":"application/javascript",".css":"text/css",".png":"image/png"};
const avatar = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aCXYAAAAASUVORK5CYII=","base64");
let coverUrl;
const server = http.createServer((req,res) => {
  const parsed = new URL(req.url,"http://localhost");
  if (parsed.pathname.startsWith("/api/")) {
    const endpoint = parsed.pathname.replace("/api/public/wiki/","");
    const q = parsed.searchParams;
    const start = Number(q.get("cursor") || 0);
    let out;
    if (endpoint.endsWith("/avatar")) { res.writeHead(200,{"content-type":"image/png","cache-control":"private,no-cache","etag":'"avatar"'}); return res.end(avatar); }
    if (endpoint === "channels") out = [channel];
    else if (endpoint === "visibility") out = {"123":"v1:1"};
    else if (endpoint === "feed") out = {items:cards.slice(start,start+20),has_more:start+20<cards.length,next_cursor:start+20<cards.length?String(start+20):null,terms:q.get("q")?.split(" ")||[]};
    else if (endpoint === "search") out = {items:cards.slice(0,Number(q.get("page_size")||20)),total:cards.length,page:1,page_size:20};
    else if (endpoint.endsWith("/timeline")) out = {items:[{...claims[0],title:cards[0].title,url:cards[0].url,event_id:"event1"}],total_pages:1,has_more:false,next_cursor:null};
    else if (endpoint.endsWith("/profile")) out = profile;
    else if (endpoint.endsWith("/graph")) out = graph;
    else if (endpoint.includes("/pages/") && q.get("part") === "related") out = {previous:cards[1],next:null};
    else if (endpoint.includes("/pages/")) {
      const card = cards.find(row => row.slug === endpoint.split("/").at(-1)) || cards[0];
      const refs = {sources:[{id:"s1",type:"message",url:"https://t.me/sakura_dev/1",created_at:channel.updated_at}],history:[{revision_id:card.revision_id,created_at:channel.updated_at}],sources_total:1,history_total:1};
      out = {...card,verified_at:channel.updated_at,...(q.get("part") !== "references" ? {claims}:{}),...(q.get("part") !== "article" ? refs:{})};
    }
    else if (endpoint === "channels/123") out = {...channel,cover_url:coverUrl,description:"Sakura 项目的更新与使用指南。",columns:["pages","graph","timeline","profile"]};
    else { res.writeHead(404); return res.end(); }
    res.writeHead(200,{"content-type":"application/json"}); return res.end(JSON.stringify(out));
  }
  const directory = baseline && req.socket.localPort === before.address()?.port ? baseline : path.join(web,"dist");
  const requested = path.join(directory, parsed.pathname);
  const filename = path.extname(requested) && fs.existsSync(requested) ? requested : path.join(directory,"index.html");
  res.setHeader("content-type",mime[path.extname(filename)]||"application/octet-stream");
  if (!compressed.has(filename)) compressed.set(filename,gzipSync(fs.readFileSync(filename)));
  res.setHeader("content-encoding","gzip");
  res.end(compressed.get(filename));
});
const before = http.createServer((req,res)=>server.emit("request",req,res));
const listen = (app) => new Promise((resolve, reject) => {
  app.once("error", reject);
  app.listen(0, "127.0.0.1", () => resolve(app.address().port));
});
const port = await listen(server);
const baselinePort = baseline ? await listen(before) : null;
const url = "http://127.0.0.1:" + port;
let browser;
try {
  browser = await chromium.launch({ ...(chrome ? { executablePath: chrome } : {}), args: ["--disable-gpu"] });
  const samples = [];
  for (const viewport of (baselinePort ? ["desktop","mobile4g"] : [])) {
    for (const [variant, serverPort] of [["before",baselinePort],["after",port]]) {
      for (let run = 0; run < 3; run++) {
        const context = await browser.newContext(viewport === "mobile4g" ? devices["Pixel 7"] : { viewport:{width:1440,height:950} });
        await context.addInitScript(() => {
          window.vitals = { lcp:0, cls:0, interaction:0 };
          new PerformanceObserver(list => { window.vitals.lcp = list.getEntries().at(-1)?.startTime ?? 0; }).observe({type:"largest-contentful-paint",buffered:true});
          new PerformanceObserver(list => { for(const item of list.getEntries()) if(!item.hadRecentInput) window.vitals.cls += item.value; }).observe({type:"layout-shift",buffered:true});
          new PerformanceObserver(list => { for(const item of list.getEntries()) if(item.interactionId) window.vitals.interaction = Math.max(window.vitals.interaction,item.duration); }).observe({type:"event",durationThreshold:16,buffered:true});
        });
        const page = await context.newPage();
        const cdp = await context.newCDPSession(page);
        if (viewport === "mobile4g") {
          await cdp.send("Network.enable");
          await cdp.send("Network.emulateNetworkConditions",{offline:false,latency:150,downloadThroughput:200000,uploadThroughput:90000,connectionType:"cellular4g"});
          await cdp.send("Emulation.setCPUThrottlingRate",{rate:4});
        }
        console.log("sample",viewport,variant,run);
        page.on("pageerror",error=>console.log("browser error",error.message));
        await page.goto("http://127.0.0.1:"+serverPort+"/wiki");
        await page.getByRole("heading",{name:"频道百科",exact:true}).waitFor();
        await page.getByRole("link",{name:cards[0].title,exact:true}).first().waitFor();
        await page.waitForLoadState("networkidle");
        const cold = await page.evaluate(() => {
          const nav = performance.getEntriesByType("navigation")[0];
          const resources = performance.getEntriesByType("resource");
          return {...window.vitals,ttfb:nav.responseStart-nav.requestStart, requests:resources.length,
            jsBytes:resources.filter(e=>e.name.endsWith(".js")).reduce((n,e)=>n+e.encodedBodySize,0),
            adminJS:resources.some(e=>e.name.includes("naive-ui-")),graphJS:resources.some(e=>e.name.includes("cytoscape"))};
        });
        const t = performance.now();
        await page.getByRole("link",{name:cards[0].title,exact:true}).first().click();
        await page.locator("article.wiki-article h1").waitFor();
        const articleMs = performance.now()-t;
        const back = performance.now();
        await page.evaluate(() => { setTimeout(() => history.back(), 0); });

        await page.getByRole("heading",{name:"频道百科",exact:true}).waitFor({timeout:7000});
        const backMs = performance.now()-back;
        const interaction = await page.evaluate(() => window.vitals.interaction).catch(() => 0);
        samples.push({viewport,variant,run,...cold,interaction,articleMs,backMs});

        await context.close();
      }
    }
  }
  console.log("mobile flow");
  const page = await browser.newPage({...devices["Pixel 7"], reducedMotion:"reduce", colorScheme:"light"});
  const errors = [];
  page.on("pageerror",error=>errors.push(error.message));
  let feeds = 0;
  page.on("request",request=>{ if(request.url().includes("/api/public/wiki/feed")) feeds++; });
  await page.goto(url + "/wiki");
  await page.locator(".wiki-feed-card").first().waitFor();
  for (let batch=1;batch<12;batch++) {
    const prev = feeds;
    await page.evaluate(()=>window.scrollTo(0, document.documentElement.scrollHeight));
    if (batch > 8) {
      await page.getByRole("button",{name:"加载更多",exact:true}).click();
    }
    await page.waitForFunction(value=>document.querySelector(".wiki-feed-end button")?.textContent?.trim() !== "加载中…" &&
      !!Array.from(document.querySelectorAll(".wiki-feed-card h2")).find(el=>el.textContent?.trim() === "Sakura Wiki 知识条目 "+value),batch*20+1);
    assert.equal(feeds,prev+1);
  }
  await page.getByText("已显示全部内容",{exact:true}).waitFor();
  assert.equal(feeds, 12);
  const bounded = await page.locator(".wiki-feed-card").count();
  assert.ok(bounded<100,"Mounted rows should stay bounded: "+bounded);
  const target = page.locator(".wiki-feed-card h2 a").last();
  await target.scrollIntoViewIfNeeded();
  const scroll = await page.evaluate(()=>scrollY);
  await target.click();
  await page.locator("article.wiki-article h1").waitFor();
  await page.evaluate(() => { setTimeout(() => history.back(), 0); });
  await page.locator(".wiki-feed-end").waitFor();
  console.log("scroll before",scroll,"after",await page.evaluate(()=>({scrollY,height:document.documentElement.scrollHeight})));
  await page.waitForFunction((saved)=>Math.abs(scrollY-saved)<600,scroll,{timeout:5000});
  console.log("search flow");
  await page.getByRole("link",{name:"探索",exact:true}).click();
  await page.getByLabel("搜索公开知识").fill("Sakura");
  await page.getByRole("button",{name:"搜索",exact:true}).click();
  await page.locator("mark").first().waitFor();
  await page.getByRole("navigation",{name:"站点导航"}).getByRole("link",{name:"频道",exact:true}).click();
  await page.getByRole("link",{name:/Sakura 开发频道/}).first().click();
  await page.getByRole("heading",{name:channel.title,exact:true}).waitFor();
  assert.equal(await page.locator(".wiki-channel-cover").count(), 0);
  await page.getByRole("link",{name:"动态",exact:true}).first().click();
  await page.getByRole("heading",{name:"时间轴",exact:true}).waitFor();
  await page.locator(".wiki-timeline-entry").waitFor();
  await page.getByRole("link",{name:"画像",exact:true}).click();
  await page.getByRole("button",{name:"活跃时段",exact:true}).click();
  await page.getByRole("button",{name:"11:00 UTC，50 条消息"}).click();
  await page.getByText("11:00 UTC · 50 条消息",{exact:true}).waitFor();
  await page.getByRole("link",{name:"知识地图",exact:true}).first().click();
  await page.locator(".wiki-graph-list button").first().click();
  const nodeDialog = page.getByRole("dialog");
  await nodeDialog.waitFor();
  const centered = await nodeDialog.evaluate((dialog) => {
    const { left, top, width, height } = dialog.getBoundingClientRect();
    return Math.abs(left + width / 2 - document.documentElement.clientWidth / 2) < 2 &&
      Math.abs(top + height / 2 - innerHeight / 2) < 2;
  });
  assert.ok(centered, "Node details open in the center of the viewport");
  await page.keyboard.press("Escape");
  await page.getByRole("dialog").waitFor({state:"hidden"});
  await page.route("https://example.invalid/image.png", route => route.fulfill({ contentType: "image/png", body: avatar }));
  await page.goto(url + "/wiki/123/page/article-0#claim-12");
  await page.locator("#claim-12").waitFor();
  await page.getByText("同主题的前后条目",{exact:true}).click();
  await page.getByRole("link",{name:/较早/}).waitFor();
  await page.getByText("查看来源",{exact:true}).click();
  await page.getByText("频道原帖",{exact:true}).waitFor();
  await page.getByRole("link",{name:"预览图片 · 示例"}).waitFor();
  await page.getByRole("link",{name:"预览图片 · 示例"}).click();
  await page.getByRole("dialog",{name:"图片预览"}).waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("dialog",{name:"图片预览"}).waitFor({state:"hidden"});
  await page.getByRole("button",{name:"本机收藏",exact:true}).click();
  await page.getByRole("button",{name:"取消收藏",exact:true}).waitFor();
  assert.equal(await page.getByRole("button",{name:"返回顶部",exact:true}).count(), 0);
  const mobileTools = page.getByRole("group",{name:"Wiki 工具"});
  await mobileTools.getByRole("button",{name:"切换到深色模式"}).click();
  assert.equal(await page.evaluate(()=>document.documentElement.classList.contains("dark")),true);
  await page.reload();
  await mobileTools.getByRole("button",{name:"切换到浅色模式"}).click();
  assert.equal(await page.evaluate(()=>document.documentElement.classList.contains("dark")),false);
  if (output) await page.screenshot({path:path.join(output,"mobile.png")});
  const desktop = await browser.newPage({viewport:{width:1440,height:900}});
  await desktop.goto(url + "/wiki");
  await desktop.locator(".wiki-feed-card").first().waitFor();
  const brand = desktop.getByRole("link",{name:"Sakura Wiki",exact:true});
  assert.equal(await brand.textContent(), "Sakura Wiki");
  const desktopTools = desktop.getByRole("group",{name:"Wiki 工具"});
  const settings = desktopTools.getByRole("link",{name:"管理 Wiki"});
  assert.equal(await settings.locator("svg").count(), 1);
  const settingsPosition = await settings.boundingBox();
  const contextPosition = await desktop.getByRole("complementary",{name:"相关导航"}).boundingBox();
  assert.ok(settingsPosition.y < contextPosition.y && settingsPosition.x > contextPosition.x + contextPosition.width / 2);
  if (output) await desktop.screenshot({path:path.join(output,"desktop.png")});
  await settings.click();
  await desktop.waitForURL("**/login");
  await desktop.getByText("登录",{exact:true}).first().waitFor();
  await desktop.route("https://example.invalid/cover.png", (route) => route.fulfill({ contentType:"image/png", body:avatar }));
  coverUrl = "https://example.invalid/cover.png";
  await desktop.goto(url + "/wiki/123");
  const cover = desktop.locator(".wiki-channel-cover img");
  await cover.waitFor();
  await desktop.waitForFunction(() => document.querySelector(".wiki-channel-cover img")?.naturalWidth > 0);
  await desktop.route("https://example.invalid/broken.png", (route) => route.abort());
  coverUrl = "https://example.invalid/broken.png";
  await desktop.reload();
  await desktop.getByRole("heading",{name:channel.title,exact:true}).waitFor();
  await cover.waitFor({state:"detached"});
  assert.deepEqual(errors,[]);
  if (output) fs.writeFileSync(path.join(output,"browser-results.json"),JSON.stringify({samples,bounded,feeds},null,2));
  console.log(JSON.stringify({samples,bounded,feeds}));
} finally {
  await browser?.close();
  server.close();
  before.close();
}
