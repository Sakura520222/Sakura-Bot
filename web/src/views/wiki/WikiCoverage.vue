<template>
  <div class="wiki-coverage">
    <strong>{{ labels[value.status] || "覆盖情况待核实" }}</strong>
    <template v-if="value.enumerated"> · 枚举 {{ value.enumerated || 0 }} · 收录 {{ value.indexed || 0 }} · 排除 {{ value.excluded || 0 }}</template>
    <template v-if="value.earliest"> · {{ formatDay(value.earliest) }} 至 {{ formatDay(value.latest) }}</template>
  </div>
</template>
<script setup lang="ts">
import { formatDay } from "./wiki-utils";
import type { Coverage } from "../../api/wiki";
defineProps<{ value: Coverage }>();
const labels: Record<string, string> = {
  accessible: "历史完整",
  partial: "部分覆盖",
  unavailable: "历史不可用",
};
</script>
