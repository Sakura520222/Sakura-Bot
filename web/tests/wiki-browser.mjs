import assert from "node:assert/strict";
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { createHash } from "node:crypto";
import { chromium, devices } from "playwright-core";

const web = fileURLToPath(new URL("../", import.meta.url));
const baseline = process.env.WIKI_BASELINE_DIST;
const output = process.env.WIKI_RESULTS_DIR;
const chrome = process.env.CHROME_PATH;
if (output) fs.mkdirSync(output, { recursive: true });
const channel = { id: "123", title: "Sakura 开发频道", username: "sakura_dev", page_count: 240,
  updated_at: "2026-11-12T16:00:00Z", publication_id: "v1", coverage: {status:"accessible"} };
const cards = Array.from({length: 240}, (_, index) => ({
  id: index.toString(16).padStart(32, "0"), slug: "article-"+index, title: "Sakura Wiki 知识条目 "+(index+1), topic: ["开发日志","使用指南","更新记录"][index%3],
  kind: "Topic", channel_id: channel.id, channel_title: channel.title, url: "/wiki/123/page/article-"+index,
  updated_at: channel.updated_at, revision_id:"r"+index, publication_id:"v1", evidence_count: 3,
  excerpt: "公开知识条目摘要。这里记录项目发布、配置与日常使用问题。"
}));
const claims = Array.from({length: 18}, (_, index) => ({key:"章节 "+(index+1),text:"**Sakura** 条目正文，包含可核对的来源。" + (index === 0 ? "\n\n![示例](https://example.invalid/image.png)" : ""), status:"current", confidence:.9, source_created_at:channel.updated_at, evidence:[{source_id:"s1",start:0,end:4,url:"https://t.me/sakura_dev/1"}]}));
claims[0].evidence[0].quote = "原帖片段";
claims[0].fact_check = { status: "unverified", sources: [{ title: "外部参考", url: "https://example.org/reference" }] };
const annotatedClaims = [{ ...claims[0], text: "- 词条名称：**护**\n- 画面元素：卡片。\n- 角色形象：护",
  community_notes: [
    { kind: "annotation", excerpt: "护", text: "第一处的注解。", sources: [], contributor: { id: "c".repeat(32), display_name: "注解作者", avatar: false } },
    { kind: "annotation", excerpt: "护", excerpt_occurrence: 1, text: "第二处的注解。", sources: [] },
    { kind: "annotation", excerpt: "旧版词句", text: "保留的旧注解。", sources: [] },
  ] }, ...claims.slice(1)];
const profile = {title:channel.title, coverage:channel.coverage, statistics:{message_count:120,avg_message_length:37}, topics:[{topic:"开发日志",message_count:45}],
  months:[{month:"2026-11",count:120}], hours_utc:[{hour:11,count:50}],as_of:channel.updated_at,publication_id:"v1"};
const graph = {nodes:[cards[0],cards[1]],edges:[{id:"e1",source:cards[0].id,target:cards[1].id,kind:"supports",confidence:.9,evidence_count:1}],publication_id:"v1",limit:60,truncated:false};
const compressed = new Map();
const mime = {".html":"text/html",".js":"application/javascript",".css":"text/css",".png":"image/png"};
const avatar = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aCXYAAAAASUVORK5CYII=","base64");
let coverUrl;
let uploadedCover;
let accountEnabled = false;
let accountUser = null;
const accountBookmarks = new Map();
let assistantEnabled = false;
let assistantCheckedIn = false;
let articleHidden = false;
let researchSaved;
let submittedProposal;
const reviewProposalId = "d".repeat(32);
const contributor = {id:"e".repeat(32), display_name:"Cherry", username:"cherry", avatar:true, total:2, annotations:1, edits:1, sources:0};
let visibilityVersion = "v1:1";
const wikiEntrypoints = ["/wiki", "/wiki/search", "/wiki?tab=channels", "/wiki/bookmarks", "/wiki/contributors"];
let reviewProposalStatus = "pending";
const server = http.createServer(async (req,res) => {
  const parsed = new URL(req.url,"http://localhost");
  if (parsed.pathname.startsWith("/api/")) {
    const endpoint = parsed.pathname.replace("/api/public/wiki/","");
    const q = parsed.searchParams;
    const start = Number(q.get("cursor") || 0);
    let out;
    if (endpoint.endsWith("/avatar")) { res.writeHead(200,{"content-type":"image/png","cache-control":"private,no-cache","etag":'"avatar"'}); return res.end(avatar); }
    const adminChannel = {
      ...channel, enabled: true, access_verified: true, cover_digest: uploadedCover?.digest,
      config: { enabled: true, public: true, hidden_topics: [], public_base_url: "", columns: ["pages"] },
    };
    if (endpoint === "/api/wiki/account/session") out = { enabled: accountEnabled, user: accountUser, can_refresh: false, assistant_enabled: assistantEnabled };
    else if (endpoint === "/api/wiki/account/bookmarks") {
      if (!accountUser) { res.writeHead(401); return res.end(); }
      const saved = accountBookmarks.get(accountUser.id) ?? new Set();
      accountBookmarks.set(accountUser.id, saved);
      if (req.method === "POST") {
        assert.equal(req.headers["x-wiki-request"], "1");
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        const { add, remove } = JSON.parse(Buffer.concat(chunks));
        for (const id of add || []) saved.add(id);
        if (remove) saved.delete(remove);
        res.writeHead(204);
        return res.end();
      }
      out = { ids: [...saved].reverse() };
    }
    else if (endpoint === "/api/wiki/admin/account-settings") {
      let enabled = false;
      if (req.method === "PUT") {
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        enabled = JSON.parse(Buffer.concat(chunks)).enabled;
      }
      out = { enabled, origin: "https://wiki.example.test", has_secret: true, has_bot: true, assistant_enabled: enabled, assistant_checkin: true };
    }
    else if (endpoint === "/api/wiki/community/proposals" && req.method === "POST") {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      submittedProposal = JSON.parse(Buffer.concat(chunks));
      out = { id: "fixture-proposal", status: "pending" };
    }
    else if (endpoint === "/api/wiki/community/contributors") out = q.has("page") ? [] : [contributor];
    else if (endpoint === `/api/wiki/community/contributors/${contributor.id}`) out = [{ ...contributor,
      title: cards[0].title, slug: cards[0].slug, kind: "annotation", created_at: channel.updated_at,
      revision_id: cards[0].revision_id, channel_id: channel.id }];
    else if (endpoint === "/api/wiki/community/proposals" && req.method === "GET") out = [
      { id: "live", title: "当前版本里的建议", status: "approved", published: true, currently_public: true, channel_id: channel.id, page_id: cards[0].id },
      { id: "old", title: "旧版本里的建议", status: "approved", published: true, currently_public: false },
      { id: "pending-publication", title: "等待发布的建议", status: "approved", published: false, currently_public: false },
    ];
    else if (endpoint === "/api/wiki/admin/proposals" || /^\/api\/wiki\/admin\/pages\/[^/]+\/proposals$/.test(endpoint)) out = [];
    else if (endpoint === `/api/wiki/admin/proposals/${reviewProposalId}`) {
      if (req.method === "POST") {
        reviewProposalStatus = "approved";
        out = { status: "approved", job_id: "b".repeat(32) };
      } else out = { id: reviewProposalId, channel_id: "123", channel_title: channel.title, status: reviewProposalStatus,
        title: "审核样例", kind: "annotation", job_status: "paused", active_job: "a".repeat(32), created_at: channel.updated_at,
        original: "完整原文", payload: { claim: 0, excerpt: "所选测试片段", text: "新的说明", diff: "", sources: [] } };
    }
    else if (endpoint === "/api/wiki/admin/research") {
      const providers = { mwmbl: "https://api.mwmbl.org", langsearch: "https://api.langsearch.com", serper: "https://google.serper.dev", deepseek: "https://api.deepseek.com", tavily: "https://api.tavily.com", serpapi: "https://serpapi.com", wikipedia: "https://www.wikipedia.org", crossref: "https://api.crossref.org", github: "https://api.github.com" };
      if (req.method === "PUT") {
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        researchSaved = JSON.parse(Buffer.concat(chunks));
      }
      out = { enabled: researchSaved?.enabled ?? false, order: researchSaved?.order ?? Object.keys(providers).filter((name) => name !== "mwmbl"), result_limit: 5, timeout: 10, concurrency: 3, ttl_hours: 24,
        providers: Object.fromEntries(Object.entries(providers).map(([name, official_url]) => [name, { enabled: researchSaved?.providers[name]?.enabled ?? false, has_key: false, official_url, requires_key: !["mwmbl", "wikipedia", "crossref", "github"].includes(name), general_web: !["wikipedia", "crossref", "github"].includes(name) }])) };
    }
    else if (/^\/api\/wiki\/admin\/channels\/123\/pages\/[^/]+$/.test(endpoint)) {
      assert.equal(req.headers.authorization, "Bearer wiki-test-token");
      if (req.method === "POST") {
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        const { action } = JSON.parse(Buffer.concat(chunks));
        articleHidden = action === "hide";
        out = { success: true };
      } else out = { id: cards[0].id, title: cards[0].title, locked: false, hidden: articleHidden };
    }
    else if (endpoint === "/api/wiki/assistant/status") out = { point_units: assistantCheckedIn ? 25 : 0, free_used: 0, free_limit: 3, site_used: 2, site_limit: 100,
      checked_in: assistantCheckedIn, checkin_enabled: true, exempt: false, day: "2026-11-13", timezone: "Asia/Shanghai" };
    else if (endpoint === "/api/wiki/assistant/checkin") {
      assert.equal(req.headers["x-wiki-request"], "1");
      assistantCheckedIn = true;
      out = { units: 25, added: true };
    }
    else if (endpoint === "/api/wiki/assistant/chat") {
      assert.equal(req.headers["x-wiki-request"], "1");
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks));
      assert.match(body.id, /^[0-9a-f]{32}$/);
      assert.equal(body.question, "这个词条讲了什么？");
      assert.equal(body.article.channel, "123");
      res.writeHead(200, { "content-type": "text/event-stream" });
      res.write("event: accepted\ndata: true\n\n");
      setTimeout(() => res.end('event: text\ndata: "**小妲回答**"\n\nevent: done\ndata: {}\n\n'), 50);
      return;
    }
    else if (endpoint === "/api/wiki/account/start") {
      assert.equal(req.headers["x-wiki-request"], "1");
      out = { authorization_url: "https://oauth.telegram.org/auth?fixture=1" };
    }
    else if (endpoint === "/api/wiki/account/privacy" && req.method === "PUT") {
      assert.equal(req.headers["x-wiki-request"], "1");
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      accountUser.privacy = JSON.parse(Buffer.concat(chunks));
      out = accountUser.privacy;
    }
    else if (endpoint === "/api/wiki/account/logout" && req.method === "POST") {
      accountUser = null;
      res.writeHead(204);
      return res.end();
    }
    else if (endpoint === "/api/wiki/admin/status") {
      if (req.headers.authorization !== "Bearer wiki-test-token") {
        res.writeHead(401, { "content-type": "application/json" });
        return res.end('{}');
      }
      out = { admin: true };
    }
    else if (endpoint === "channels") out = [channel];
    else if (endpoint === "/api/wiki/admin/channels") out = [adminChannel];
    else if (endpoint === "/api/wiki/admin/channels/123/config") out = adminChannel;
    else if (endpoint === "/api/wiki/admin/channels/123/jobs") out = [];
    else if (endpoint === "/api/wiki/admin/channels/123/excluded") out = { total: 0, unverified_total: 0, reasons: [], items: [] };
    else if (endpoint === "/api/wiki/admin/channels/123/cover" && req.method === "PUT") {
      assert.equal(req.headers.authorization, "Bearer wiki-test-token");
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const image = Buffer.concat(chunks);
      uploadedCover = { image, digest: createHash("sha256").update(image).digest("hex") };
      out = { cover_digest: uploadedCover.digest };
    }
    else if (endpoint === "/api/wiki/admin/channels/123/cover" && req.method === "DELETE") {
      uploadedCover = undefined;
      out = { cover_digest: null };
    }
    else if (endpoint === "channels/123/cover" && uploadedCover) {
      res.writeHead(200, { "content-type": "image/png" });
      return res.end(uploadedCover.image);
    }
    else if (endpoint === "visibility") out = {"123":visibilityVersion};
    else if (endpoint === "bookmarks") {
      out = { items: q.getAll("id").map((id) => cards.find((card) => card.id === id)).filter(Boolean) };
    }
    else if (endpoint === "feed") out = {items:cards.slice(start,start+20),has_more:start+20<cards.length,next_cursor:start+20<cards.length?String(start+20):null,terms:q.get("q")?.split(" ")||[]};
    else if (endpoint === "search") out = {items:cards.slice(0,Number(q.get("page_size")||20)),total:cards.length,page:1,page_size:20};
    else if (endpoint.endsWith("/timeline")) out = {items:[{...claims[0],title:cards[0].title,url:cards[0].url,event_id:"event1"}],total_pages:1,has_more:false,next_cursor:null};
    else if (endpoint.endsWith("/profile")) out = profile;
    else if (endpoint.endsWith("/graph")) {
      if (q.get("node_id")) {
        out = {...graph,next_cursor:null};
      } else if (q.get("cursor")) {
        out = {...graph,nodes:cards.slice(24,36),edges:[],next_cursor:null};
      } else {
        const count = Number(q.get("limit") || 24);
        const nodes = cards.slice(0, count);
        const edges = nodes.slice(1).map((node, index) => ({
          ...graph.edges[0], id: `edge-${index}`, source: nodes[Math.floor(index / 4)].id, target: node.id,
        }));
        out = {...graph,nodes,edges,next_cursor:"graph-second-batch"};
      }
    }
    else if (endpoint.includes("/pages/") && q.get("part") === "related") out = {previous:cards[1],next:null};
    else if (endpoint.endsWith("/article-1/compare")) {
      assert.equal(q.get("before"), "d".repeat(32));
      assert.equal(q.get("after"), "r1");
      out = {before:{id:"old-r1",created_at:"2026-11-11T16:00:00Z"},after:{id:"r1",created_at:channel.updated_at},
        changes:[{before:"旧正文",after:"新正文"},{before:null,after:"第一处的注解。"}]};
    }
    else if (endpoint.includes("/pages/")) {
      const card = cards.find(row => row.slug === endpoint.split("/").at(-1)) || cards[0];
      if (articleHidden && card === cards[0]) { res.writeHead(404); return res.end(); }
      const history = [{revision_id:card.revision_id,created_at:channel.updated_at}];
      if (card === cards[1]) history.push({revision_id:"d".repeat(32),created_at:"2026-11-11T16:00:00Z"});
      const historical = card === cards[1] && q.get("revision") === history[1].revision_id;
      const version = historical ? history[1] : history[0];
      const refs = {sources:[{id:"s1",type:"message",url:"https://t.me/sakura_dev/1",created_at:channel.updated_at}],history,sources_total:1,history_total:history.length};
      out = {...card,revision_id:version.revision_id,current_revision_id:card.revision_id,updated_at:version.created_at,verified_at:version.created_at,
        proposals_enabled:!historical,annotations_enabled:!historical,history_total:history.length,
        sources: refs.sources,
        ...(q.get("part") !== "references" ? {claims:historical ? [{...claims[0], text:"旧版词条正文"}] : card === cards[1] ? annotatedClaims : claims}:{}),
        ...(q.get("part") !== "article" ? refs:{})};
    }
    else if (endpoint === "channels/123") out = {
      ...channel,
      cover_url: uploadedCover ? `/api/public/wiki/channels/123/cover?v=${uploadedCover.digest}` : coverUrl,
      description:"Sakura 项目的更新与使用指南。",columns:["pages","graph","timeline","profile"],
    };
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
async function assertCentered(dialog) {
  await dialog.waitFor();
  await dialog.evaluate(async (element) => {
    await Promise.allSettled(element.getAnimations().map((animation) => animation.finished));
    const { left, top, width, height } = element.getBoundingClientRect();
    const x = left + width / 2 - document.documentElement.clientWidth / 2;
    const y = top + height / 2 - innerHeight / 2;
    if (Math.abs(x) >= 2 || Math.abs(y) >= 2) throw new Error(`Dialog center offset: ${x}px, ${y}px`);
  });
}
async function assertHomeTabAlignment(page) {
  const tabs = page.getByRole("navigation", { name: "首页栏目" });
  const feed = page.locator(".wiki-feed-card").first();
  await feed.waitFor();
  const before = { tabs: (await tabs.boundingBox()).y, feed: (await feed.boundingBox()).y };
  await tabs.getByRole("link", { name: "最新" }).click();
  await page.waitForURL("**/wiki?tab=latest");
  await feed.waitFor();
  assert.deepEqual({ tabs: (await tabs.boundingBox()).y, feed: (await feed.boundingBox()).y }, before);
  await tabs.getByRole("link", { name: "推荐" }).click();
  await page.waitForURL("**/wiki");
  await feed.waitFor();
}
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
  const desktopChrome = await chromium.launch({
    ...(chrome ? { executablePath: chrome } : {}),
    args: ["--disable-features=OverlayScrollbar"],
    ignoreDefaultArgs: ["--hide-scrollbars"],
  });
  const layout = await desktopChrome.newPage({viewport:{width:1250,height:750}});
  try {
    let expectedColumns;
    for (const entrypoint of wikiEntrypoints) {
      await layout.goto(url + entrypoint);
      await layout.locator(".wiki-view h1").first().waitFor();
      const dimensions = await layout.locator(".wiki-columns").evaluate((shell) => {
        const read = () => ["navigation", "main", "context"].map(name => {
          const { x, width } = shell.querySelector(".wiki-" + name).getBoundingClientRect();
          return [x, width];
        });
        const root = document.documentElement;
        root.style.overflowY = "scroll";
        const withScrollbar = read();
        root.style.overflowY = "hidden";
        const withoutScrollbar = read();
        root.style.removeProperty("overflow-y");
        return { withScrollbar, withoutScrollbar };
      });
      assert.deepEqual(dimensions.withScrollbar, dimensions.withoutScrollbar, entrypoint);
      expectedColumns ??= dimensions.withScrollbar;
      assert.deepEqual(dimensions.withScrollbar, expectedColumns, entrypoint);
    }
  } finally {
    await desktopChrome.close();
  }
  console.log("mobile flow");
  const page = await browser.newPage({...devices["Pixel 7"], reducedMotion:"reduce", colorScheme:"light"});
  const errors = [];
  page.on("pageerror",error=>errors.push(error.message));
  let feeds = 0;
  page.on("request",request=>{ if(request.url().includes("/api/public/wiki/feed")) feeds++; });
  await page.goto(url + "/wiki");
  await page.locator(".wiki-feed-card").first().waitFor();
  const mobileTabs = await browser.newPage({ ...devices["Pixel 7"] });
  await mobileTabs.goto(url + "/wiki");
  await assertHomeTabAlignment(mobileTabs);
  await mobileTabs.close();
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
  await page.getByRole("button", { name: "返回", exact: true }).click();
  await page.locator(".wiki-feed-end").waitFor();
  console.log("scroll before",scroll,"after",await page.evaluate(()=>({scrollY,height:document.documentElement.scrollHeight})));
  await page.waitForFunction((saved)=>Math.abs(scrollY-saved)<600,scroll,{timeout:5000});
  console.log("search flow");
  await page.getByRole("link",{name:"探索",exact:true}).click();
  await page.getByLabel("搜索公开知识").fill("Sakura");
  await page.getByRole("button",{name:"搜索",exact:true}).click();
  await page.locator("mark").first().waitFor();
  await page.getByRole("navigation",{name:"站点导航"}).getByRole("link",{name:"频道",exact:true}).click();
  await page.getByRole("heading",{name:"频道目录",exact:true}).waitFor();
  assert.equal(await page.getByRole("navigation",{name:"首页栏目"}).count(), 0);
  await page.locator(".wiki-directory-row").first().click();
  await page.getByRole("heading",{name:channel.title,exact:true}).waitFor();
  assert.equal(await page.locator(".wiki-channel-cover").count(), 0);
  await page.getByRole("link",{name:"动态",exact:true}).first().click();
  await page.getByRole("heading",{name:"时间轴",exact:true}).waitFor();
  await page.locator(".wiki-timeline-entry").waitFor();
  await page.getByRole("link",{name:"画像",exact:true}).click();
  await page.locator(".wiki-article-toolbar").getByRole("heading", { name:"频道画像" }).waitFor();
  assert.equal(await page.locator(".wiki-breadcrumb").count(), 0);
  await page.getByRole("button", { name:"返回", exact:true }).click();
  await page.waitForURL("**/timeline");
  await page.getByRole("link",{name:"画像",exact:true}).click();
  await page.getByRole("button",{name:"活跃时段",exact:true}).click();
  await page.getByRole("button",{name:"11:00 UTC，50 条消息"}).click();
  await page.getByText("11:00 UTC · 50 条消息",{exact:true}).waitFor();
  await page.getByRole("link",{name:"知识地图",exact:true}).first().click();
  await page.locator(".wiki-article-toolbar").getByRole("heading", { name:"知识地图" }).waitFor();
  assert.equal(await page.locator(".wiki-breadcrumb").count(), 0);
  await page.getByRole("button", { name:"返回", exact:true }).click();
  await page.waitForURL("**/profile");
  await page.getByRole("link",{name:"知识地图",exact:true}).first().click();
  const nodePage = page.getByRole("navigation", { name:"节点分页" });
  await nodePage.getByRole("button", { name:"下一批" }).click();
  await page.locator(".wiki-graph-list button").getByText(cards[24].title, { exact:true }).waitFor();
  assert.equal(await nodePage.getByRole("button", { name:"下一批" }).isDisabled(), true);
  await nodePage.getByRole("button", { name:"上一批" }).click();
  await page.locator(".wiki-graph-list button").getByText(cards[0].title, { exact:true }).waitFor();
  await page.locator(".wiki-graph-canvas canvas").first().waitFor();
  if (output) await page.screenshot({path:path.join(output,"graph-mobile.png")});
  await page.locator(".wiki-graph-list button").first().click();
  const nodeDialog = page.getByRole("dialog");
  await assertCentered(nodeDialog);
  await nodeDialog.getByRole("button", { name:"查看邻居" }).click();
  await page.getByRole("button", { name:"返回节点总览" }).waitFor();
  await page.locator(".wiki-graph-list button").getByText(cards[1].title, { exact:true }).waitFor();
  await page.getByRole("button", { name:"返回节点总览" }).click();
  await page.getByRole("navigation", { name:"节点分页" }).waitFor();
  await page.locator(".wiki-graph-list button").first().click();
  await page.keyboard.press("Escape");
  await page.getByRole("dialog").waitFor({state:"hidden"});
  await page.route("https://example.invalid/image.png", route => route.fulfill({ contentType: "image/png", body: avatar }));
  await page.goto(url + "/wiki/123/page/article-0#claim-12");
  await page.locator("#claim-12").waitFor();
  const relatedEntries = page.getByRole("navigation", { name: "相关条目" });
  await relatedEntries.getByRole("link",{name:/较早/}).waitFor();
  await relatedEntries.getByRole("link", { name: "浏览同主题条目" }).waitFor();
  await page.locator("#claim-0").getByRole("link", { name: "查看来源 1" }).click();
  await page.waitForURL("**#source-1");
  await page.locator("#source-1:focus").waitFor();
  assert.equal(await page.getByRole("link", { name: "频道原帖", exact: true }).count(), 1);
  const source = page.locator("#source-1");
  const excerpt = source.getByText("原帖片段", { exact: true });
  await excerpt.waitFor({ state: "hidden" });
  await source.getByText("查看摘录", { exact: true }).click();
  await excerpt.waitFor();
  await source.getByText("查看摘录", { exact: true }).click();
  await excerpt.waitFor({ state: "hidden" });
  await page.locator("#source-2").getByRole("link", { name: "外部参考" }).waitFor();
  await page.locator("#claim-0").getByText("未获外部证据", { exact: true }).waitFor();
  await page.getByRole("link",{name:"预览图片 · 示例"}).waitFor();
  await page.getByRole("link",{name:"预览图片 · 示例"}).click();
  await assertCentered(page.getByRole("dialog",{name:"图片预览"}));
  await page.keyboard.press("Escape");
  await page.getByRole("dialog",{name:"图片预览"}).waitFor({state:"hidden"});
  const actions = page.getByRole("group", { name: "文章操作" });
  assert.equal(await actions.locator("button svg").count(), 2);
  const titleRow = await page.locator(".wiki-reader-title h1").boundingBox();
  const actionsRow = await actions.boundingBox();
  assert.ok(actionsRow.x >= titleRow.x + titleRow.width && Math.abs(actionsRow.y - titleRow.y) < 30);
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: url });
  await actions.getByRole("button", { name: "更多词条操作" }).click();
  await page.getByRole("menuitem", { name: "复制链接" }).click();
  await page.getByText("已复制", { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), url + "/wiki/123/page/article-0#source-1");
  await actions.getByRole("button",{name:"收藏到本机",exact:true}).click();
  await page.getByRole("button",{name:"取消收藏",exact:true}).waitFor();
  assert.equal(await page.getByRole("button",{name:"返回顶部",exact:true}).count(), 0);
  const mobileTools = page.getByRole("group",{name:"Wiki 工具"});
  await mobileTools.getByRole("button",{name:"切换到深色模式"}).click();
  assert.equal(await page.evaluate(()=>document.documentElement.classList.contains("dark")),true);
  await page.reload();
  await mobileTools.getByRole("button",{name:"切换到浅色模式"}).click();
  assert.equal(await page.evaluate(()=>document.documentElement.classList.contains("dark")),false);
  const mobileHistory = page.getByRole("button", { name: "修订历史", exact: true });
  await mobileHistory.tap();
  await assertCentered(page.getByRole("dialog", { name: "修订历史" }));
  await page.getByRole("button", { name: "关闭修订历史" }).tap();
  if (output) await page.screenshot({path:path.join(output,"mobile.png")});
  const hiddenBookmark = "f".repeat(32);
  await page.evaluate(([legacyId, missingId]) => {
    localStorage.setItem("sakura-wiki-saved:" + legacyId, "1");
    localStorage.setItem("sakura-wiki-saved:" + missingId, "1");
  }, [cards[1].id, hiddenBookmark]);
  await page.getByRole("navigation", { name: "站点导航" }).getByRole("link", { name: "收藏", exact: true }).click();
  await page.waitForURL(url + "/wiki/bookmarks");
  await page.getByRole("link", { name: cards[0].title, exact: true }).waitFor();
  await page.getByRole("link", { name: cards[1].title, exact: true }).waitFor();
  await page.reload();
  await page.getByRole("button", { name: `取消收藏 ${cards[0].title}` }).click();
  await page.getByRole("button", { name: `取消收藏 ${cards[1].title}` }).click();
  await page.getByRole("button", { name: "移除不可访问的收藏" }).click();
  await page.getByText("还没有收藏", { exact: true }).waitFor();
  const desktop = await browser.newPage({viewport:{width:1440,height:900}});
  await desktop.goto(url + "/wiki");
  await desktop.locator(".wiki-feed-card").first().waitFor();
  const firstFeedMeta = desktop.locator(".wiki-feed-card").first().locator(".wiki-feed-meta");
  assert.equal(await firstFeedMeta.getByText("主题", { exact: true }).count(), 0);
  await firstFeedMeta.getByRole("link", { name: cards[0].topic, exact: true }).waitFor();
  await firstFeedMeta.getByRole("link", { name: `${cards[0].evidence_count} 个来源`, exact: true }).waitFor();
  assert.equal(await desktop.getByRole("navigation", { name: "站点导航" }).getByRole("link", { name: "收藏", exact: true }).getAttribute("href"), "/wiki/bookmarks");
  const tabs = desktop.getByRole("navigation", { name: "首页栏目" });
  assert.deepEqual(await tabs.getByRole("link").allTextContents(), ["推荐", "最新"]);
  await assertHomeTabAlignment(desktop);
  const firstRecommendation = request => {
    const target = new URL(request.url());
    return target.pathname === "/api/public/wiki/feed" && target.searchParams.get("mode") === "recommended" && !target.searchParams.has("cursor");
  };
  let recommendationRequests = 0;
  const observeRecommendation = request => { if (firstRecommendation(request)) recommendationRequests++; };
  desktop.on("request", observeRecommendation);
  const visibility = desktop.waitForResponse(response => response.url().endsWith("/api/public/wiki/visibility"));
  visibilityVersion = "v2:2";
  await desktop.clock.setFixedTime(new Date(Date.now() + 31_000));
  await desktop.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await visibility;
  await desktop.getByRole("link", { name: "贡献", exact: true }).click();
  await desktop.getByRole("heading", { name: "社区贡献" }).waitFor();
  await desktop.getByRole("link", { name: "首页", exact: true }).click();
  await desktop.locator(".wiki-feed-card").first().waitFor();
  assert.equal(recommendationRequests, 0);
  const changed = desktop.waitForRequest(firstRecommendation);
  await desktop.getByRole("button", { name: "换一批", exact: true }).click();
  assert.ok(new URL((await changed).url()).searchParams.has("refresh"));
  await desktop.locator(".wiki-feed-card").first().waitFor();
  const reloaded = desktop.waitForRequest(firstRecommendation);
  await desktop.reload();
  await reloaded;
  desktop.off("request", observeRecommendation);
  const brand = desktop.getByRole("link",{name:"Sakura Wiki",exact:true});
  assert.equal(await brand.textContent(), "Sakura Wiki");
  const desktopTools = desktop.getByRole("group",{name:"Wiki 工具"});
  const settings = desktopTools.getByRole("link",{name:"管理 Wiki"});
  assert.equal(await settings.count(), 0);
  await desktop.evaluate(() => localStorage.setItem("sakura_bot_token", "wiki-test-token"));
  await desktop.reload();
  await settings.waitFor();
  assert.equal(await settings.locator("svg").count(), 1);
  const settingsPosition = await settings.boundingBox();
  const contextPosition = await desktop.getByRole("complementary",{name:"相关导航"}).boundingBox();
  assert.ok(settingsPosition.y < contextPosition.y && settingsPosition.x > contextPosition.x + contextPosition.width / 2);
  const columnWidths = await desktop.locator(".wiki-columns").evaluate((grid) => getComputedStyle(grid).gridTemplateColumns);
  assert.equal(columnWidths, "200px 820px 285px");
  if (output) await desktop.screenshot({path:path.join(output,"desktop.png")});
  await desktop.evaluate(() => localStorage.removeItem("sakura_bot_token"));
  await settings.click();
  await desktop.waitForURL("**/login");
  await desktop.getByText("登录",{exact:true}).first().waitFor();
  accountEnabled = true;
  assistantEnabled = true;
  await desktop.goto(url + "/wiki");
  await desktop.getByRole("button", { name: "Telegram 登录", exact: true }).click();
  await assertCentered(desktop.getByRole("dialog", { name: "使用 Telegram 登录" }));
  await desktop.keyboard.press("Escape");
  await desktop.getByRole("button", { name: "小妲助手", exact: true }).click();
  await assertCentered(desktop.getByRole("dialog", { name: "小妲助手" }));
  await desktop.keyboard.press("Escape");
  const accountPage = await browser.newPage({ ...devices["iPhone 13"] });
  await accountPage.route("https://oauth.telegram.org/auth?fixture=1", (route) => route.fulfill({ body: "Telegram fixture" }));
  await accountPage.goto(url + "/wiki");
  await accountPage.getByRole("button", { name: "Telegram 登录", exact: true }).click();
  await assertCentered(accountPage.getByRole("dialog", { name: "使用 Telegram 登录" }));
  await accountPage.getByText("Wiki 会使用你的 Telegram 数字 ID", { exact: false }).waitFor();
  await accountPage.getByRole("button", { name: "前往 Telegram 授权" }).click();
  await accountPage.waitForURL("https://oauth.telegram.org/auth?fixture=1");
  accountUser = { id: "public-fixture", display_name: "Wiki 测试用户", username: "fixture", avatar: false,
    privacy: { show_avatar: false, show_username: false, anonymous_contributions: true } };
  await accountPage.goto(url + "/wiki");
  const accountTrigger = accountPage.getByRole("button", { name: "Wiki 测试用户" });
  await accountTrigger.locator(".wiki-user-avatar").getByText("W测", { exact: true }).waitFor();
  await accountTrigger.click();
  await assertCentered(accountPage.getByRole("dialog", { name: "Wiki 账户" }));
  assert.equal(await accountPage.getByRole("link", { name: "管理 Wiki" }).count(), 0);
  await accountPage.getByRole("switch", { name: "匿名署名" }).uncheck();
  await accountPage.getByRole("switch", { name: "公开 @用户名" }).check();
  await accountPage.getByRole("button", { name: "保存隐私设置" }).click();
  await accountPage.getByText("隐私设置已保存。", { exact: true }).waitFor();
  assert.deepEqual(accountUser.privacy, { show_avatar: false, show_username: true, anonymous_contributions: false });
  await accountPage.getByRole("button", { name: "退出登录" }).click();
  await accountPage.getByRole("button", { name: "Telegram 登录", exact: true }).waitFor();
  accountUser = { id: "public-fixture", display_name: "Wiki 测试用户", username: null, avatar: true,
    privacy: { show_avatar: false, show_username: false, anonymous_contributions: true } };
  assistantEnabled = true;
  await accountPage.evaluate((id) => localStorage.setItem("sakura-wiki-saved:" + id, String(Date.now())), cards[1].id);
  await accountPage.goto(url + "/wiki/123/page/article-0");
  await accountTrigger.locator("img").waitFor();
  await accountPage.getByRole("button", { name: "社区贡献", exact: true }).click();
  await accountPage.getByText("暂无公开贡献。", { exact: true }).waitFor();
  assert.equal(await accountPage.getByText("我的修订建议", { exact: true }).count(), 0);
  await accountPage.getByRole("button", { name: "收藏到账号", exact: true }).click();
  await accountPage.getByRole("button", { name: "取消收藏", exact: true }).waitFor();
  assert.ok(accountBookmarks.get(accountUser.id).has(cards[0].id));
  assert.ok(accountBookmarks.get(accountUser.id).has(cards[1].id));
  assert.equal(await accountPage.evaluate((id) => localStorage.getItem("sakura-wiki-saved:" + id), cards[1].id), null);
  const otherDevice = await browser.newPage();
  await otherDevice.goto(url + "/wiki/123/page/article-0");
  await otherDevice.getByRole("button", { name: "取消收藏", exact: true }).waitFor();
  await otherDevice.close();
  await accountPage.reload();
  await accountPage.getByRole("button", { name: "取消收藏", exact: true }).click();
  await accountPage.getByRole("button", { name: "收藏到账号", exact: true }).waitFor();
  assert.ok(!accountBookmarks.get(accountUser.id).has(cards[0].id));
  await accountPage.goto(url + "/wiki/contributors");
  await accountPage.getByRole("link", { name: "我的修订建议", exact: true }).click();
  for (const [title, status] of [["当前版本里的建议", "已发布"], ["旧版本里的建议", "当前未公开"], ["等待发布的建议", "已批准"]]) {
    await accountPage.getByRole("listitem").filter({ hasText: title }).getByText(status, { exact: true }).waitFor();
  }
  const publishedProposal = accountPage.getByRole("link", { name: /当前版本里的建议/ });
  assert.equal(await publishedProposal.getAttribute("href"), `/wiki/123/page/${cards[0].id}`);
  assert.equal(await accountPage.getByRole("link", { name: /旧版本里的建议|等待发布的建议/ }).count(), 0);
  await publishedProposal.click();
  await accountPage.waitForURL(`**/wiki/123/page/${cards[0].id}`);
  await accountPage.getByRole("heading", { name: cards[0].title, exact: true }).waitFor();
  await accountPage.goBack();
  assert.equal(await accountPage.getByRole("list", { name: "公开贡献者" }).count(), 0);
  await accountPage.getByRole("link", { name: "贡献者", exact: true }).click();
  await accountPage.getByRole("list", { name: "公开贡献者" }).getByText("Cherry", { exact: true }).waitFor();
  await accountPage.goto(url + "/wiki/123/page/article-0");
  assert.equal(await accountTrigger.locator("img").getAttribute("src"), "/api/wiki/account/avatar");
  await accountPage.getByRole("button", { name: "更多词条操作" }).click();
  await accountPage.getByRole("menuitem", { name: "询问当前词条" }).click();
  const assistant = accountPage.getByRole("dialog", { name: "小妲助手" });
  await assistant.getByRole("button", { name: "签到", exact: true }).click();
  await assertCentered(assistant);
  await assistant.getByText("积分 2.5", { exact: true }).waitFor();
  const composer = assistant.getByRole("textbox", { name: "问题" });
  await composer.fill("这个词条讲了什么？");
  await composer.press("Enter");
  assert.equal(await composer.inputValue(), "这个词条讲了什么？\n");
  await composer.dispatchEvent("keydown", { key: "Enter", ctrlKey: true, isComposing: true });
  assert.equal(await composer.inputValue(), "这个词条讲了什么？\n");
  await composer.press("Control+Enter");
  await assistant.locator("strong").getByText("小妲回答", { exact: true }).waitFor();
  await assistant.getByRole("button", { name: "收起助手" }).click();
  await accountPage.locator(".wiki-markdown[data-claim='0'] strong").first().evaluate((element) => {
    const selection = window.getSelection();
    const range = document.createRange(); range.selectNodeContents(element);
    selection.removeAllRanges(); selection.addRange(range);
  });
  const tools = accountPage.getByRole("toolbar", { name: "选中文字操作" });
  await tools.getByRole("button", { name: "添加注解" }).click();
  const note = accountPage.getByRole("dialog", { name: "添加注解" });
  await assertCentered(note);
  await note.getByRole("textbox", { name: "说明" }).fill("这段需要补充来源。");
  await note.getByRole("button", { name: "提交审核" }).click();
  await note.waitFor({ state: "hidden" });
  await accountPage.getByRole("status").filter({ hasText: "已提交，等待管理员审核。" }).waitFor();
  assert.deepEqual([submittedProposal.claim, submittedProposal.excerpt, submittedProposal.kind, submittedProposal.excerpt_occurrence], [0, "Sakura", "annotation", 0]);
  await accountPage.locator(".wiki-markdown[data-claim='0'] strong").first().evaluate((element) => {
    const selection = window.getSelection();
    const range = document.createRange(); range.selectNodeContents(element);
    selection.removeAllRanges(); selection.addRange(range);
  });
  await tools.getByRole("button", { name: "建议修订" }).click();
  const edit = accountPage.getByRole("dialog", { name: "提交修订建议" });
  await assertCentered(edit);
  await edit.getByRole("textbox", { name: "替换选中文字" }).fill("Saku🟢a");
  await edit.getByText("查看差异", { exact: true }).click();
  const removed = edit.locator(".revision-diff del");
  const added = edit.locator(".revision-diff ins");
  assert.equal(await removed.textContent(), "r");
  assert.equal(await added.textContent(), "🟢");
  assert.equal(await removed.evaluate((element) => getComputedStyle(element).backgroundColor), "rgb(251, 218, 218)");
  assert.equal(await added.evaluate((element) => getComputedStyle(element).backgroundColor), "rgb(214, 243, 223)");
  await edit.getByRole("button", { name: "提交审核" }).click();
  await edit.waitFor({ state: "hidden" });
  await accountPage.getByRole("status").filter({ hasText: "已提交，等待管理员审核。" }).waitFor();
  assert.equal(submittedProposal.text, claims[0].text.replace("Sakura", "Saku🟢a"));
  assistantEnabled = false;
  accountUser = null;
  await accountPage.close();
  accountEnabled = false;
  await desktop.goto(url + "/wiki/123/page/article-0#claim-12");
  await desktop.locator("#claim-12").waitFor();
  const back = desktop.getByRole("button", { name: "返回", exact: true });
  const toolbar = desktop.locator(".wiki-article-toolbar");
  await desktop.waitForFunction(() => {
    const section = document.querySelector("#claim-12");
    const toolbar = document.querySelector(".wiki-article-toolbar");
    return !!section && !!toolbar && section.getBoundingClientRect().top >= toolbar.getBoundingClientRect().bottom;
  });
  assert.equal(await toolbar.locator("svg").count(), 1);
  await back.click();
  await desktop.waitForURL(url + "/wiki/123");
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
  await desktop.evaluate(() => localStorage.setItem("sakura_bot_token", "wiki-test-token"));
  await desktop.goto(url + "/wiki/admin");
  await desktop.getByText("社区与联网", { exact: true }).click();
  await desktop.getByText("https://api.deepseek.com", { exact: true }).waitFor();
  await desktop.getByText("百科条目 · 无需密钥", { exact: true }).waitFor();
  await desktop.getByText("没有待审修订建议", { exact: true }).waitFor();
  assert.equal(await desktop.getByText("每日成本上限", { exact: true }).count(), 0);
  await desktop.getByRole("switch", { name: "启用 Mwmbl" }).click();
  await desktop.getByRole("button", { name: "调整 Mwmbl 的顺序" }).dragTo(desktop.locator('[data-provider="langsearch"]'), { targetPosition: { x: 5, y: 10 } });
  await desktop.getByRole("button", { name: "保存联网设置" }).click();
  await desktop.getByText("联网设置已保存。", { exact: true }).waitFor();
  assert.deepEqual(researchSaved.providers.deepseek, { enabled: false });
  assert.deepEqual(researchSaved.providers.mwmbl, { enabled: true });
  assert.deepEqual(researchSaved.order.slice(0, 2), ["mwmbl", "langsearch"]);
  assert.equal(researchSaved.daily_calls, undefined);
  if (output) await desktop.screenshot({path:path.join(output,"wiki-search-settings.png")});
  await desktop.getByText("设置", { exact: true }).click();
  await desktop.getByText("Telegram 登录与小妲助手", { exact: true }).waitFor();
  await desktop.getByRole("button", { name: "保存登录与助手设置" }).click();
  await desktop.getByText("设置已生效。", { exact: true }).waitFor();
  const uploadButton = desktop.getByRole("button", { name: "上传本机图片" });
  const [fileChooser] = await Promise.all([desktop.waitForEvent("filechooser"), uploadButton.click()]);
  await fileChooser.setFiles({ name: "cover.png", mimeType: "image/png", buffer: avatar });
  await desktop.getByText("封面已上传", { exact: true }).waitFor();
  assert.deepEqual(uploadedCover.image, avatar);
  await desktop.goto(url + "/wiki/123");
  await cover.waitFor();
  assert.match(await cover.getAttribute("src"), /^\/api\/public\/wiki\/channels\/123\/cover\?v=/);
  await desktop.goto(url + "/wiki/admin");
  await desktop.getByRole("button", { name: "移除本机封面" }).click();
  await desktop.getByText("本机封面已移除", { exact: true }).waitFor();
  assert.equal(uploadedCover, undefined);
  await desktop.getByText("任务", { exact: true }).click();
  await desktop.getByRole("button", { name: /查看原因/ }).click();
  await desktop.getByText("没有符合条件的记录", { exact: true }).waitFor();
  await assertCentered(desktop.locator(".n-modal").filter({ hasText: "未纳入知识生成的消息" }));
  await desktop.keyboard.press("Escape");
  await desktop.goto(url + "/wiki/contributors");
  const contributorList = desktop.getByRole("list", { name: "公开贡献者" });
  const contributorRow = contributorList.getByRole("link", { name: /Cherry/ });
  await contributorRow.locator("img").waitFor();
  assert.equal(await contributorRow.evaluate(row => row.getBoundingClientRect().width), await contributorList.evaluate(list => list.getBoundingClientRect().width));
  await contributorRow.click();
  await desktop.getByRole("heading", { name: "Cherry" }).waitFor();
  await desktop.getByRole("list", { name: "贡献记录" }).getByText(/注解/).waitFor();
  await desktop.getByRole("link", { name: "查看贡献者" }).click();
  await contributorRow.waitFor();
  await desktop.goto(url + "/wiki/123/page/article-1");
  await desktop.locator(".wiki-context #wiki-article-contributors").getByRole("heading", { name: "社区贡献" }).waitFor();
  assert.equal(await desktop.locator("article").getByRole("heading", { name: "社区贡献" }).count(), 0);
  const annotated = desktop.locator(".wiki-markdown[data-claim='0']");
  await annotated.locator("li").nth(0).getByText("第一处的注解。").waitFor();
  await annotated.locator("li").nth(2).getByText("第二处的注解。").waitFor();
  await annotated.getByText("保留的旧注解。").waitFor();
  assert.deepEqual(await annotated.locator("mark").allTextContents(), ["护", "护"]);
  assert.equal(await annotated.locator("mark").first().evaluate((mark) => getComputedStyle(mark).backgroundColor), "rgb(255, 241, 170)");
  const caption = annotated.locator(".wiki-inline-caption").first();
  const markBox = await annotated.locator("mark").first().boundingBox();
  const captionBox = await caption.boundingBox();
  assert.ok(captionBox.y >= markBox.y + markBox.height - 1 && Math.abs(captionBox.x - markBox.x) < 2);
  await caption.hover();
  await caption.getByRole("tooltip").getByText("注解作者").waitFor();
  const historyBadge = desktop.getByRole("button", { name: "修订历史", exact: true });
  assert.equal(await historyBadge.textContent(), "[2]");
  await historyBadge.hover();
  const historyPreview = desktop.locator(".history-preview");
  await historyPreview.getByText(/最新版/).waitFor();
  await historyPreview.getByRole("button").nth(1).click();
  await desktop.waitForURL(`**/wiki/123/page/article-1?revision=${"d".repeat(32)}`);
  await desktop.getByText("旧版词条正文", { exact: true }).waitFor();
  await desktop.reload();
  await desktop.getByText("旧版词条正文", { exact: true }).waitFor();
  await desktop.getByRole("button", { name: "返回最新版", exact: true }).click();
  await desktop.waitForURL("**/wiki/123/page/article-1");
  await annotated.locator("mark").first().waitFor();
  await historyBadge.click();
  const historyDialog = desktop.getByRole("dialog", { name: "修订历史" });
  await assertCentered(historyDialog);
  await historyDialog.getByRole("button", { name: "与上一版本比较" }).click();
  await historyDialog.locator("del").getByText("旧", { exact: true }).waitFor();
  await historyDialog.locator("ins").getByText("新", { exact: true }).waitFor();
  await historyDialog.locator("ins").getByText("第一处的注解。").waitFor();
  assert.equal(await historyDialog.locator("del").first().evaluate((node) => getComputedStyle(node).backgroundColor), "rgb(251, 218, 218)");
  assert.equal(await historyDialog.locator("ins").first().evaluate((node) => getComputedStyle(node).backgroundColor), "rgb(214, 243, 223)");
  await historyDialog.getByRole("button", { name: "关闭修订历史" }).click();
  await historyDialog.waitFor({ state: "hidden" });
  await desktop.goto(url + "/wiki/123/page/article-0");
  await desktop.getByText("暂无公开贡献。", { exact: true }).waitFor();
  await desktop.locator(".wiki-markdown[data-claim='0'] p").first().click({ button: "right" });
  await desktop.getByRole("menuitem", { name: "管理本词条", exact: true }).click();
  await desktop.getByText("没有待审投稿。", { exact: true }).waitFor();
  await assertCentered(desktop.getByRole("dialog", { name: "管理本词条" }));
  assert.ok((await desktop.getByRole("textbox", { name: "正文", exact: true }).inputValue()).includes("Sakura"));
  if (output) await desktop.screenshot({path:path.join(output,"wiki-article-management.png")});
  await desktop.getByRole("button", { name: "隐藏词条", exact: true }).click();
  await assertCentered(desktop.getByRole("dialog", { name: /^隐藏「/ }));
  await desktop.getByRole("dialog").getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(articleHidden, false);
  await desktop.getByRole("button", { name: "隐藏词条", exact: true }).click();
  await desktop.getByRole("button", { name: "确认隐藏", exact: true }).click();
  await desktop.getByText("词条已隐藏，访客无法访问。", { exact: true }).waitFor();
  await desktop.reload();
  await desktop.getByRole("button", { name: "管理本词条", exact: true }).click();
  await desktop.getByRole("button", { name: "恢复公开", exact: true }).click();
  await desktop.getByText("已取消隐藏。", { exact: true }).waitFor();
  await desktop.getByRole("heading", { name: cards[0].title, exact: true }).waitFor();
  await desktop.goto(url + `/wiki/admin?proposal=${reviewProposalId}`);
  await desktop.getByText("所选测试片段", { exact: true }).waitFor();
  await desktop.getByText("新的说明", { exact: true }).waitFor();
  await desktop.getByRole("button", { name: "批准", exact: true }).click();
  await desktop.getByText("已批准，已排入词条处理队列。", { exact: true }).waitFor();
  assert.match(await desktop.getByRole("link", { name: "查看词条任务" }).getAttribute("href"), /#job-b{32}$/);
  await desktop.goto(url + "/wiki/123/graph");
  await desktop.locator(".wiki-graph-list button").first().waitFor();
  await desktop.locator(".wiki-graph-canvas canvas").first().waitFor();
  if (output) await desktop.screenshot({path:path.join(output,"graph-desktop.png")});
  assert.deepEqual(errors,[]);
  if (output) fs.writeFileSync(path.join(output,"browser-results.json"),JSON.stringify({samples,bounded,feeds},null,2));
  console.log(JSON.stringify({samples,bounded,feeds}));
} finally {
  await browser?.close();
  server.close();
  before.close();
}
