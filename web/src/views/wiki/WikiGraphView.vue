<template>
  <p class="wiki-breadcrumb">
    <RouterLink :to="`/wiki/${route.params.channel}`">返回频道</RouterLink> /
    知识地图
  </p>
  <div class="wiki-section-head">
    <div>
      <p class="wiki-kicker">Connected knowledge</p>
      <h1>沿着关系，发现知识</h1>
    </div>
    <button @click="share">{{ shared ? "已复制链接" : "分享视图" }}</button>
  </div>
  <div class="wiki-filters">
    <label
      >定位<input
        v-model="query"
        placeholder="搜索实体或主题"
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
    ><button @click="load()">查找</button>
  </div>
  <p v-if="error" class="wiki-error" role="alert">
    {{ error }}<button @click="load()">重试</button>
  </p>
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
    <div v-if="loading" class="wiki-graph-notice" role="status">
      加载相关知识…
    </div>
    <div v-else-if="!graph?.nodes.length" class="wiki-graph-notice">
      暂无符合筛选条件的知识节点。
    </div>
    <div v-if="hover" class="wiki-graph-hover">{{ hover }}</div>
    <aside class="wiki-graph-sidebar">
      <div v-if="selected" class="wiki-graph-detail">
        <span class="wiki-tag">{{ kindLabels[selected.kind] }}</span>
        <h3>{{ selected.title }}</h3>
        <p class="wiki-muted">{{ selected.topic }}</p>
        <RouterLink :to="selected.url">阅读知识条目 ↗</RouterLink>
        <div class="wiki-graph-actions">
          <button @click="load(selected.id, true)" :disabled="loading">
            展开邻居</button
          ><button @click="collapse">折叠邻居</button>
        </div>
      </div>
      <p class="wiki-muted">
        {{ graph?.nodes.length || 0 }} 个节点 ·
        {{ graph?.edges.length || 0 }} 条关系<span v-if="graph?.truncated">
          · 已达密度限制</span
        >
      </p>
      <div class="wiki-graph-list" aria-label="知识节点">
        <button
          v-for="node in graph?.nodes"
          :key="node.id"
          :aria-pressed="selected?.id === node.id"
          @click="select(node)"
        >
          {{ node.title }}<small>{{ kindLabels[node.kind] }}</small>
        </button>
      </div>
    </aside>
  </div>
  <div class="wiki-graph-legend">
    <span v-for="(color, key) in colors" :key="key"
      ><i :style="{ background: color }"></i>{{ kindLabels[key] }}</span
    >
  </div>
  <p class="wiki-muted">
    拖动节点或空白画布，滚轮/双指缩放。点击节点查看详情，再按需展开邻居。关系箭头表示方向。
  </p>
</template>
<script setup lang="ts">
import { ref, shallowRef, onMounted, onUnmounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import cytoscape, { type Core } from "cytoscape";
import {
  publicWiki,
  kindLabels,
  type WikiGraph,
  type WikiNode,
} from "../../api/wiki";
import { mergeGraph } from "./wiki-utils";
const route = useRoute(),
  router = useRouter();
const canvas = ref<HTMLElement>(),
  frame = ref<HTMLElement>();
let cy: Core | undefined;
let controller: AbortController | undefined;
let resize: ResizeObserver | undefined;
const graph = shallowRef<WikiGraph>(),
  selected = ref<WikiNode>(),
  hover = ref(""),
  loading = ref(false),
  error = ref(""),
  shared = ref(false);
const query = ref(String(route.query.q || "")),
  kind = ref(String(route.query.kind || "")),
  topic = ref(String(route.query.topic || "")),
  relation = ref(String(route.query.relation || "")),
  after = ref(String(route.query.after || "")),
  limit = ref(Math.min(120, Math.max(30, Number(route.query.limit) || 60))),
  layout = ref(String(route.query.layout || "cose"));
const colors: Record<string, string> = {
  Topic: "#59896a",
  Entity: "#6c8bb2",
  Event: "#d49956",
  Claim: "#aa7ca5",
  FAQ: "#67a5a0",
  Procedure: "#ae9f5f",
};
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
  controller?.abort();
  const current = new AbortController();
  controller = current;
  loading.value = true;
  error.value = "";
  if (!append) {
    selected.value = undefined;
    graph.value = undefined;
    cy?.elements().remove();
  }
  try {
    const result = await publicWiki<WikiGraph>(
      `/channels/${route.params.channel}/graph`,
      {
        node_id: node,
        q: query.value,
        kind: kind.value,
        topic: topic.value,
        relation: relation.value,
        after: after.value,
        limit: limit.value,
      },
      current.signal,
    );
    if (current.signal.aborted) return;
    graph.value = mergeGraph(
      append ? graph.value : undefined,
      result,
      limit.value,
    );
    render();
  } catch (err) {
    if (!current.signal.aborted)
      error.value = err instanceof Error ? err.message : "地图加载失败";
  } finally {
    if (!current.signal.aborted) loading.value = false;
  }
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
        color: colors[node.kind] || colors.Entity,
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
  if (target) cy?.animate({ center: { eles: target }, duration: 200 });
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
    error.value = "当前浏览器不支持全屏，可继续使用地图。";
  }
}
onMounted(() => {
  cy = cytoscape({
    container: canvas.value,
    style: [
      {
        selector: "node",
        style: {
          label: "data(label)",
          "background-color": "data(color)",
          color: "#718177",
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
          "line-color": "#9aaea0",
          "target-arrow-color": "#9aaea0",
          "target-arrow-shape": "triangle",
          "curve-style": "bezier",
          label: "data(label)",
          "font-size": 9,
          color: "#718177",
          "text-rotation": "autorotate",
        },
      },
      {
        selector: ":selected",
        style: { "border-width": 3, "border-color": "#d09b51" },
      },
    ],
    minZoom: 0.2,
    maxZoom: 3,
    wheelSensitivity: 0.25,
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
  load(route.query.node ? String(route.query.node) : undefined);
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
  () => load(),
);
onUnmounted(() => {
  controller?.abort();
  resize?.disconnect();
  cy?.destroy();
});
</script>
<style scoped>
.wiki-graph-frame {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 260px;
  border: 1px solid var(--wk-line);
  border-radius: 14px;
  background: var(--wk-card);
  overflow: hidden;
  min-height: 560px;
}
.wiki-graph-canvas {
  height: 600px;
  background-image: radial-gradient(var(--wk-line) 1px, transparent 1px);
  background-size: 22px 22px;
}
.wiki-graph-tools {
  position: absolute;
  top: 14px;
  left: 14px;
  z-index: 2;
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
  max-width: calc(100% - 290px);
}
.wiki-graph-tools button,
.wiki-graph-tools select {
  font-size: 12px;
  padding: 7px 10px;
}
.wiki-graph-sidebar {
  padding: 20px;
  border-left: 1px solid var(--wk-line);
  overflow: auto;
  max-height: 600px;
}
.wiki-graph-list {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.wiki-graph-list button {
  text-align: left;
  font-size: 12px;
  border: 0;
  background: var(--wk-soft);
}
.wiki-graph-list small {
  display: block;
  color: var(--wk-muted);
  margin-top: 3px;
}
.wiki-graph-list button[aria-pressed="true"] {
  outline: 2px solid var(--wk-accent);
}
.wiki-graph-actions {
  display: flex;
  gap: 5px;
  margin: 15px 0;
}
.wiki-graph-actions button {
  padding: 7px;
  font-size: 12px;
}
.wiki-graph-detail {
  padding-bottom: 10px;
  border-bottom: 1px solid var(--wk-line);
}
.wiki-graph-notice {
  position: absolute;
  top: 45%;
  left: 15%;
  padding: 15px;
  background: var(--wk-card);
  border-radius: 8px;
  pointer-events: none;
}
.wiki-graph-hover {
  position: absolute;
  bottom: 16px;
  left: 16px;
  padding: 8px 14px;
  border-radius: 6px;
  background: var(--wk-soft);
  font-size: 13px;
}
.wiki-graph-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  margin: 18px 0;
  font-size: 12px;
}
.wiki-graph-legend i {
  display: inline-block;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  margin-right: 6px;
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
    border-top: 1px solid var(--wk-line);
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
  .wiki-section-head h1 {
    font-size: 28px;
  }
}
</style>
