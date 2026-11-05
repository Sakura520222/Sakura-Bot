<template>
  <div class="wiki-coverage">
    <strong>{{ labels[value.status] || "覆盖情况待核实" }}</strong> · 已枚举
    {{ value.enumerated || 0 }} 条 · 已收录 {{ value.indexed || 0 }} 条 · 排除
    {{ value.excluded || 0 }} 条<br /><span v-if="value.earliest"
      >范围：{{ formatDate(value.earliest) }} —
      {{ formatDate(value.latest) }}</span
    ><span v-else>尚无可展示的历史覆盖范围。</span
    ><span v-if="value.status !== 'accessible'"> · 历史可能存在缺口</span>
  </div>
</template>
<script setup lang="ts">
import { formatDate, type Coverage } from "../../api/wiki";
defineProps<{ value: Coverage }>();
const labels: Record<string, string> = {
  accessible: "已扫描当前可访问历史",
  partial: "部分覆盖",
  unavailable: "历史不可用",
};
</script>
