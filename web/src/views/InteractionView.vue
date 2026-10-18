<template>
  <n-spin :show="loading" description="加载中...">
    <div>
      <n-tabs type="line" animated>
        <!-- 投票设置 -->
        <n-tab-pane name="poll" tab="投票设置">
          <n-space vertical :size="16">
            <!-- 全局投票设置 -->
            <n-card title="全局投票设置">
              <n-form label-placement="left" label-width="140">
                <n-grid :cols="2" :x-gap="24" responsive="screen">
                  <n-gi>
                    <n-form-item label="投票功能">
                      <n-switch v-model:value="globalPoll.enable_poll" @update:value="saveGlobalPoll">
                        <template #checked>已启用</template>
                        <template #unchecked>已禁用</template>
                      </n-switch>
                    </n-form-item>
                  </n-gi>
                  <n-gi>
                    <n-form-item label="公开投票者">
                      <n-switch v-model:value="globalPoll.public_voters" @update:value="saveGlobalPoll">
                        <template #checked>公开</template>
                        <template #unchecked>匿名</template>
                      </n-switch>
                    </n-form-item>
                  </n-gi>
                  <n-gi>
                    <n-form-item label="重新生成阈值">
                      <n-input-number
                        v-model:value="globalPoll.poll_regen_threshold"
                        :min="1"
                        :max="100"
                        style="width: 160px"
                        @update:value="saveGlobalPoll"
                      >
                        <template #suffix>票</template>
                      </n-input-number>
                    </n-form-item>
                  </n-gi>
                  <n-gi>
                    <n-form-item label="投票重新生成请求">
                      <n-switch
                        v-model:value="globalPoll.enable_vote_regen_request"
                        @update:value="saveGlobalPoll"
                      >
                        <template #checked>已启用</template>
                        <template #unchecked>已禁用</template>
                      </n-switch>
                    </n-form-item>
                  </n-gi>
                </n-grid>
              </n-form>
            </n-card>

            <!-- 投票提示词 -->
            <n-card title="投票提示词">
              <template #header-extra>
                <n-button size="small" quaternary @click="resetPollPrompt">
                  恢复默认
                </n-button>
              </template>
              <n-input
                v-model:value="pollPrompt"
                type="textarea"
                :rows="6"
                placeholder="投票提示词..."
              />
              <div style="margin-top: 8px; text-align: right">
                <n-button type="primary" size="small" :loading="savingPrompt" @click="savePollPrompt">
                  保存提示词
                </n-button>
              </div>
            </n-card>

            <!-- 频道投票配置 -->
            <n-card title="频道投票配置">
              <template #header-extra>
                <n-space align="center">
                  <n-tag type="info" size="small">
                    {{ Object.keys(pollSettings.settings).length }} 个频道
                  </n-tag>
                  <n-button
                    type="primary"
                    size="small"
                    :disabled="!availablePollChannels.length"
                    @click="openChannelPoll()"
                  >
                    添加频道配置
                  </n-button>
                </n-space>
              </template>
              <n-text depth="3" style="display: block; margin-bottom: 8px">
                此处的投票为「总结投票」：每次总结完成后根据总结内容生成并发送，与「自动投票」页签中逐帖触发的自动趣味投票相互独立。
              </n-text>
              <n-empty
                v-if="!Object.keys(pollSettings.settings).length"
                :description="channels.length ? '暂无频道投票配置，点击右上角添加' : '暂无频道，请先在频道管理中添加频道'"
              >
                <template v-if="!channels.length" #extra>
                  <router-link to="/channels">
                    <n-button size="small">前往频道管理</n-button>
                  </router-link>
                </template>
              </n-empty>
              <n-table v-else :bordered="false" :single-line="false" size="small" striped>
                <thead>
                  <tr>
                    <th>频道</th>
                    <th style="width: 120px">投票</th>
                    <th style="width: 120px">发送到频道</th>
                    <th style="width: 120px">公开投票</th>
                    <th style="width: 120px">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(setting, channel) in pollSettings.settings" :key="String(channel)">
                    <td>
                      <n-tag size="small" :bordered="false">{{ getChannelName(String(channel)) }}</n-tag>
                    </td>
                    <td>
                      <n-switch
                        size="small"
                        :value="setting.enabled ?? globalPoll.enable_poll"
                        :disabled="savingPollChannels.has(String(channel))"
                        @update:value="(v: boolean) => saveChannelPoll(String(channel), { enabled: v })"
                      />
                      <n-text v-if="setting.enabled == null" depth="3" style="margin-left: 8px">
                        继承全局
                      </n-text>
                    </td>
                    <td>
                      <n-switch
                        size="small"
                        :value="setting.send_to_channel ?? false"
                        :disabled="savingPollChannels.has(String(channel))"
                        @update:value="(v: boolean) => saveChannelPoll(String(channel), { send_to_channel: v })"
                      />
                    </td>
                    <td>
                      <n-switch
                        size="small"
                        :value="setting.public_voters ?? globalPoll.public_voters"
                        :disabled="savingPollChannels.has(String(channel))"
                        @update:value="(v: boolean) => saveChannelPoll(String(channel), { public_voters: v })"
                      />
                      <n-text v-if="setting.public_voters == null" depth="3" style="margin-left: 8px">
                        继承全局
                      </n-text>
                    </td>
                    <td>
                      <n-space :wrap="false">
                        <n-button
                          size="tiny"
                          quaternary
                          :disabled="savingPollChannels.has(String(channel))"
                          @click="openChannelPoll(String(channel))"
                        >
                          编辑
                        </n-button>
                        <n-button
                          size="tiny"
                          quaternary
                          type="error"
                          :disabled="savingPollChannels.has(String(channel))"
                          @click="deleteChannelPoll(String(channel))"
                        >
                          删除
                        </n-button>
                      </n-space>
                    </td>
                  </tr>
                </tbody>
              </n-table>
            </n-card>
          </n-space>
        </n-tab-pane>

        <!-- 自动投票 -->
        <n-tab-pane name="auto-poll" tab="自动投票">
          <n-space vertical :size="16">
            <!-- 全局自动投票 -->
            <n-card title="自动趣味投票">
              <template #header-extra>
                <n-switch v-model:value="autoPollEnabled" @update:value="handleAutoPollToggle">
                  <template #checked>已启用</template>
                  <template #unchecked>已禁用</template>
                </n-switch>
              </template>
              <n-text depth="3">
                启用后，频道每条符合条件的帖子同步到讨论组时，将根据该帖内容自动生成趣味投票并回复到讨论组。
                与「投票设置」中总结完成后发送的普通投票是两个独立功能，互不影响。可以为每个频道单独配置。
              </n-text>
            </n-card>

            <!-- 频道自动投票配置 -->
            <n-card title="频道自动投票配置">
              <template #header-extra>
                <n-tag type="info" size="small">
                  {{ autoPollConfiguredCount }} / {{ channels.length }} 个频道
                </n-tag>
              </template>

              <n-empty v-if="!channels.length" description="暂无频道" />
              <n-table v-else :bordered="false" :single-line="false" size="small" striped>
                <thead>
                  <tr>
                    <th>频道</th>
                    <th style="width: 180px">自动投票</th>
                    <th style="width: 120px">状态</th>
                    <th style="width: 80px">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="ch in channels" :key="ch">
                    <td>
                      <n-tag size="small" :bordered="false">{{ getChannelName(ch) }}</n-tag>
                    </td>
                    <td>
                      <n-switch
                        size="small"
                        :value="getAutoPollChannelEnabled(ch)"
                        :disabled="!autoPollEnabled"
                        @update:value="(v: boolean) => saveChannelAutoPoll(ch, v)"
                      >
                        <template #checked>已启用</template>
                        <template #unchecked>已禁用</template>
                      </n-switch>
                    </td>
                    <td>
                      <n-tag size="small" :type="getAutoPollChannelStatus(ch).type">
                        {{ getAutoPollChannelStatus(ch).label }}
                      </n-tag>
                    </td>
                    <td>
                      <n-button
                        size="tiny"
                        quaternary
                        type="error"
                        :disabled="!autoPollEnabled || !autoPollChannelSettings[ch]"
                        @click="deleteChannelAutoPollSetting(ch)"
                      >
                        重置
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </n-table>
            </n-card>
          </n-space>
        </n-tab-pane>

        <!-- 评论区欢迎 -->
        <n-tab-pane name="welcome" tab="评论区欢迎">
          <n-space vertical :size="16">
            <n-card title="默认欢迎配置">
              <n-form label-placement="left" label-width="120">
                <n-form-item label="启用">
                  <n-switch v-model:value="welcomeConfig.enabled">
                    <template #checked>已启用</template>
                    <template #unchecked>已禁用</template>
                  </n-switch>
                </n-form-item>
                <n-form-item label="欢迎消息">
                  <n-input
                    v-model:value="welcomeConfig.welcome_message"
                    type="textarea"
                    :rows="3"
                    placeholder="欢迎来到评论区！"
                  />
                </n-form-item>
                <n-form-item label="按钮文本">
                  <n-input v-model:value="welcomeConfig.button_text" placeholder="申请总结" />
                </n-form-item>
                <n-form-item label="按钮动作">
                  <n-select
                    v-model:value="welcomeConfig.button_action"
                    :options="[{ label: '申请总结', value: 'request_summary' }]"
                  />
                </n-form-item>
                <n-form-item>
                  <n-button type="primary" :loading="savingWelcome" @click="saveWelcomeConfig">
                    保存配置
                  </n-button>
                </n-form-item>
              </n-form>
            </n-card>

            <!-- 频道覆盖配置 -->
            <n-card title="频道覆盖配置">
              <template #header-extra>
                <n-tag type="info" size="small">
                  {{ Object.keys(welcomeOverrides || {}).length }} 个频道
                </n-tag>
              </template>
              <n-empty
                v-if="!Object.keys(welcomeOverrides || {}).length"
                description="暂无频道覆盖配置"
              />
              <n-table v-else :bordered="false" :single-line="false" size="small" striped>
                <thead>
                  <tr>
                    <th>频道</th>
                    <th style="width: 100px">启用</th>
                    <th style="width: 80px">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(cfg, ch) in welcomeOverrides" :key="String(ch)">
                    <td>
                      <n-tag size="small" :bordered="false">{{ getChannelName(String(ch)) }}</n-tag>
                    </td>
                    <td>
                      <n-switch
                        size="small"
                        :value="(cfg as any).enabled"
                        @update:value="(v: boolean) => toggleChannelWelcome(String(ch), v)"
                      />
                    </td>
                    <td>
                      <n-button
                        size="tiny"
                        quaternary
                        type="error"
                        @click="deleteChannelWelcomeOverride(String(ch))"
                      >
                        删除
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </n-table>
            </n-card>
          </n-space>
        </n-tab-pane>
      </n-tabs>
    </div>
  </n-spin>

  <n-modal
    v-model:show="showChannelPollModal"
    preset="card"
    :title="editingPollChannel ? '编辑频道投票配置' : '添加频道投票配置'"
    style="width: 560px; max-width: calc(100vw - 32px)"
    :mask-closable="!savingChannelPoll"
    :close-on-esc="!savingChannelPoll"
    :closable="!savingChannelPoll"
  >
    <n-form label-placement="top" :disabled="savingChannelPoll">
      <n-form-item label="频道">
        <n-select
          v-model:value="channelPollForm.channel"
          :options="pollChannelOptions"
          :disabled="!!editingPollChannel || savingChannelPoll"
          filterable
          placeholder="选择要单独配置投票的频道"
        />
      </n-form-item>
      <n-form-item label="投票功能">
        <n-select v-model:value="channelPollForm.enabled" :options="pollEnabledOptions" />
      </n-form-item>
      <n-form-item label="发送位置">
        <n-switch v-model:value="channelPollForm.send_to_channel">
          <template #checked>频道</template>
          <template #unchecked>讨论组</template>
        </n-switch>
      </n-form-item>
      <n-form-item label="公开投票者">
        <n-select v-model:value="channelPollForm.public_voters" :options="pollVoterOptions" />
      </n-form-item>
    </n-form>
    <n-text depth="3">选择“继承全局”可取消该项覆盖；删除频道配置后恢复全局默认设置。</n-text>
    <template #footer>
      <n-space justify="end">
        <n-button :disabled="savingChannelPoll" @click="showChannelPollModal = false">取消</n-button>
        <n-button
          type="primary"
          :loading="savingChannelPoll"
          :disabled="!channelPollForm.channel"
          @click="saveChannelPollForm"
        >
          保存配置
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from "vue";
import { useMessage, useDialog } from "naive-ui";
import {
  getPollSettings,
  updateGlobalPollSettings,
  updatePollSettings,
  deletePollSettings,
  getAutoPollSettings,
  updateAutoPoll,
  updateChannelAutoPoll,
  deleteChannelAutoPoll as deleteChannelAutoPollApi,
  getInteractionChannels,
  getCommentWelcomeConfig,
  updateDefaultCommentWelcome,
  getPrompt,
  updatePrompt,
  resetPrompt,
} from "@/api/modules";
import type { ChannelPollSettings } from "@/api/modules";
import { getChannelName } from "@/utils/formatters";

const message = useMessage();
const dialog = useDialog();
const loading = ref(true);

// ===== 投票设置 =====
const pollSettings = ref<{ settings: Record<string, ChannelPollSettings> }>({ settings: {} });
type PollOverride = "inherit" | "on" | "off";
const showChannelPollModal = ref(false);
const editingPollChannel = ref<string | null>(null);
const savingChannelPoll = ref(false);
const savingPollChannels = reactive(new Set<string>());
const channelPollForm = reactive({
  channel: null as string | null,
  enabled: "inherit" as PollOverride,
  send_to_channel: false,
  public_voters: "inherit" as PollOverride,
});
const pollPrompt = ref("");
const savingPrompt = ref(false);
const globalPoll = reactive({
  enable_poll: true,
  poll_regen_threshold: 5,
  public_voters: true,
  enable_vote_regen_request: true,
});

// ===== 自动投票 =====
const autoPollEnabled = ref(false);
const autoPollChannelSettings = ref<Record<string, any>>({});
const channels = ref<string[]>([]);
const availablePollChannels = computed(() =>
  [...new Set(channels.value)].filter((channel) => !(channel in pollSettings.value.settings)),
);
const pollChannelOptions = computed(() =>
  (editingPollChannel.value ? [editingPollChannel.value] : availablePollChannels.value).map(
    (channel) => ({ label: getChannelName(channel), value: channel }),
  ),
);
const pollEnabledOptions = computed(() => [
  { label: `继承全局（${globalPoll.enable_poll ? "已启用" : "已禁用"}）`, value: "inherit" },
  { label: "启用", value: "on" },
  { label: "禁用", value: "off" },
]);
const pollVoterOptions = computed(() => [
  { label: `继承全局（${globalPoll.public_voters ? "公开" : "匿名"}）`, value: "inherit" },
  { label: "公开", value: "on" },
  { label: "匿名", value: "off" },
]);
const autoPollConfiguredCount = computed(
  () => Object.keys(autoPollChannelSettings.value || {}).length,
);

// ===== 评论区欢迎 =====
const savingWelcome = ref(false);
const welcomeOverrides = ref<Record<string, any>>({});
const welcomeConfig = reactive({
  enabled: true,
  welcome_message: "",
  button_text: "",
  button_action: "request_summary",
});

async function loadData() {
  loading.value = true;
  try {
    const [pollRes, autoPollRes, welcomeRes, channelsRes, promptRes] = await Promise.all([
      getPollSettings(),
      getAutoPollSettings(),
      getCommentWelcomeConfig(),
      getInteractionChannels(),
      getPrompt("poll").catch(() => ({ success: false, data: { content: "" } })),
    ]);

    if (pollRes.success) {
      pollSettings.value = { settings: pollRes.data.settings || {} };
      globalPoll.enable_poll = pollRes.data.enable_poll ?? true;
      globalPoll.poll_regen_threshold = pollRes.data.poll_regen_threshold ?? 5;
      globalPoll.public_voters = pollRes.data.public_voters ?? true;
      globalPoll.enable_vote_regen_request = pollRes.data.enable_vote_regen_request ?? true;
    }

    if (autoPollRes.success) {
      autoPollEnabled.value = autoPollRes.data.enabled;
      autoPollChannelSettings.value = autoPollRes.data.channel_settings;
    }

    if (welcomeRes.success) {
      const def = welcomeRes.data.default || {};
      welcomeConfig.enabled = def.enabled ?? true;
      welcomeConfig.welcome_message = def.welcome_message ?? "";
      welcomeConfig.button_text = def.button_text ?? "";
      welcomeConfig.button_action = def.button_action ?? "request_summary";
      welcomeOverrides.value = welcomeRes.data.channel_overrides || {};
    }

    if (channelsRes.success) {
      channels.value = channelsRes.data.channels || [];
    }

    if (promptRes.success) {
      pollPrompt.value = promptRes.data.content || "";
    }
  } catch {
    message.error("加载数据失败");
  } finally {
    loading.value = false;
  }
}

// ===== 投票设置操作 =====
let globalPollTimer: ReturnType<typeof setTimeout> | null = null;

function saveGlobalPoll() {
  if (globalPollTimer) clearTimeout(globalPollTimer);
  globalPollTimer = setTimeout(async () => {
    try {
      const res = await updateGlobalPollSettings({ ...globalPoll });
      if (!res.success) message.error(res.message);
    } catch {
      message.error("保存失败");
    }
  }, 500);
}

function openChannelPoll(channel?: string) {
  const setting = channel ? pollSettings.value.settings[channel] : undefined;
  editingPollChannel.value = channel ?? null;
  channelPollForm.channel = channel ?? null;
  channelPollForm.enabled = setting?.enabled == null ? "inherit" : setting.enabled ? "on" : "off";
  channelPollForm.send_to_channel = setting?.send_to_channel ?? false;
  channelPollForm.public_voters =
    setting?.public_voters == null ? "inherit" : setting.public_voters ? "on" : "off";
  showChannelPollModal.value = true;
}

async function saveChannelPollForm() {
  const channel = channelPollForm.channel;
  if (!channel || savingChannelPoll.value) return;
  savingChannelPoll.value = true;
  try {
    const saved = await saveChannelPoll(channel, {
      enabled: channelPollForm.enabled === "inherit" ? null : channelPollForm.enabled === "on",
      send_to_channel: channelPollForm.send_to_channel,
      public_voters:
        channelPollForm.public_voters === "inherit" ? null : channelPollForm.public_voters === "on",
    });
    if (saved) {
      showChannelPollModal.value = false;
      message.success("频道投票配置已保存");
    }
  } finally {
    savingChannelPoll.value = false;
  }
}

async function saveChannelPoll(channel: string, data: ChannelPollSettings): Promise<boolean> {
  if (savingPollChannels.has(channel)) return false;
  savingPollChannels.add(channel);
  try {
    const res = await updatePollSettings(channel, data);
    if (res.success) {
      const settings = { ...(pollSettings.value.settings || {}) };
      const current = { ...(settings[channel] || {}) };
      Object.assign(current, data);
      settings[channel] = current;
      pollSettings.value = { ...pollSettings.value, settings };
      return true;
    } else {
      message.error(res.message);
    }
  } catch {
    message.error("保存失败");
  } finally {
    savingPollChannels.delete(channel);
  }
  return false;
}

function deleteChannelPoll(channel: string) {
  dialog.warning({
    title: "确认删除",
    content: `确定要删除 ${getChannelName(channel)} 的投票配置吗？`,
    positiveText: "删除",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        const res = await deletePollSettings(channel);
        message[res.success ? "success" : "error"](res.message);
        if (res.success) await loadData();
      } catch {
        message.error("删除失败");
      }
    },
  });
}

async function savePollPrompt() {
  savingPrompt.value = true;
  try {
    const res = await updatePrompt("poll", pollPrompt.value);
    message[res.success ? "success" : "error"](res.message);
  } catch {
    message.error("保存提示词失败");
  } finally {
    savingPrompt.value = false;
  }
}

async function resetPollPrompt() {
  dialog.warning({
    title: "恢复默认",
    content: "确定要将投票提示词恢复为默认值吗？",
    positiveText: "恢复",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        const res = await resetPrompt("poll");
        if (res.success) {
          pollPrompt.value = res.data?.content || "";
          message.success("已恢复默认提示词");
        } else {
          message.error(res.message);
        }
      } catch {
        message.error("恢复失败");
      }
    },
  });
}

// ===== 自动投票操作 =====
async function handleAutoPollToggle(enabled: boolean) {
  try {
    const res = await updateAutoPoll(enabled);
    if (res.success) {
      message.success(res.message);
    } else {
      autoPollEnabled.value = !enabled;
      message.error(res.message);
    }
  } catch {
    autoPollEnabled.value = !enabled;
    message.error("操作失败");
  }
}

function getAutoPollChannelEnabled(channel: string): boolean {
  if (!autoPollEnabled.value) return false;
  const cfg = autoPollChannelSettings.value[channel];
  if (cfg) return !!cfg.enabled;
  return true;
}

function getAutoPollChannelStatus(
  channel: string,
): { label: string; type: "default" | "success" | "info" | "warning" } {
  if (!autoPollEnabled.value) {
    return { label: "全局已关闭", type: "default" };
  }

  const cfg = autoPollChannelSettings.value[channel];
  if (!cfg) {
    return { label: "继承全局", type: "success" };
  }

  return cfg.enabled
    ? { label: "独立开启", type: "info" }
    : { label: "独立禁用", type: "warning" };
}

async function saveChannelAutoPoll(channel: string, enabled: boolean) {
  if (!autoPollEnabled.value) return;

  const previousSettings = { ...autoPollChannelSettings.value };
  try {
    const res = await updateChannelAutoPoll(channel, enabled);
    if (res.success) {
      const newSettings = { ...autoPollChannelSettings.value };
      newSettings[channel] = { enabled };
      autoPollChannelSettings.value = newSettings;
    } else {
      autoPollChannelSettings.value = previousSettings;
      message.error(res.message);
    }
  } catch {
    autoPollChannelSettings.value = previousSettings;
    await loadData();
    message.error("保存失败");
  }
}

async function deleteChannelAutoPollSetting(channel: string) {
  if (!autoPollEnabled.value) return;

  const previousSettings = { ...autoPollChannelSettings.value };
  try {
    const res = await deleteChannelAutoPollApi(channel);
    if (res.success) {
      const newSettings = { ...autoPollChannelSettings.value };
      delete newSettings[channel];
      autoPollChannelSettings.value = newSettings;
      message.success("已重置为全局设置");
    } else {
      autoPollChannelSettings.value = previousSettings;
      message.error(res.message);
    }
  } catch {
    autoPollChannelSettings.value = previousSettings;
    await loadData();
    message.error("操作失败");
  }
}

// ===== 评论区欢迎操作 =====
async function saveWelcomeConfig() {
  savingWelcome.value = true;
  try {
    const res = await updateDefaultCommentWelcome({ ...welcomeConfig });
    message[res.success ? "success" : "error"](res.message);
  } catch {
    message.error("保存失败");
  } finally {
    savingWelcome.value = false;
  }
}

async function toggleChannelWelcome(channel: string, enabled: boolean) {
  try {
    const res = await updateDefaultCommentWelcome({ enabled });
    if (res.success) {
      const newOverrides = { ...welcomeOverrides.value };
      newOverrides[channel] = { ...(newOverrides[channel] || {}), enabled };
      welcomeOverrides.value = newOverrides;
    }
  } catch {
    message.error("更新失败");
  }
}

function deleteChannelWelcomeOverride(channel: string) {
  dialog.warning({
    title: "确认删除",
    content: `确定要删除 ${getChannelName(channel)} 的评论区欢迎覆盖配置吗？`,
    positiveText: "删除",
    negativeText: "取消",
    onPositiveClick: async () => {
      // 更新本地状态
      const newOverrides = { ...welcomeOverrides.value };
      delete newOverrides[channel];
      welcomeOverrides.value = newOverrides;
      message.success("已删除频道覆盖配置");
    },
  });
}

onMounted(loadData);
</script>
