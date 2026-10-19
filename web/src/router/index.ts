import { createRouter, createWebHistory } from "vue-router";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/login",
      name: "Login",
      component: () => import("../views/LoginView.vue"),
      meta: { public: true },
    },
    {
      path: "/",
      component: () => import("../components/AppLayout.vue"),
      children: [
        {
          path: "bot-chats",
          name: "BotChats",
          component: () => import("../views/BotChatsView.vue"),
        },
        {
          path: "guard",
          name: "Guard",
          component: () => import("../views/GuardView.vue"),
        },
        {
          path: "",
          name: "Dashboard",
          component: () => import("../views/DashboardView.vue"),
        },
        {
          path: "channels",
          name: "Channels",
          component: () => import("../views/ChannelsView.vue"),
        },
        {
          path: "ai-config",
          name: "AIConfig",
          component: () => import("../views/AIConfigView.vue"),
        },
        {
          path: "schedules",
          name: "Schedules",
          component: () => import("../views/SchedulesView.vue"),
        },
        {
          path: "forwarding",
          name: "Forwarding",
          component: () => import("../views/ForwardingView.vue"),
        },
        {
          path: "interaction",
          name: "Interaction",
          component: () => import("../views/InteractionView.vue"),
        },
        {
          path: "commands",
          name: "Commands",
          component: () => import("../views/CommandsView.vue"),
        },
        {
          path: "stats",
          name: "Stats",
          component: () => import("../views/StatsView.vue"),
        },
        {
          path: "system",
          name: "System",
          component: () => import("../views/SystemView.vue"),
        },
        {
          path: "userbot",
          name: "UserBot",
          component: () => import("../views/UserBotView.vue"),
        },
        {
          path: "vector-store",
          name: "VectorStore",
          component: () => import("../views/VectorStoreView.vue"),
        },
        {
          path: "database",
          name: "Database",
          component: () => import("../views/DatabaseView.vue"),
        },
      ],
    },
  ],
});

// 路由守卫 - 检查认证
router.beforeEach((to, _from, next) => {
  const token = localStorage.getItem("sakura_bot_token");
  if (to.meta.public) {
    next();
    return;
  }
  // 没有 token 直接跳转登录
  if (!token) {
    next("/login");
    return;
  }
  // token 存在则放行，401 由 API 拦截器处理
  next();
});

export default router;
