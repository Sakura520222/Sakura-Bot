# 文档站维护与部署

文档站使用 VitePress，直接读取 `wiki/` 下的 Markdown 文件。修改现有文档即可更新页面，不需要复制到另一份文档目录。

## 本地预览

安装 Node.js 22 或更新版本（CI 使用 Node.js 24），在仓库根目录执行：

```bash
npm --prefix wiki ci
npm --prefix wiki run docs:dev
```

打开终端输出的本地地址。站点默认使用域名根路径 `/`，与正式自定义域名的部署路径一致。

## 构建与检查

```bash
npm --prefix wiki run docs:build
npm --prefix wiki run docs:preview
```

构建产物位于 `wiki/.vitepress/dist/`。构建会检查内部页面链接，链接指向不存在的页面时会失败。依赖目录、缓存和构建产物已加入 `.gitignore`；更新依赖时需要一并提交 `wiki/package-lock.json`。

当前锁定 VitePress 1.6.4，并通过 npm `overrides` 将其 Vite 依赖固定到 6.4.4，以包含开发服务器的安全修复。升级 VitePress 时应同时检查是否仍需此覆盖。

## 自动部署

部署工作流位于 [`.github/workflows/docs.yml`](../.github/workflows/docs.yml)：

- 每次推送到 `main` 都会安装锁定依赖、构建文档并部署至 GitHub Pages。
- 向 `main` 或 `dev` 提交涉及文档的 PR 时，只检查构建；合并后随 `main` 推送发布。
- 可以在 Actions 的“文档站构建与部署”工作流中手动运行；仅选择 `main` 时才会发布。
- 构建失败不会进入部署步骤；同一分支的发布按顺序执行。

首次发布前，需要在仓库 **Settings → Pages → Build and deployment → Source** 中选择 **GitHub Actions**。发布工作流使用仓库内置的 `GITHUB_TOKEN` 和 OIDC，无需添加部署密钥或个人令牌。

本仓库的正式地址为 [Sakura-Bot 文档站](https://sakura-bot.pyabunny.com/)。首次构建部署成功后即可访问文档；部署状态和实际地址也会显示在工作流的 `github-pages` 环境中。

## 增加或调整文档

1. 在 `wiki/` 中新增或修改 Markdown 文件。
2. 在 `wiki/.vitepress/config.mts` 的 `sidebar` 中添加入口，并视内容更新 `Home.md`、`User-Guide.md` 或 `Developer-Guide.md`。
3. 文档间使用相对 Markdown 链接，例如 `[快速开始](User-Getting-Started.md)`。
4. 指向仓库源码的 `../` 链接在站点中会自动转换为 GitHub 文件链接，源文档仍可直接在仓库中阅读。
5. 本地构建成功后，提交并推送变更。

`index.md` 是站点首页，原有 `Home.md` 保留为完整文档目录。站点提供中文全文搜索、页内目录、深浅色主题、更新时间和 GitHub 编辑入口。

## 自定义域名、HTTPS 与部署路径

正式域名为 `sakura-bot.pyabunny.com`，使用以下配置：

- GitHub 仓库 **Settings → Pages → Custom domain**：`sakura-bot.pyabunny.com`。
- Cloudflare DNS：`sakura-bot.pyabunny.com` 的 `CNAME` 指向 `sakura520222.github.io`，使用 **DNS only**，关闭代理。
- GitHub Pages：证书就绪后开启 **Enforce HTTPS**，将 HTTP 请求重定向到 HTTPS。
- 仓库 **Settings → Secrets and variables → Actions → Variables**：`DOCS_BASE` 为 `/`。本地默认值同样为 `/`。

本项目使用 GitHub Actions 发布，域名绑定保存在仓库 Pages 设置中，不依赖 `CNAME` 文件。GitHub Pages 自动签发和续期证书；更换域名后需要等待新证书签发，再确认 HTTPS 强制跳转状态。

如果将来移除自定义域名、恢复 `github.io/Sakura-Bot/` 地址，需要把 `DOCS_BASE` 改为 `/Sakura-Bot/`，然后重新构建部署。本地可用 PowerShell 指定相同路径：

```powershell
$env:DOCS_BASE = '/Sakura-Bot/'
npm --prefix wiki run docs:build
npm --prefix wiki run docs:preview
```

更换路径后需要重新构建。自定义域名和 DNS 请在 GitHub Pages 设置中配置。

## 参考

- [VitePress 部署说明](https://vitepress.dev/zh/guide/deploy)
- [GitHub Pages 发布源配置](https://docs.github.com/zh/pages/getting-started-with-github-pages/configuring-a-publishing-source-for-your-github-pages-site)
