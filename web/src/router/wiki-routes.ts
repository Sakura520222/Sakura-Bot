import type { RouteLocationNormalized, RouteRecordRaw } from "vue-router";

// KeepAlive preserves loaded batches and measured segment heights for back navigation.
// Articles and graph instances keep their data in the shared resource cache.
export const WIKI_KEEP_ALIVE = [
  "WikiHomeView",
  "WikiChannelView",
  "WikiSearchView",
  "WikiTimelineView",
  "WikiProfileView",
];
const routeProps = (route: RouteLocationNormalized) => ({ viewRoute: route });
export const WIKI_FEED_ROUTES = new Set(["WikiHome", "WikiChannel", "WikiGlobalSearch", "WikiSearch", "WikiTimeline"]);

// Explicit public leaves; administrative routes never inherit public=true.
export const wikiRoutes: RouteRecordRaw = {
  path: "/wiki",
  name: "PublicWiki",
  component: () => import("../views/wiki/PublicWikiLayout.vue"),
  children: [
    {
      path: "",
      name: "WikiHome",
      props: routeProps,
      component: () => import("../views/wiki/WikiHomeView.vue"),
      meta: { public: true },
    },
    {
      path: "search",
      name: "WikiGlobalSearch",
      props: routeProps,
      component: () => import("../views/wiki/WikiSearchView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)",
      name: "WikiChannel",
      props: routeProps,
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
      props: routeProps,
      component: () => import("../views/wiki/WikiTimelineView.vue"),
      meta: { public: true },
    },
    {
      path: ":channel(\\d+)/search",
      name: "WikiSearch",
      props: routeProps,
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
