import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import vue from "@vitejs/plugin-vue";
import { chromium } from "playwright-core";

const web = fileURLToPath(new URL("../", import.meta.url));
const first = "https://t.me/first";
const second = "https://t.me/second";
let config = {
  assistant: {
    timezone: "UTC",
    checkin_enabled: true,
    require_subscription: true,
    required_channels: [first],
    comments: [{ channel: first, enabled: true, wake_word: "小妲" }],
    max_messages: 8,
    inbox_capacity: 8,
    per_user_concurrency: 4,
  },
  pool: { workers: 4, queue_capacity: 64, timeout_seconds: 180 },
  channels: [first, second],
};
let diagnosticsOK = false;
const writes = [];
const diagnosticDrafts = [];
const browserErrors = [];
const server = await createServer({
  configFile: false,
  root: web,
  resolve: { alias: { "@": fileURLToPath(new URL("../src", import.meta.url)) } },
  plugins: [
    vue(),
    {
      name: "assistant-browser-test",
      resolveId(id) {
        if (id === "/assistant-test.js") return id;
      },
      load(id) {
        if (id !== "/assistant-test.js") return;
        return `
          import { createApp, h } from "vue";
          import naive, { NMessageProvider } from "naive-ui";
          import QAAssistantPanel from "/src/components/QAAssistantPanel.vue";
          createApp({ render: () => h(NMessageProvider, null, { default: () => h(QAAssistantPanel) }) })
            .use(naive).mount("#app");
        `;
      },
      configureServer(app) {
        app.middlewares.use((req, res, next) => {
          if (req.url !== "/assistant-test") return next();
          res.setHeader("Content-Type", "text/html");
          res.end('<div id="app"></div><script type="module" src="/assistant-test.js"></script>');
        });
      },
    },
  ],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;
try {
  await server.listen();
  const port = server.httpServer.address().port;
  browser = await chromium.launch({
    ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}),
    args: ["--disable-gpu"],
  });
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 } });
  page.on("pageerror", (error) => browserErrors.push(error.message));
  await page.route("**/api/ai/assistant**", async (route) => {
    const request = route.request();
    if (request.url().endsWith("/diagnostics")) {
      diagnosticDrafts.push(request.postDataJSON());
      return route.fulfill({ json: { success: true, data: [
        { channel: first, ok: diagnosticsOK, message: diagnosticsOK ? "权限已确认" : "群隐私模式限制普通群消息读取" },
      ] } });
    }
    if (request.method() === "PUT") {
      const value = request.postDataJSON();
      writes.push(value);
      config = { ...value, channels: [first, second] };
      return route.fulfill({ json: { success: true, message: "已保存" } });
    }
    return route.fulfill({ json: { success: true, data: config } });
  });
  await page.goto(`http://127.0.0.1:${port}/assistant-test`);
  const anonymous = page.getByRole("switch", {
    name: `允许 ${first} 关联讨论群匿名管理员使用助手`, exact: true,
  });
  const quota = page.locator(".n-form-item", { hasText: "匿名管理员群组每日共享额度" }).getByRole("textbox");
  const save = page.getByRole("button", { name: "保存助手配置", exact: true });
  await anonymous.waitFor();
  assert.equal(await anonymous.getAttribute("aria-checked"), "false");
  assert.equal(await quota.count(), 0);
  assert.equal(await save.isDisabled(), true);
  assert.match(await page.locator("body").innerText(), /频道实际关联的讨论群直接使用/);
  assert.match(await page.locator("body").innerText(), /无法验证个人订阅。开启即授权本群身份/);

  await anonymous.click();
  await quota.waitFor();
  assert.equal(await quota.inputValue(), "20");
  await quota.fill("37");
  await quota.blur();
  await save.click();
  await page.getByText("请先修复助手权限问题", { exact: true }).waitFor();
  assert.equal(writes.length, 0);
  assert.equal(diagnosticDrafts[0].assistant.comments[0].anonymous_daily_limit, 37);
  assert.equal(diagnosticDrafts[0].assistant.comments[0].anonymous_enabled, true);

  diagnosticsOK = true;
  const saved = page.waitForResponse((response) => response.request().method() === "PUT");
  await save.click();
  await saved;
  assert.equal(writes.length, 1);
  assert.equal(writes[0].assistant.require_subscription, true);
  assert.deepEqual(writes[0].assistant.comments, [
    { channel: first, enabled: true, wake_word: "小妲", anonymous_enabled: true, anonymous_daily_limit: 37 },
    { channel: second, enabled: false, wake_word: "小妲", anonymous_enabled: false, anonymous_daily_limit: 20 },
  ]);

  await page.reload();
  await quota.waitFor();
  assert.equal(await anonymous.getAttribute("aria-checked"), "true");
  assert.equal(await quota.inputValue(), "37");
  await anonymous.click();
  assert.equal(await quota.count(), 0);
  await page.getByRole("switch", { name: `启用 ${second} 评论助手`, exact: true }).click();
  const otherAnonymous = page.getByRole("switch", {
    name: `允许 ${second} 关联讨论群匿名管理员使用助手`, exact: true,
  });
  assert.equal(await otherAnonymous.getAttribute("aria-checked"), "false");
  assert.deepEqual(browserErrors, []);
  console.log("Assistant browser checks passed for legacy defaults, opt-in, quota persistence, per-channel scope, and failed diagnostics blocking saves.");
} finally {
  await browser?.close();
  await server.close();
}
