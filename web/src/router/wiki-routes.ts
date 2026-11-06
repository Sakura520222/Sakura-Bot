import type { RouteRecordRaw } from "vue-router";

// KeepAlive 白名单（单一事实来源）：键 = 路由名 + 频道 ID，绝不用 fullPath——
// 查询变化（翻页/筛选）不能重挂载，文章的 canonical router.replace 也不能打断阅读。
// 排除 WikiGraphView（cytoscape 实例缓存会累积且复活代价高，改为只缓存数据载荷）
// 与 WikiArticleView（canonical 替换 + 每 slug 键会产生重复 LRU 条目与过期标题）。
export const WIKI_KEEP_ALIVE = [
  "WikiHomeView",
  "WikiChannelView",
  "WikiSearchView",
  "WikiTimelineView",
  "WikiProfileView",
];

// Explicit public leaves; administrative routes never inherit public=true.
export const wikiRoutes: RouteRecordRaw = {
  path: "/wiki",
  component: () => import("../views/wiki/PublicWikiLayout.vue"),
  children: [
    {
      path: "",
      name: "WikiHome",
      component: () => import("../views/wiki/WikiHomeView.vue"),
      meta: { public: true },
    },
    {
      path: "search",
      name: "WikiGlobalSearch",
      component: () => import("../views/wiki/WikiSearchView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)",
      name: "WikiChannel",
      component: () => import("../views/wiki/WikiChannelView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/page/:slug",
      name: "WikiArticle",
      component: () => import("../views/wiki/WikiArticleView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/graph",
      name: "WikiGraph",
      component: () => import("../views/wiki/WikiGraphView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/timeline",
      name: "WikiTimeline",
      component: () => import("../views/wiki/WikiTimelineView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/search",
      name: "WikiSearch",
      component: () => import("../views/wiki/WikiSearchView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/profile",
      name: "WikiProfile",
      component: () => import("../views/wiki/WikiProfileView.vue"),
      meta: { public: true },
    },
  ],
};
