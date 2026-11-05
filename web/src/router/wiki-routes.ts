import type { RouteRecordRaw } from "vue-router";

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
