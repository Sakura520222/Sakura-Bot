<template>
  <div class="wiki-view">
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    知识地图
  </p>
  <div class="wiki-section-head">
    <h1>知识地图</h1>
    <button @click="share">{{ shared ? "已复制链接" : "分享视图" }}</button>
  </div>
  <div class="wiki-filters">
    <label
      >定位<input
        v-model="query"
        placeholder="定位节点"
        @keydown.enter="load()" /></label
    ><label
      >类型<select v-model="kind" @change="load()">
        <option value="">全部</option>
        <option v-for="(label, key) in kindLabels" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label
    ><label
      >主题<input v-model="topic" placeholder="全部" @change="load()" /></label
    ><label
      >关系<select v-model="relation" @change="load()">
        <option value="">全部</option>
        <option v-for="(label, key) in relations" :key="key" :value="key">
          {{ label }}
        </option>
      </select></label
    ><label
      >发生起始<input v-model="after" type="date" @change="load()" /></label
    ><label
      >节点上限<select v-model.number="limit" @change="load()">
        <option :value="30">30</option>
        <option :value="60">60</option>
        <option :value="120">120</option>
      </select></label
    ><button class="wiki-primary" @click="load()">查找</button>
  </div>
  <p v-if="localError || error" class="wiki-error" role="alert">
    {{ localError || error }}<button @click="retry()">重试</button>
  </p>
  <WikiState v-if="stale && data" variant="stale" @retry="retry()" />
  <div ref="frame" class="wiki-graph-frame">
    <div class="wiki-graph-tools">
      <button aria-label="放大地图" @click="zoom(1.3)">＋</button
      ><button aria-label="缩小地图" @click="zoom(1 / 1.3)">－</button
      ><button @click="cy?.fit(undefined, 40)">适应画面</button
      ><select v-model="layout" aria-label="地图布局" @change="relayout">
        <option value="cose">关系布局</option>
        <option value="circle">环形布局</option>
        <option value="grid">网格布局</option></select
      ><button @click="fullscreen">全屏</button>
    </div>
    <div
      ref="canvas"
      class="wiki-graph-canvas"
      role="img"
      aria-label="可拖拽、缩放的知识地图；也可使用右侧节点列表操作"
    ></div>
    <div v-if="loading" class="wiki-graph-notice" role="status">加载中…</div>
    <div v-else-if="!graph?.nodes.length" class="wiki-graph-notice">
      无匹配节点，调整筛选后重试
    </div>
    <div v-if="hover" class="wiki-graph-hover">{{ hover }}</div>
    <aside class="wiki-graph-sidebar">
      <div v-if="selected" class="wiki-graph-detail">
        <span v-if="selected.kind !== 'Topic'" class="wiki-tag wiki-chip-accent">{{
          kindLabels[selected.kind]
        }}</span>
        <h3>{{ selected.title }}</h3>
        <p class="wiki-muted">{{ selected.topic }}</p>
        <RouterLink :to="selected.url">阅读条目</RouterLink>
        <div class="wiki-graph-actions">
          <button @click="load(selected.id, true)" :disabled="loading || expanding">
            展开邻居</button
          ><button @click="collapse">折叠邻居</button>
        </div>
      </div>
      <input
        v-model="nodeFilter"
        class="wiki-graph-filter"
        type="search"
        placeholder="筛选节点"
        aria-label="按标题筛选节点列表"
      />
      <div class="wiki-graph-list" aria-label="知识节点">
        <button
          v-for="node in visibleNodes"
          :key="node.id"
          :aria-pressed="selected?.id === node.id"
          @click="select(node)"
        >
          {{ node.title
          }}<small v-if="node.kind !== 'Topic'">{{
            kindLabels[node.kind]
          }}</small>
        </button>
      </div>
    </aside>
  </div>
  <div class="wiki-graph-foot">
    <div class="wiki-graph-legend">
      <span v-for="(color, key) in palette.kinds" :key="key"
        ><i :style="{ background: color }"></i>{{ kindLabels[key] }}</span
      >
    </div>
    <span class="wiki-muted">
      {{ graph?.nodes.length || 0 }} 个节点 ·
      {{ graph?.edges.length || 0 }} 条关系<span v-if="graph?.truncated">
        · 已按上限截断 —— 提高节点上限或添加筛选</span
      >
    </span>
    <span class="wiki-muted">滚轮缩放 · 拖拽平移 · 点击节点展开邻居 · 箭头为关系方向</span>
  </div>
  </div>
</template>
<script setup lang="ts">
import { ref, shallowRef, computed, onMounted, onUnmounted, watch } from "vue";

const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
import { useRoute, useRouter } from "vue-router";
import cytoscape, { type Core } from "cytoscape";
import {
  publicWikiUrl,
  kindLabels,
  type WikiGraph,
  type WikiNode,
} from "../../api/wiki";
import { useWikiResource, wikiUrl } from "../../composables/wiki-store";
import { mergeGraph } from "./wiki-utils";

defineOptions({ name: "WikiGraphView" });

const route = useRoute(),
  router = useRouter();
const canvas = ref<HTMLElement>(),
  frame = ref<HTMLElement>();
let cy: Core | undefined;
let resize: ResizeObserver | undefined;
// 本地图谱是视图私有的展开态：SWR 缓存只存单次请求载荷，展开合并不回写缓存
const graph = shallowRef<WikiGraph>(),
  selected = ref<WikiNode>(),
  hover = ref(""),
  shared = ref(false);
const query = ref(String(route.query.q || "")),
  kind = ref(String(route.query.kind || "")),
  topic = ref(String(route.query.topic || "")),
  relation = ref(String(route.query.relation || "")),
  after = ref(String(route.query.after || "")),
  limit = ref(Math.min(120, Math.max(30, Number(route.query.limit) || 60))),
  layout = ref(String(route.query.layout || "cose"));
// 根节点只取首挂载时的 ?node：分享写入 URL 的 node 不应反过来触发重新拉取
const rootNodeId = ref(String(route.query.node || ""));
const expanding = ref(false),
  localError = ref("");

function graphUrl(nodeId?: string): string {
  return wikiUrl(`/channels/${route.params.channel}/graph`, {
    node_id: nodeId || undefined,
    q: query.value,
    kind: kind.value,
    topic: topic.value,
    relation: relation.value,
    after: after.value,
    limit: limit.value,
  });
}
const { data, loading, error, stale, reload } = useWikiResource<WikiGraph>(
  () => graphUrl(rootNodeId.value || undefined),
  publicWikiUrl<WikiGraph>,
  { ttlMs: 300_000 },
);
watch(data, (payload) => {
  if (!payload) return;
  graph.value = payload;
  render();
});

/** 侧栏节点列表的客户端筛选：只过滤展示，不参与图谱请求 */
const nodeFilter = ref("");
const visibleNodes = computed(() => {
  const needle = nodeFilter.value.trim().toLocaleLowerCase();
  const nodes = graph.value?.nodes || [];
  if (!needle) return nodes;
  return nodes.filter((node) =>
    node.title.toLocaleLowerCase().includes(needle),
  );
});

/* 画布是 canvas 渲染，读不到 CSS 变量，因此节点/边/标签配色按主题各备一套常量，
   取值与 --sb-* token 保持同源（亮色 = Google 控制台，暗色 = X）。 */
interface GraphPalette {
  kinds: Record<string, string>;
  label: string;
  edge: string;
  selected: string;
}
const palettes: Record<"light" | "dark", GraphPalette> = {
  light: {
    kinds: {
      Topic: "#1a73e8",
      Entity: "#188038",
      Event: "#e37400",
      Claim: "#9334e6",
      FAQ: "#12b5cb",
      Procedure: "#5f6368",
    },
    label: "#5f6368", // = 浅色 --sb-text-2
    edge: "#bdc1c6", // = 浅色 --sb-border-strong
    selected: "#1a73e8", // = 浅色 --sb-accent
  },
  dark: {
    kinds: {
      Topic: "#8ab4f8",
      Entity: "#81c995",
      Event: "#fa903e",
      Claim: "#c58af9",
      FAQ: "#78d9ed",
      Procedure: "#9ca0a6",
    },
    label: "#9ca0a6", // = 深色 --sb-text-2
    edge: "#5f6368", // #000 画布上箭头方向必须可读，不能更暗
    selected: "#1d9bf0", // = 深色 --sb-accent
  },
};
const palette = ref<GraphPalette>(palettes.light);
function syncPalette() {
  // App.vue 在根组件 onMounted 才写入 html.dark（晚于本子组件挂载），
  // 首屏判定需兼读持久化设置，避免暗色用户先渲染成亮色画布
  const dark =
    document.documentElement.classList.contains("dark") ||
    localStorage.getItem("sakura_bot_theme") === "dark";
  palette.value = dark ? palettes.dark : palettes.light;
}
function nodeColor(kind: string): string {
  return palette.value.kinds[kind] || palette.value.kinds.Entity;
}
function cyStyle(): cytoscape.StylesheetJson {
  const value = palette.value;
  return [
    {
      selector: "node",
      style: {
        label: "data(label)",
        "background-color": "data(color)",
        color: value.label,
        "font-size": 11,
        "text-wrap": "wrap",
        "text-max-width": "100px",
        "text-valign": "bottom",
        "text-margin-y": 8,
        width: 28,
        height: 28,
      },
    },
    {
      selector: "edge",
      style: {
        width: 1.5,
        "line-color": value.edge,
        "target-arrow-color": value.edge,
        "target-arrow-shape": "triangle",
        "curve-style": "bezier",
        label: "data(label)",
        "font-size": 9,
        color: value.label,
        "text-rotation": "autorotate",
      },
    },
    {
      selector: ":selected",
      style: { "border-width": 3, "border-color": value.selected },
    },
  ];
}
function handleThemeChange() {
  syncPalette();
  if (!cy) return;
  cy.style(cyStyle());
  cy.nodes().forEach((node) => {
    node.data("color", nodeColor(String(node.data("kind"))));
  });
}
const relations: Record<string, string> = {
  belongs_to: "属于",
  depends_on: "依赖",
  discusses: "讨论",
  solves: "解决",
  replaces: "替代",
  supports: "支持",
  contradicts: "反驳",
  evolves: "演化",
};
async function load(node?: string, append = false) {
  if (append && node) {
    // 展开邻居是用户触发的增量动作：直接请求后本地合并，不占用缓存键
    localError.value = "";
    expanding.value = true;
    try {
      const payload = await publicWikiUrl<WikiGraph>(graphUrl(node));
      graph.value = mergeGraph(graph.value, payload, limit.value);
      render();
    } catch (err) {
      localError.value = err instanceof Error ? err.message : "地图加载失败";
    } finally {
      expanding.value = false;
    }
    return;
  }
  if (typeof node === "string") rootNodeId.value = node;
  localError.value = "";
  selected.value = undefined;
}
async function retry() {
  localError.value = "";
  await reload();
}
function render() {
  if (!cy || !graph.value) return;
  const saved = new Map(cy.nodes().map((node) => [node.id(), node.position()]));
  cy.elements().remove();
  cy.add([
    ...graph.value.nodes.map((node) => ({
      data: {
        ...node,
        label: node.title,
        color: nodeColor(node.kind),
      },
      position: saved.get(node.id),
    })),
    ...graph.value.edges.map((edge) => ({
      data: { ...edge, label: relations[edge.kind] || edge.kind },
    })),
  ]);
  relayout();
}
function relayout() {
  cy?.layout({
    name: ["circle", "grid", "cose"].includes(layout.value)
      ? layout.value
      : "cose",
    animate: false,
    padding: 45,
  } as cytoscape.LayoutOptions).run();
}
function select(node: WikiNode) {
  selected.value = node;
  cy?.elements().unselect();
  const target = cy?.getElementById(node.id);
  target?.select();
  if (target)
    cy?.animate({
      center: { eles: target },
      duration: reduceMotion.matches ? 0 : 200,
    });
}
function collapse() {
  if (!selected.value || !graph.value) return;
  const remove = new Set(
    cy
      ?.getElementById(selected.value.id)
      .neighborhood()
      .nodes()
      .map((node) => node.id()),
  );
  graph.value = {
    ...graph.value,
    nodes: graph.value.nodes.filter((n) => !remove.has(n.id)),
    edges: graph.value.edges.filter(
      (e) => !remove.has(e.source) && !remove.has(e.target),
    ),
  };
  render();
}
function zoom(factor: number) {
  if (cy)
    cy.zoom({
      level: cy.zoom() * factor,
      renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
    });
}
async function share() {
  await router.replace({
    query: {
      node: selected.value?.id,
      q: query.value,
      kind: kind.value,
      topic: topic.value,
      relation: relation.value,
      after: after.value,
      limit: limit.value,
      layout: layout.value,
    },
  });
  try {
    await navigator.clipboard.writeText(location.href);
    shared.value = true;
  } catch {
    shared.value = false;
  }
}
async function fullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await frame.value?.requestFullscreen();
    cy?.resize();
  } catch {
    localError.value = "当前浏览器不支持全屏，可继续使用地图。";
  }
}
onMounted(() => {
  syncPalette();
  // wheelSensitivity 保持 cytoscape 默认值 1（缩放速率与之成正比）；
  // 此前显式设为 0.25，滚轮缩放比原生慢 4 倍。
  cy = cytoscape({
    container: canvas.value,
    style: cyStyle(),
    minZoom: 0.2,
    maxZoom: 3,
  });
  cy.on("tap", "node", (event) => {
    const node = graph.value?.nodes.find((n) => n.id === event.target.id());
    if (node) select(node);
  });
  cy.on("mouseover", "node,edge", (event) => {
    hover.value = event.target.data("label");
  });
  cy.on("mouseout", "node,edge", () => {
    hover.value = "";
  });
  window.addEventListener("theme-change", handleThemeChange);
  // 命中缓存时载荷在挂载前就已就绪（data watch 的 render 早于画布创建）：
  // 画布建好后补渲染一次；冷路径由 data watch 接手
  render();
  load();
});
onMounted(() => {
  resize = new ResizeObserver(() => {
    cy?.resize();
    cy?.fit(undefined, 35);
  });
  if (canvas.value) resize.observe(canvas.value);
});
watch(
  () => route.params.channel,
  () => {
    rootNodeId.value = "";
    load();
  },
);
onUnmounted(() => {
  window.removeEventListener("theme-change", handleThemeChange);
  resize?.disconnect();
  cy?.destroy();
});
</script>
<style scoped>
.wiki-graph-frame {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 240px;
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius);
  background: var(--sb-surface);
  overflow: hidden;
  min-height: 560px;
}
.wiki-graph-canvas {
  height: 600px;
  background: var(--sb-bg);
  background-image: radial-gradient(var(--sb-border) 1px, transparent 1px);
  background-size: 22px 22px;
}
.wiki-graph-tools {
  position: absolute;
  top: 12px;
  left: 12px;
  z-index: 2;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  max-width: calc(100% - 270px);
}
.wiki-graph-tools button,
.wiki-graph-tools select {
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  box-shadow: var(--sb-shadow-1);
}
.wiki-graph-tools button:hover,
.wiki-graph-tools select:hover {
  background: var(--sb-surface-2);
}
.wiki-graph-sidebar {
  padding: 14px;
  border-left: 1px solid var(--sb-border);
  overflow: auto;
  max-height: 600px;
}
.wiki-graph-detail {
  padding-bottom: 12px;
  margin-bottom: 10px;
  border-bottom: 1px solid var(--sb-border);
}
.wiki-graph-detail h3 {
  font-size: 15px;
  font-weight: 600;
  margin: 8px 0 4px;
}
.wiki-graph-actions {
  display: flex;
  gap: 6px;
  margin: 12px 0;
}
.wiki-graph-actions button {
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
}
.wiki-graph-filter {
  width: 100%;
  margin-bottom: 8px;
  padding: 4px 8px;
  font-size: 12px;
}
.wiki-graph-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.wiki-graph-list button {
  height: auto;
  text-align: left;
  font-size: 12px;
  font-weight: 500;
  color: var(--sb-text-2);
  border: 0;
  border-radius: var(--sb-radius-s);
  background: transparent;
  padding: 6px 10px;
}
.wiki-graph-list button:hover {
  background: var(--sb-surface-2);
}
.wiki-graph-list small {
  display: block;
  color: var(--sb-text-3);
  font-weight: 400;
  margin-top: 2px;
}
.wiki-graph-list button[aria-pressed="true"] {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}
.wiki-graph-notice {
  position: absolute;
  top: 45%;
  left: 15%;
  padding: 12px 16px;
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  border-radius: var(--sb-radius-s);
  box-shadow: var(--sb-shadow-1);
  font-size: 13px;
  color: var(--sb-text-2);
  pointer-events: none;
}
.wiki-graph-hover {
  position: absolute;
  bottom: 16px;
  left: 16px;
  padding: 6px 12px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface);
  border: 1px solid var(--sb-border);
  box-shadow: var(--sb-shadow-1);
  font-size: 12px;
  color: var(--sb-text-2);
}
.wiki-graph-foot {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 18px;
  margin: 12px 0;
  font-size: 12px;
}
.wiki-graph-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
}
.wiki-graph-legend i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 5px;
}
.wiki-graph-frame:fullscreen {
  border-radius: 0;
}
.wiki-graph-frame:fullscreen .wiki-graph-canvas {
  height: 100vh;
}
@media (max-width: 760px) {
  .wiki-graph-frame {
    grid-template-columns: 1fr;
  }
  .wiki-graph-canvas {
    height: 450px;
  }
  .wiki-graph-sidebar {
    border-left: 0;
    border-top: 1px solid var(--sb-border);
    max-height: 260px;
  }
  .wiki-graph-list {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }
  .wiki-graph-tools {
    max-width: 95%;
  }
  .wiki-graph-notice {
    top: 30%;
    left: 10%;
  }
}
@media (max-width: 600px) {
  .wiki-section-head {
    display: block;
  }
  .wiki-section-head > button {
    margin-top: 10px;
  }
}
</style>
