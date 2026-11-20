<template>
  <div class="channels-page">
    <header class="page-head">
      <div>
        <h2 class="page-title">频道管理</h2>
        <p class="page-subtitle">管理机器人监控的频道，可随时生成总结或移除不再需要的频道。</p>
      </div>
      <n-button type="primary" round @click="openAddModal">
        <template #icon><AddOutlined /></template>
        添加频道
      </n-button>
    </header>

    <n-card class="panel-card" :bordered="true">
      <div class="list-heading">
        <h3>监控频道</h3>
        <div class="list-tools">
          <span class="list-count" aria-live="polite">{{ statText }}</span>
          <n-button quaternary circle :loading="loading" title="刷新" aria-label="刷新" @click="loadChannels">
            <template #icon><RefreshOutlined /></template>
          </n-button>
        </div>
      </div>

      <div v-if="loaded && !loadError" class="list-filters">
        <n-input
          v-model:value="search"
          :input-props="{ 'aria-label': '搜索频道名称或 URL' }"
          placeholder="搜索频道名称或 URL"
          clearable
        >
          <template #prefix><SearchOutlined /></template>
        </n-input>
        <n-select v-model:value="configFilter" :options="configFilterOptions" aria-label="筛选频道配置" />
      </div>

      <!-- 异步三态：首屏骨架屏 -->
      <div v-if="!loaded && loading" class="skel">
        <n-skeleton v-for="i in 5" :key="i" height="44px" :sharp="false" />
      </div>
      <!-- 异步三态：错误可重试 -->
      <n-result
        v-else-if="loadError"
        status="error"
        title="加载失败"
        description="频道列表获取失败，请检查后端连接后重试。"
      >
        <template #footer>
          <n-button type="primary" @click="loadChannels">重试</n-button>
        </template>
      </n-result>
      <!-- 异步三态：空态区分"真的没有"与"筛选后没有"，都能走到下一步 -->
      <n-empty
        v-else-if="filteredChannels.length === 0"
        class="table-empty"
        :description="channels.length === 0 ? '还没有添加频道' : '没有匹配的频道'"
      >
        <template #extra>
          <n-button v-if="channels.length === 0" type="primary" @click="openAddModal">添加第一个频道</n-button>
          <span v-else class="empty-hint">换个关键词或调整筛选条件试试</span>
        </template>
      </n-empty>
      <ul v-else class="channel-list" role="list" aria-label="监控频道" :aria-busy="loading">
        <li v-for="channel in filteredChannels" :key="channel.url" class="channel-row">
          <div class="channel-identity">
            <span class="channel-icon" aria-hidden="true"><ChannelOutlined /></span>
            <div class="channel-copy">
              <strong class="channel-name">{{ getChannelName(channel.url) }}</strong>
              <span v-if="getChannelName(channel.url) !== channel.url" class="channel-raw">{{ channel.url }}</span>
            </div>
          </div>
          <dl class="channel-settings">
            <div>
              <dt>定时任务</dt>
              <dd>
                <n-tag :type="channel.has_schedule ? 'success' : 'default'" :bordered="false" round size="small">
                  {{ channel.has_schedule ? "已配置" : "未配置" }}
                </n-tag>
              </dd>
            </div>
            <div>
              <dt>投票设置</dt>
              <dd>
                <n-tag :type="channel.has_poll_settings ? 'success' : 'default'" :bordered="false" round size="small">
                  {{ channel.has_poll_settings ? "已配置" : "未配置" }}
                </n-tag>
              </dd>
            </div>
          </dl>
          <div class="row-actions">
            <n-button
              size="small"
              quaternary
              type="primary"
              :aria-label="`生成总结 ${getChannelName(channel.url)}`"
              :loading="generating.has(channel.url)"
              :disabled="generating.has(channel.url)"
              @click="handleGenerate(channel.url)"
            >
              生成总结
            </n-button>
            <n-popconfirm @positive-click="removeChannel(channel.url)">
              <template #trigger>
                <n-button
                  size="small"
                  quaternary
                  type="error"
                  :aria-label="`删除 ${getChannelName(channel.url)}`"
                  :loading="deletingUrl === channel.url"
                >
                  删除
                </n-button>
              </template>
              删除后 {{ getChannelName(channel.url) }} 将退出监控，其定时任务与投票设置会被一并清除，且不可恢复。确定删除？
            </n-popconfirm>
          </div>
        </li>
      </ul>
    </n-card>

    <!-- 添加频道弹窗：表单校验、仅成功才关闭，失败提示留在弹窗内 -->
    <n-modal
      v-model:show="showAddModal"
      preset="card"
      class="modal-responsive"
      title="添加频道"
      :mask-closable="false"
    >
      <n-form
        ref="formRef"
        class="modal-form"
        :model="addForm"
        :rules="addRules"
        label-placement="top"
        @keyup.enter="handleAdd"
      >
        <n-form-item label="频道 URL" path="url">
          <div class="channel-input">
            <n-input
              v-model:value="addForm.url"
              :placeholder="channelUrlPlaceholder"
              :input-props="{ 'aria-describedby': 'channel-url-hint' }"
              :disabled="saving"
            />
            <p id="channel-url-hint">支持 https://t.me/ 链接、@用户名或频道 ID，添加后立即纳入监控。</p>
          </div>
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary :disabled="saving" @click="showAddModal = false">取消</n-button>
          <n-button type="primary" round :loading="saving" @click="handleAdd">添加</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useMessage } from "naive-ui";
import type { FormInst, FormRules } from "naive-ui";
import { addChannel, deleteChannel, generateSummary, getChannels, type ChannelInfo } from "@/api/modules";
import { AddOutlined, ChannelOutlined, RefreshOutlined, SearchOutlined } from "@/components/icons";
import { getChannelName } from "@/utils/formatters";

const message = useMessage();

// URL 不直接写在模板静态属性里：属性值中的 "//" 会截断 vue-tsc 3 虚拟代码的作用域，导致后续类型检查失效
const channelUrlPlaceholder = "https://t.me/channel_name";

// ── 列表数据与三态 ─────────────────────────────────────
const loading = ref(false);
const loaded = ref(false);
const loadError = ref(false);
const channels = ref<ChannelInfo[]>([]);

// 工具栏：搜索 + 配置状态筛选
const search = ref("");
const configFilter = ref("all");
const configFilterOptions = [
  { label: "全部频道", value: "all" },
  { label: "有定时任务", value: "scheduled" },
  { label: "无定时任务", value: "unscheduled" },
  { label: "有投票设置", value: "polled" },
  { label: "无投票设置", value: "unpolled" },
];

// 前端过滤：关键词同时匹配显示名与原始 URL，筛选维度覆盖两个配置位
const filteredChannels = computed(() => {
  const kw = search.value.trim().toLowerCase();
  return channels.value.filter((row) => {
    if (configFilter.value === "scheduled" && !row.has_schedule) return false;
    if (configFilter.value === "unscheduled" && row.has_schedule) return false;
    if (configFilter.value === "polled" && !row.has_poll_settings) return false;
    if (configFilter.value === "unpolled" && row.has_poll_settings) return false;
    if (!kw) return true;
    return (
      (row.url || "").toLowerCase().includes(kw) ||
      (getChannelName(row.url) || "").toLowerCase().includes(kw)
    );
  });
});

// 未加载或出错时统计文案以 (–) 表示"未知"，避免与错误态自相矛盾
const statText = computed(() => {
  if (loadError.value || !loaded.value) return "–";
  const total = channels.value.length;
  const shown = filteredChannels.value.length;
  return shown === total ? `共 ${total} 个频道` : `${shown} / ${total} 个频道`;
});

const deletingUrl = ref("");
const generating = ref<Set<string>>(new Set());

// ── 数据加载 ───────────────────────────────────────────
async function loadChannels() {
  loading.value = true;
  loadError.value = false;
  try {
    const res = await getChannels();
    if (res.success) {
      channels.value = (res.data?.channels || []) as ChannelInfo[];
      loaded.value = true;
    } else {
      loadError.value = true;
      message.error(res.message || "加载频道列表失败");
    }
  } catch {
    loadError.value = true;
    message.error("加载频道列表失败");
  } finally {
    loading.value = false;
  }
}

// ── 添加频道 ───────────────────────────────────────────
const showAddModal = ref(false);
const saving = ref(false);
const formRef = ref<FormInst | null>(null);
const addForm = ref({ url: "" });
const addRules: FormRules = {
  url: { required: true, message: "请输入频道 URL", trigger: ["blur", "change"], type: "string" },
};

function openAddModal() {
  addForm.value = { url: "" };
  formRef.value?.restoreValidation();
  showAddModal.value = true;
}

async function handleAdd() {
  try {
    await formRef.value?.validate();
  } catch {
    return; // 校验不过留在弹窗内，行内错误已提示
  }
  saving.value = true;
  try {
    const res = await addChannel(addForm.value.url.trim());
    if (res.success) {
      message.success(res.message || "频道已添加");
      showAddModal.value = false;
      await loadChannels();
    } else {
      // 后端业务失败：不关弹窗，用户可直接改后重试
      message.error(res.message || "添加频道失败");
    }
  } catch {
    message.error("添加频道失败");
  } finally {
    saving.value = false;
  }
}

// ── 生成总结（行级 loading，互不阻塞） ─────────────────
async function handleGenerate(channelUrl: string) {
  if (generating.value.has(channelUrl)) return;
  generating.value.add(channelUrl);
  try {
    const res = await generateSummary(channelUrl);
    if (res.success) {
      message.success(res.message || "总结生成成功");
    } else {
      message.error(res.message || "总结生成失败");
    }
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : "生成总结请求失败";
    message.error(msg);
  } finally {
    generating.value.delete(channelUrl);
  }
}

// ── 删除频道（Popconfirm 确认后执行，成功才刷新列表） ──
async function removeChannel(channelUrl: string) {
  deletingUrl.value = channelUrl;
  try {
    const res = await deleteChannel(channelUrl);
    if (res.success) {
      message.success(res.message || "频道已删除");
      await loadChannels();
    } else {
      message.error(res.message || "删除频道失败");
    }
  } catch {
    message.error("删除频道失败");
  } finally {
    deletingUrl.value = "";
  }
}

onMounted(loadChannels);
</script>

<style scoped>
.channels-page {
  container: channels / inline-size;
  min-width: 0;
}

.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}

.page-title {
  font-size: clamp(24px, 2.5cqi, 30px);
  font-weight: 650;
}

.page-subtitle {
  margin-top: 4px;
  font-size: 14px;
  color: var(--sb-text-2);
}

.list-heading,
.list-tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.list-heading {
  justify-content: space-between;
  margin-bottom: 14px;
}

.list-heading h3 {
  font-size: 16px;
  font-weight: 650;
}

.list-count {
  padding: 4px 10px;
  border-radius: var(--sb-radius-pill);
  background: var(--sb-surface-2);
  font-size: 12px;
  color: var(--sb-text-2);
  white-space: nowrap;
}

.list-filters {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(9rem, 12rem);
  gap: 10px;
  margin-bottom: 16px;
}

.panel-card :deep(.n-card__content) {
  min-width: 0;
  padding: clamp(14px, 2.7cqi, 24px);
}

.skel {
  display: grid;
  gap: 10px;
  padding: 4px 0;
}

.table-empty {
  padding: 40px 0;
}

.empty-hint {
  font-size: 12px;
  color: var(--sb-text-2);
}

.channel-list {
  list-style: none;
  border-top: 1px solid var(--sb-border);
}

.channel-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 16px;
  padding: 14px 0;
}

.channel-row + .channel-row {
  border-top: 1px solid var(--sb-border);
}

.channel-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.channel-icon {
  display: grid;
  flex: none;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: var(--sb-radius);
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
  font-size: 18px;
}

.channel-copy {
  display: grid;
  gap: 2px;
  min-width: 0;
  overflow-wrap: anywhere;
}

.channel-name {
  font-weight: 650;
}

.channel-raw {
  font-size: 12px;
  color: var(--sb-text-2);
}

.channel-settings {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
}

.channel-settings div {
  display: grid;
  gap: 4px;
}

.channel-settings dt {
  font-size: 12px;
  color: var(--sb-text-2);
}

.row-actions,
.modal-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
}

.channel-input {
  display: grid;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.channel-input p {
  color: var(--sb-text-2);
  font-size: 12px;
}

.modal-footer {
  gap: 8px;
}

@container channels (max-width: 640px) {
  .channel-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 10px 12px;
  }

  .channel-identity {
    grid-column: 1 / -1;
  }
}

@container channels (max-width: 410px) {
  .list-filters,
  .channel-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .row-actions .n-button {
    min-height: 36px;
  }
}
</style>
