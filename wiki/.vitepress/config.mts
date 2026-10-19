import { defineConfig } from 'vitepress'

const repository = process.env.GITHUB_REPOSITORY || 'Sakura520222/Sakura-Bot'
const repositoryUrl = `https://github.com/${repository}`
const base = process.env.DOCS_BASE || '/'

export default defineConfig({
  lang: 'zh-CN',
  title: 'Sakura-Bot 文档',
  description: 'Sakura-Bot 安装配置、功能使用与开发维护文档。',
  base,
  lastUpdated: true,
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}favicon.svg` }]],

  markdown: {
    languageAlias: { env: 'dotenv' },
    config(md) {
      // Keep repository-relative links useful in both GitHub's wiki sources and the site.
      md.core.ruler.after('inline', 'repository-links', (state) => {
        for (const block of state.tokens) {
          for (const token of block.children || []) {
            if (token.type !== 'link_open') continue
            const href = token.attrGet('href')
            if (href === '../wiki' || href === '../wiki/') {
              token.attrSet('href', './Home.md')
            } else if (href?.startsWith('../')) {
              token.attrSet('href', new URL(href, `${repositoryUrl}/blob/main/wiki/`).href)
            }
          }
        }
      })
    }
  },

  themeConfig: {
    siteTitle: 'Sakura-Bot',
    logo: '/favicon.svg',
    nav: [
      { text: '首页', link: '/' },
      { text: '用户指南', link: '/User-Guide', activeMatch: '/User-' },
      { text: '开发文档', link: '/Developer-Guide', activeMatch: '/Developer-' },
      { text: '文档目录', link: '/Home' }
    ],
    sidebar: [
      {
        text: '开始阅读',
        items: [
          { text: '全部文档', link: '/Home' },
          { text: '用户文档概览', link: '/User-Guide' },
          { text: '开发者文档概览', link: '/Developer-Guide' }
        ]
      },
      {
        text: '用户指南',
        collapsed: false,
        items: [
          { text: '快速开始', link: '/User-Getting-Started' },
          { text: 'WebUI 管理面板', link: '/User-WebUI-Guide' },
          { text: 'Sakura Guard 智能群管', link: '/User-Sakura-Guard' },
          { text: '问答 Bot', link: '/User-QA-Bot-Guide' },
          { text: 'RAG 快速启动', link: '/User-RAG-Quickstart' },
          { text: '多轮对话', link: '/User-Multi-Turn-Conversation' },
          { text: '频道投票', link: '/User-Poll-Feature' }
        ]
      },
      {
        text: '开发与维护',
        collapsed: false,
        items: [
          { text: '开发快速开始', link: '/Developer-Getting-Started' },
          { text: '系统架构', link: '/Developer-Architecture' },
          { text: '配置持久化与恢复', link: '/Developer-Configuration-Persistence' },
          { text: 'Ruff 脚本指南', link: '/Developer-Ruff-Script-Guide' },
          { text: '文档站维护与部署', link: '/Developer-Documentation-Site' }
        ]
      },
      {
        text: 'AI 功能实现',
        collapsed: true,
        items: [
          { text: '问答 Bot 实施', link: '/Developer-QA-Bot-Implementation' },
          { text: 'RAG 技术文档', link: '/Developer-RAG-Feature-V3' },
          { text: '查询改写增强', link: '/Developer-Query-Rewrite-Enhancement' }
        ]
      },
      {
        text: '开发记录',
        collapsed: true,
        items: [
          { text: '模块拆分总结', link: '/Developer-Module-Split-Summary' },
          { text: '代码重构总结', link: '/Developer-Refactoring-Summary' },
          { text: '错误处理增强', link: '/Developer-Error-Handling-Improvements' },
          { text: '技术债务追踪', link: '/Developer-Technical-Debt' },
          { text: 'CI/CD 工作流', link: '/Developer-Workflow-Improvements' },
          { text: 'CI 测试修复', link: '/Developer-CI-Test-Fix' },
          { text: '工作流合并修复', link: '/Developer-Workflow-Merge-Fix' }
        ]
      },
      {
        text: '社区',
        items: [{ text: '行为准则', link: '/Code-of-Conduct' }]
      }
    ],
    socialLinks: [{ icon: 'github', link: repositoryUrl }],
    search: {
      provider: 'local',
      options: {
        miniSearch: {
          options: {
            tokenize(text) {
              return Array.from(new Intl.Segmenter('zh-CN', { granularity: 'word' }).segment(text))
                .filter((entry) => entry.isWordLike)
                .map((entry) => entry.segment)
            }
          }
        },
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '没有找到相关文档',
            resetButtonTitle: '清空搜索',
            backButtonTitle: '关闭搜索',
            displayDetails: '显示详细内容',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' }
          }
        }
      }
    },
    editLink: {
      pattern: `${repositoryUrl}/edit/main/wiki/:path`,
      text: '在 GitHub 上编辑此页'
    },
    lastUpdated: {
      text: '最后更新',
      formatOptions: { year: 'numeric', month: '2-digit', day: '2-digit' }
    },
    outline: { label: '本页目录', level: [2, 3] },
    docFooter: { prev: '上一篇', next: '下一篇' },
    sidebarMenuLabel: '文档导航',
    returnToTopLabel: '回到顶部',
    darkModeSwitchLabel: '主题',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    skipToContentLabel: '跳转到正文',
    notFound: {
      title: '页面未找到',
      quote: '请通过文档导航或搜索查找需要的内容。',
      linkLabel: '返回首页',
      linkText: '返回首页'
    },
    footer: { message: '基于 AGPL-3.0 许可证发布', copyright: 'Sakura-Bot 项目文档' }
  }
})
