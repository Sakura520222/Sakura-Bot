<template>
  <div class="interaction-page">
    <!-- 统一页头：标题 + 一句话用途 + 右侧主操作（刷新常驻；「添加频道配置」随投票页签出现） -->
    <header class="page-header">
      <div>
        <h2 class="page-title">互动设置</h2>
        <p class="page-subtitle">统一配置频道投票、自动趣味投票与评论区欢迎行为</p>
      </div>
      <div class="page-header-actions">
        <n-button quaternary circle :loading="refreshing" aria-label="刷新" @click="handleRefresh">
          <template #icon><RefreshOutlined /></template>
        </n-button>
        <n-button
          v-if="activeTab === 'poll'"
          type="primary"
          :disabled="!availablePollChannels.length"
          @click="openChannelPoll()"
        >
          <template #icon><AddOutlined /></template>
          添加频道配置
        </n-button>
      </div>
    </header>

    <!-- 异步三态：首屏骨架屏 -->
    <div v-if="initialLoading" class="page-skeleton">
      <n-skeleton text style="width: 220px" />
      <n-skeleton text style="width: 320px" />
      <n-skeleton height="200px" width="100%" />
      <n-skeleton height="160px" width="100%" />
    </div>

    <!-- 异步三态：整页加载失败给出重试入口，避免静默白屏 -->
    <n-result
      v-else-if="loadError"
      class="load-error"
      status="error"
      title="加载失败"
      description="互动设置未能加载，请检查网络连接后重试。"
    >
      <template #footer>
        <n-button type="primary" @click="handleRetry">重试</n-button>
      </template>
    </n-result>

    <n-tabs v-else v-model:value="activeTab" type="line" animated>
      <!-- ==================== 投票设置 ==================== -->
      <n-tab-pane name="poll" tab="投票设置" display-directive="show:lazy">
        <div class="tab-sections">
          <!-- 全局投票设置：多字段表单，采用分区显式保存（保存策略：表单 → 保存按钮；单开关 → 即时保存） -->
          <n-card title="全局投票设置" class="section-card">
            <p class="card-desc">总结投票的总开关与重新生成规则，未单独配置的频道跟随这里。</p>
            <n-form
              ref="globalPollFormRef"
              :model="globalPoll"
              :rules="globalPollRules"
              label-placement="top"
            >
              <div class="form-grid">
                <n-form-item label="投票功能" path="enable_poll">
                  <div class="field-control">
                    <n-switch v-model:value="globalPoll.enable_poll">
                      <template #checked>已启用</template>
                      <template #unchecked>已禁用</template>
                    </n-switch>
                    <div class="field-help">总开关：关闭后不再为任何频道生成总结投票</div>
                  </div>
                </n-form-item>
                <n-form-item label="公开投票者" path="public_voters">
                  <div class="field-control">
                    <n-switch v-model:value="globalPoll.public_voters">
                      <template #checked>公开</template>
                      <template #unchecked>匿名</template>
                    </n-switch>
                    <div class="field-help">开启后投票人名单公开可见，关闭则为匿名投票</div>
                  </div>
                </n-form-item>
                <n-form-item label="重新生成阈值" path="poll_regen_threshold">
                  <div class="field-control">
                    <n-input-number
                      v-model:value="globalPoll.poll_regen_threshold"
                      :min="1"
                      :max="100"
                      class="threshold-input"
                    >
                      <template #suffix>票</template>
                    </n-input-number>
                    <div class="field-help">总结投票累计票数达到该值后触发重新生成（1-100）</div>
                  </div>
                </n-form-item>
                <n-form-item label="投票重新生成请求" path="enable_vote_regen_request">
                  <div class="field-control">
                    <n-switch v-model:value="globalPoll.enable_vote_regen_request">
                      <template #checked>已启用</template>
                      <template #unchecked>已禁用</template>
                    </n-switch>
                    <div class="field-help">允许成员通过投票发起重新生成请求</div>
                  </div>
                </n-form-item>
              </div>
              <div class="card-footer">
                <n-button
                  type="primary"
                  :loading="savingGlobalPoll"
                  :disabled="!globalPollDirty"
                  @click="saveGlobalPollSettings"
                >
                  保存设置
                </n-button>
                <n-text v-if="globalPollDirty" depth="3" class="dirty-hint">有未保存的修改</n-text>
              </div>
            </n-form>
          </n-card>

          <!-- 投票提示词：独立文本表单，显式保存；恢复默认需二次确认 -->
          <n-card title="投票提示词" class="section-card">
            <template #header-extra>
              <n-popconfirm @positive-click="resetPollPrompt">
                <template #trigger>
                  <n-button size="small" quaternary>恢复默认</n-button>
                </template>
                将用系统默认提示词覆盖当前内容，确定继续？
              </n-popconfirm>
            </template>
            <p class="card-desc">引导 AI 生成总结投票内容的提示词，修改后记得点击保存。</p>
            <n-input
              v-model:value="pollPrompt"
              type="textarea"
              :rows="6"
              placeholder="输入投票提示词..."
            />
            <div class="card-footer">
              <n-button
                type="primary"
                :loading="savingPrompt"
                :disabled="!promptDirty"
                @click="savePollPrompt"
              >
                保存提示词
              </n-button>
              <n-text v-if="promptDirty" depth="3" class="dirty-hint">有未保存的修改</n-text>
            </div>
          </n-card>

          <!-- 频道投票配置：逐频道覆盖，行内开关即时保存 -->
          <n-card title="频道投票配置" class="section-card">
            <template #header-extra>
              <span class="sb-pill sb-pill--neutral">{{ pollChannelRows.length }} 个频道</span>
            </template>
            <p class="card-desc">
              此处的投票为「总结投票」：每次总结完成后根据总结内容生成并发送，与「自动投票」页签中逐帖触发的自动趣味投票相互独立。
            </p>
            <n-data-table
              :columns="pollChannelColumns"
              :data="pollChannelRows"
              :row-key="pollRowKey"
              :loading="refreshing"
              size="small"
            >
              <template #empty>
                <n-empty
                  :description="
                    channels.length
                      ? '暂无频道投票配置，点击右上角「添加频道配置」开始'
                      : '暂无频道，请先在频道管理中添加频道'
                  "
                >
                  <template #extra>
                    <n-button
                      v-if="channels.length"
                      size="small"
                      type="primary"
                      :disabled="!availablePollChannels.length"
                      @click="openChannelPoll()"
                    >
                      添加频道配置
                    </n-button>
                    <router-link v-else to="/channels">
                      <n-button size="small">前往频道管理</n-button>
                    </router-link>
                  </template>
                </n-empty>
              </template>
            </n-data-table>
          </n-card>
        </div>
      </n-tab-pane>

      <!-- ==================== 自动投票 ==================== -->
      <n-tab-pane name="auto-poll" tab="自动投票" display-directive="show:lazy">
        <div class="tab-sections">
          <!-- 全局总开关：专用接口即时生效，失败自动回滚 -->
          <n-card title="自动趣味投票" class="section-card">
            <p class="card-desc">
              启用后，频道每条符合条件的帖子同步到讨论组时，将根据该帖内容自动生成趣味投票并回复到讨论组。与「投票设置」中总结完成后发送的普通投票是两个独立功能，互不影响，可为每个频道单独配置。
            </p>
            <div class="field-control">
              <n-switch
                :value="autoPollEnabled"
                :loading="autoPollToggling"
                @update:value="handleAutoPollToggle"
              >
                <template #checked>已启用</template>
                <template #unchecked>已禁用</template>
              </n-switch>
              <div class="field-help">总开关关闭时所有频道自动投票暂停，已保存的单独配置不会丢失</div>
            </div>
          </n-card>

          <!-- 频道自动投票配置：全频道清单 + 独立覆盖状态 -->
          <n-card title="频道自动投票配置" class="section-card">
            <template #header-extra>
              <span class="sb-pill sb-pill--neutral">
                {{ autoPollConfiguredCount }} / {{ channels.length }} 个频道
              </span>
            </template>
            <p class="card-desc">为单个频道单独开启或关闭自动趣味投票，未配置的频道默认跟随总开关。</p>
            <n-data-table
              :columns="autoPollColumns"
              :data="autoPollRows"
              :row-key="autoPollRowKey"
              :loading="refreshing"
              size="small"
            >
              <template #empty>
                <n-empty description="暂无可用频道，请先在频道管理中添加频道">
                  <template #extra>
                    <router-link to="/channels">
                      <n-button size="small">前往频道管理</n-button>
                    </router-link>
                  </template>
                </n-empty>
              </template>
            </n-data-table>
          </n-card>
        </div>
      </n-tab-pane>

      <!-- ==================== 评论区欢迎 ==================== -->
      <n-tab-pane name="welcome" tab="评论区欢迎" display-directive="show:lazy">
        <div class="tab-sections">
          <!-- 默认欢迎配置：多字段表单，分区显式保存（启用时才校验必填） -->
          <n-card title="默认欢迎配置" class="section-card">
            <p class="card-desc">频道评论区使用的默认欢迎语与引导按钮，可被下方频道覆盖配置单独替换。</p>
            <n-form
              ref="welcomeFormRef"
              :model="welcomeConfig"
              :rules="welcomeRules"
              label-placement="top"
            >
              <n-form-item label="启用欢迎" path="enabled">
                <div class="field-control">
                  <n-switch v-model:value="welcomeConfig.enabled">
                    <template #checked>已启用</template>
                    <template #unchecked>已禁用</template>
                  </n-switch>
                  <div class="field-help">关闭后不发送欢迎消息（除非频道覆盖配置单独启用）</div>
                </div>
              </n-form-item>
              <n-form-item label="欢迎消息" path="welcome_message">
                <n-input
                  v-model:value="welcomeConfig.welcome_message"
                  type="textarea"
                  :rows="3"
                  placeholder="欢迎来到评论区！"
                />
              </n-form-item>
              <n-form-item label="按钮文本" path="button_text">
                <n-input v-model:value="welcomeConfig.button_text" placeholder="申请总结" />
              </n-form-item>
              <n-form-item label="按钮动作" path="button_action">
                <n-select
                  v-model:value="welcomeConfig.button_action"
                  :options="[{ label: '申请总结', value: 'request_summary' }]"
                />
              </n-form-item>
              <div class="card-footer">
                <n-button
                  type="primary"
                  :loading="savingWelcome"
                  :disabled="!welcomeDirty"
                  @click="saveWelcomeConfig"
                >
                  保存设置
                </n-button>
                <n-text v-if="welcomeDirty" depth="3" class="dirty-hint">有未保存的修改</n-text>
              </div>
            </n-form>
          </n-card>

          <!-- 频道覆盖配置：仅展示与启停，覆盖项由后端/其他流程创建 -->
          <n-card title="频道覆盖配置" class="section-card">
            <template #header-extra>
              <span class="sb-pill sb-pill--neutral">{{ welcomeOverrideRows.length }} 个频道</span>
            </template>
            <p class="card-desc">已单独配置欢迎行为的频道，未列出的频道使用上方默认配置。</p>
            <n-data-table
              :columns="welcomeColumns"
              :data="welcomeOverrideRows"
              :row-key="welcomeRowKey"
              :loading="refreshing"
              size="small"
            >
              <template #empty>
                <n-empty description="暂无频道覆盖配置，所有频道均使用默认欢迎配置" />
              </template>
            </n-data-table>
          </n-card>
        </div>
      </n-tab-pane>
    </n-tabs>

    <!-- 添加/编辑频道投票配置弹窗：三态选项支持「继承全局」 -->
    <n-modal
      v-model:show="showChannelPollModal"
      preset="card"
      class="modal-responsive"
      :title="editingPollChannel ? '编辑频道投票配置' : '添加频道投票配置'"
      :mask-closable="!savingChannelPoll"
      :close-on-esc="!savingChannelPoll"
      :closable="!savingChannelPoll"
    >
      <n-form
        ref="channelPollFormRef"
        :model="channelPollForm"
        :rules="channelPollRules"
        label-placement="top"
        :disabled="savingChannelPoll"
      >
        <n-form-item label="频道" path="channel">
          <n-select
            v-model:value="channelPollForm.channel"
            :options="pollChannelOptions"
            :disabled="!!editingPollChannel"
            filterable
            placeholder="选择要单独配置投票的频道"
          />
        </n-form-item>
        <n-form-item label="投票功能" path="enabled">
          <n-select v-model:value="channelPollForm.enabled" :options="pollEnabledOptions" />
        </n-form-item>
        <n-form-item label="发送位置" path="send_to_channel">
          <div class="field-control">
            <n-switch v-model:value="channelPollForm.send_to_channel">
              <template #checked>频道</template>
              <template #unchecked>讨论组</template>
            </n-switch>
            <div class="field-help">总结投票发送到频道本身，还是发送到关联讨论组</div>
          </div>
        </n-form-item>
        <n-form-item label="公开投票者" path="public_voters">
          <n-select v-model:value="channelPollForm.public_voters" :options="pollVoterOptions" />
        </n-form-item>
      </n-form>
      <n-text depth="3" class="modal-hint">
        选择「继承全局」可取消该项覆盖；删除频道配置后将恢复全局默认设置。
      </n-text>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary :disabled="savingChannelPoll" @click="showChannelPollModal = false">
            取消
          </n-button>
          <n-button
            type="primary"
            :loading="savingChannelPoll"
            :disabled="!channelPollForm.channel"
            @click="saveChannelPollForm"
          >
            保存配置
          </n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from "vue";
import { useMessage } from "naive-ui";
// 表格列渲染函数需要组件引用，故显式引入（模板中的 n-* 组件由全局注册提供）
import { NButton, NPopconfirm, NSwitch, NTag, NText } from "naive-ui";
import type { DataTableColumns, FormInst, FormRules } from "naive-ui";
import { AddOutlined, RefreshOutlined } from "@/components/icons";
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

// ===== 异步三态 =====
const initialLoading = ref(true);
const refreshing = ref(false);
const loadError = ref(false);
const activeTab = ref("poll");

// ===== 投票设置 =====
const pollSettings = ref<{ settings: Record<string, ChannelPollSettings> }>({ settings: {} });
type PollOverride = "inherit" | "on" | "off";
const showChannelPollModal = ref(false);
const editingPollChannel = ref<string | null>(null);
const savingChannelPoll = ref(false);
// 每个频道行内开关的保存中状态（防并发点击）
const savingPollChannels = reactive(new Set<string>());
const channelPollForm = reactive({
  channel: null as string | null,
  enabled: "inherit" as PollOverride,
  send_to_channel: false,
  public_voters: "inherit" as PollOverride,
});
const channelPollFormRef = ref<FormInst | null>(null);
const channelPollRules: FormRules = {
  channel: [
    {
      required: true,
      trigger: ["change", "blur"],
      validator: (_rule, value: string | null) => (value ? true : new Error("请选择要配置的频道")),
    },
  ],
};
const pollPrompt = ref("");
const lastSavedPrompt = ref("");
const promptDirty = computed(() => pollPrompt.value !== lastSavedPrompt.value);
const savingPrompt = ref(false);
const globalPoll = reactive({
  enable_poll: true,
  poll_regen_threshold: 5,
  public_voters: true,
  enable_vote_regen_request: true,
});
const globalPollFormRef = ref<FormInst | null>(null);
const globalPollRules: FormRules = {
  poll_regen_threshold: [
    { required: true, type: "number", message: "请填写重新生成阈值", trigger: ["blur", "change"] },
    { type: "number", min: 1, max: 100, message: "阈值范围为 1-100 票", trigger: ["blur", "change"] },
  ],
};
// 已保存快照：与当前表单对比得出「未保存修改」状态
const savedGlobalPollSnapshot = ref("");
const globalPollDirty = computed(() => JSON.stringify(globalPoll) !== savedGlobalPollSnapshot.value);
const savingGlobalPoll = ref(false);

// ===== 自动投票 =====
const autoPollEnabled = ref(false);
const autoPollChannelSettings = ref<Record<string, { enabled?: boolean }>>({});
const channels = ref<string[]>([]);
const savingAutoPollChannels = reactive(new Set<string>());
const autoPollToggling = ref(false);

// ===== 评论区欢迎 =====
const savingWelcome = ref(false);
const savingWelcomeChannels = reactive(new Set<string>());
const welcomeOverrides = ref<Record<string, { enabled?: boolean }>>({});
const welcomeConfig = reactive({
  enabled: true,
  welcome_message: "",
  button_text: "",
  button_action: "request_summary",
});
const welcomeFormRef = ref<FormInst | null>(null);
// 启用时欢迎消息与按钮文本为必填；禁用时不拦截保存
const welcomeRules: FormRules = {
  welcome_message: [
    {
      required: true,
      trigger: ["input", "blur"],
      validator: (_rule, value: string | null) =>
        !welcomeConfig.enabled || (typeof value === "string" && value.trim().length > 0)
          ? true
          : new Error("启用后需填写欢迎消息"),
    },
  ],
  button_text: [
    {
      required: true,
      trigger: ["input", "blur"],
      validator: (_rule, value: string | null) =>
        !welcomeConfig.enabled || (typeof value === "string" && value.trim().length > 0)
          ? true
          : new Error("启用后需填写按钮文本"),
    },
  ],
};
const savedWelcomeSnapshot = ref("");
const welcomeDirty = computed(() => JSON.stringify(welcomeConfig) !== savedWelcomeSnapshot.value);

// ===== 派生数据 =====
const availablePollChannels = computed(() =>
  [...new Set(channels.value)].filter((channel) => !(channel in pollSettings.value.settings)),
);
const pollChannelOptions = computed(() =>
  (editingPollChannel.value ? [editingPollChannel.value] : availablePollChannels.value).map(
    (channel) => ({ label: getChannelName(channel), value: channel }),
  ),
);
// 「继承全局」选项文案动态展示当前全局值，降低理解成本
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

// ===== 数据加载（含三态处理） =====
async function loadData(isRefresh = false) {
  if (isRefresh) {
    refreshing.value = true;
  } else {
    initialLoading.value = true;
  }
  loadError.value = false;
  try {
    // 提示词加载失败不阻塞页面（保持旧版容错行为），其余四项任一失败即进入错误态
    const [pollRes, autoPollRes, welcomeRes, channelsRes, promptRes] = await Promise.all([
      getPollSettings(),
      getAutoPollSettings(),
      getCommentWelcomeConfig(),
      getInteractionChannels(),
      getPrompt("poll").catch(() => ({ success: false, data: { content: "" } })),
    ]);

    let failed = false;
    let failMsg = "加载数据失败";

    if (pollRes.success) {
      pollSettings.value = { settings: pollRes.data.settings || {} };
      globalPoll.enable_poll = pollRes.data.enable_poll ?? true;
      globalPoll.poll_regen_threshold = pollRes.data.poll_regen_threshold ?? 5;
      globalPoll.public_voters = pollRes.data.public_voters ?? true;
      globalPoll.enable_vote_regen_request = pollRes.data.enable_vote_regen_request ?? true;
    } else {
      failed = true;
      failMsg = pollRes.message || failMsg;
    }

    if (autoPollRes.success) {
      autoPollEnabled.value = autoPollRes.data.enabled;
      autoPollChannelSettings.value = autoPollRes.data.channel_settings;
    } else {
      failed = true;
      failMsg = autoPollRes.message || failMsg;
    }

    if (welcomeRes.success) {
      const def = welcomeRes.data.default || {};
      welcomeConfig.enabled = def.enabled ?? true;
      welcomeConfig.welcome_message = def.welcome_message ?? "";
      welcomeConfig.button_text = def.button_text ?? "";
      welcomeConfig.button_action = def.button_action ?? "request_summary";
      welcomeOverrides.value = welcomeRes.data.channel_overrides || {};
    } else {
      failed = true;
      failMsg = welcomeRes.message || failMsg;
    }

    if (channelsRes.success) {
      channels.value = channelsRes.data.channels || [];
    } else {
      failed = true;
      failMsg = channelsRes.message || failMsg;
    }

    if (promptRes.success) {
      pollPrompt.value = promptRes.data.content || "";
      lastSavedPrompt.value = pollPrompt.value;
    } else {
      message.warning("投票提示词加载失败，请稍后刷新重试");
    }

    if (failed) {
      loadError.value = true;
      message.error(failMsg);
      return;
    }

    // 加载成功后记录快照，作为「未保存修改」的基准
    savedGlobalPollSnapshot.value = JSON.stringify(globalPoll);
    savedWelcomeSnapshot.value = JSON.stringify(welcomeConfig);
  } catch {
    loadError.value = true;
    message.error("加载数据失败");
  } finally {
    initialLoading.value = false;
    refreshing.value = false;
  }
}

function handleRefresh() {
  void loadData(true);
}

function handleRetry() {
  void loadData();
}

// ===== 投票设置：全局表单（分区显式保存，替代旧版防抖静默保存） =====
async function saveGlobalPollSettings() {
  if (globalPollFormRef.value) {
    try {
      await globalPollFormRef.value.validate();
    } catch {
      return;
    }
  }
  savingGlobalPoll.value = true;
  try {
    const res = await updateGlobalPollSettings({ ...globalPoll });
    if (res.success) {
      savedGlobalPollSnapshot.value = JSON.stringify(globalPoll);
      message.success("全局投票设置已保存");
    } else {
      message.error(res.message);
    }
  } catch {
    message.error("保存失败");
  } finally {
    savingGlobalPoll.value = false;
  }
}

// ===== 投票设置：提示词 =====
async function savePollPrompt() {
  savingPrompt.value = true;
  try {
    const res = await updatePrompt("poll", pollPrompt.value);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) lastSavedPrompt.value = pollPrompt.value;
  } catch {
    message.error("保存提示词失败");
  } finally {
    savingPrompt.value = false;
  }
}

async function resetPollPrompt() {
  try {
    const res = await resetPrompt("poll");
    if (res.success) {
      pollPrompt.value = res.data?.content || "";
      lastSavedPrompt.value = pollPrompt.value;
      message.success("已恢复默认提示词");
    } else {
      message.error(res.message);
    }
  } catch {
    message.error("恢复失败");
  }
}

// ===== 投票设置：频道配置 =====
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
  if (!channelPollForm.channel || savingChannelPoll.value) return;
  if (channelPollFormRef.value) {
    try {
      await channelPollFormRef.value.validate();
    } catch {
      return;
    }
  }
  savingChannelPoll.value = true;
  try {
    // 「继承全局」映射为 null，由后端清除该项覆盖
    const saved = await saveChannelPoll(channelPollForm.channel, {
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

async function deleteChannelPoll(channel: string) {
  try {
    const res = await deletePollSettings(channel);
    message[res.success ? "success" : "error"](res.message);
    if (res.success) {
      // 仅本地移除该行，避免整页刷新丢失其他分区未保存的编辑
      const settings = { ...pollSettings.value.settings };
      delete settings[channel];
      pollSettings.value = { ...pollSettings.value, settings };
    }
  } catch {
    message.error("删除失败");
  }
}

// ===== 自动投票 =====
async function handleAutoPollToggle(enabled: boolean) {
  if (autoPollToggling.value) return;
  autoPollToggling.value = true;
  // 乐观更新，失败回滚
  autoPollEnabled.value = enabled;
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
  } finally {
    autoPollToggling.value = false;
  }
}

function getAutoPollChannelEnabled(channel: string): boolean {
  if (!autoPollEnabled.value) return false;
  const cfg = autoPollChannelSettings.value[channel];
  if (cfg) return !!cfg.enabled;
  return true;
}

// 状态药丸四态：与旧版标签语义一一对应
type PillTone = "success" | "info" | "warning" | "neutral";
function getAutoPollChannelStatus(channel: string): { label: string; tone: PillTone } {
  if (!autoPollEnabled.value) {
    return { label: "全局已关闭", tone: "neutral" };
  }
  const cfg = autoPollChannelSettings.value[channel];
  if (!cfg) {
    return { label: "继承全局", tone: "success" };
  }
  return cfg.enabled ? { label: "独立开启", tone: "info" } : { label: "独立禁用", tone: "warning" };
}

async function saveChannelAutoPoll(channel: string, enabled: boolean) {
  if (!autoPollEnabled.value || savingAutoPollChannels.has(channel)) return;
  savingAutoPollChannels.add(channel);
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
    // 请求异常时回滚到尝试前的本地状态，保留其他分区未保存的编辑
    autoPollChannelSettings.value = previousSettings;
    message.error("保存失败");
  } finally {
    savingAutoPollChannels.delete(channel);
  }
}

async function deleteChannelAutoPollSetting(channel: string) {
  if (!autoPollEnabled.value || savingAutoPollChannels.has(channel)) return;
  savingAutoPollChannels.add(channel);
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
    message.error("操作失败");
  } finally {
    savingAutoPollChannels.delete(channel);
  }
}

// ===== 评论区欢迎 =====
async function saveWelcomeConfig() {
  if (welcomeFormRef.value) {
    try {
      await welcomeFormRef.value.validate();
    } catch {
      return;
    }
  }
  savingWelcome.value = true;
  try {
    const res = await updateDefaultCommentWelcome({ ...welcomeConfig });
    message[res.success ? "success" : "error"](res.message);
    if (res.success) savedWelcomeSnapshot.value = JSON.stringify(welcomeConfig);
  } catch {
    message.error("保存失败");
  } finally {
    savingWelcome.value = false;
  }
}

async function toggleChannelWelcome(channel: string, enabled: boolean) {
  if (savingWelcomeChannels.has(channel)) return;
  savingWelcomeChannels.add(channel);
  try {
    // 与旧版保持一致：沿用默认配置接口携带 { enabled }，不改动后端契约
    const res = await updateDefaultCommentWelcome({ enabled });
    if (res.success) {
      const newOverrides = { ...welcomeOverrides.value };
      newOverrides[channel] = { ...(newOverrides[channel] || {}), enabled };
      welcomeOverrides.value = newOverrides;
    } else {
      message.error(res.message || "更新失败");
    }
  } catch {
    message.error("更新失败");
  } finally {
    savingWelcomeChannels.delete(channel);
  }
}

function deleteChannelWelcomeOverride(channel: string) {
  // 与旧版一致：删除覆盖仅为本地状态更新，刷新后恢复显示
  const newOverrides = { ...welcomeOverrides.value };
  delete newOverrides[channel];
  welcomeOverrides.value = newOverrides;
  message.success("已删除频道覆盖配置");
}

// ===== 表格行数据与列定义 =====
interface PollChannelRow {
  channel: string;
  setting: ChannelPollSettings;
}
interface AutoPollRow {
  channel: string;
}
interface WelcomeOverrideRow {
  channel: string;
  enabled: boolean | undefined;
}

const pollChannelRows = computed<PollChannelRow[]>(() =>
  Object.entries(pollSettings.value.settings).map(([channel, setting]) => ({
    channel,
    setting: setting || {},
  })),
);
const autoPollRows = computed<AutoPollRow[]>(() => channels.value.map((channel) => ({ channel })));
const welcomeOverrideRows = computed<WelcomeOverrideRow[]>(() =>
  Object.entries(welcomeOverrides.value || {}).map(([channel, cfg]) => ({
    channel,
    enabled: cfg?.enabled,
  })),
);

function pollRowKey(row: PollChannelRow) {
  return row.channel;
}
function autoPollRowKey(row: AutoPollRow) {
  return row.channel;
}
function welcomeRowKey(row: WelcomeOverrideRow) {
  return row.channel;
}

// 状态药丸：soft 底 + 语义前景 + 圆点，深浅主题均走 --sb-* 令牌
function renderPill(label: string, tone: PillTone) {
  return h(
    "span",
    { class: `sb-pill sb-pill--${tone}` },
    { default: () => [h("span", { class: "sb-pill-dot" }), label] },
  );
}

function renderChannelTag(name: string) {
  return h(NTag, { size: "small", bordered: false }, { default: () => name });
}

// 行内投票开关：null 时回退全局值并提示「继承全局」
function renderPollChannelSwitch(row: PollChannelRow, field: "enabled" | "send_to_channel" | "public_voters") {
  const raw = row.setting[field];
  const value =
    raw != null
      ? raw
      : field === "enabled"
        ? globalPoll.enable_poll
        : field === "public_voters"
          ? globalPoll.public_voters
          : false;
  const children = [
    h(NSwitch, {
      size: "small",
      value,
      disabled: savingPollChannels.has(row.channel),
      loading: savingPollChannels.has(row.channel),
      // 载荷必须取切换后的新值 v，而不是渲染时的旧值
      onUpdateValue: (v: boolean) => {
        const data: ChannelPollSettings =
          field === "enabled"
            ? { enabled: v }
            : field === "send_to_channel"
              ? { send_to_channel: v }
              : { public_voters: v };
        void saveChannelPoll(row.channel, data);
      },
    }),
    // 发送到频道无全局继承语义，不显示提示
    raw == null && field !== "send_to_channel"
      ? h(NText, { depth: 3, class: "cell-hint" }, { default: () => "继承全局" })
      : null,
  ];
  return h("div", { class: "cell-switch" }, children);
}

const pollChannelColumns = computed<DataTableColumns<PollChannelRow>>(() => [
  {
    title: "频道",
    key: "channel",
    minWidth: 160,
    ellipsis: { tooltip: true },
    render: (row) => renderChannelTag(getChannelName(row.channel)),
  },
  {
    title: "投票",
    key: "enabled",
    width: 160,
    render: (row) => renderPollChannelSwitch(row, "enabled"),
  },
  {
    title: "发送到频道",
    key: "send_to_channel",
    width: 130,
    render: (row) => renderPollChannelSwitch(row, "send_to_channel"),
  },
  {
    title: "公开投票",
    key: "public_voters",
    width: 160,
    render: (row) => renderPollChannelSwitch(row, "public_voters"),
  },
  {
    title: "操作",
    key: "actions",
    width: 120,
    align: "right",
    render: (row) =>
      h("div", { class: "cell-actions" }, [
        h(
          NButton,
          {
            size: "small",
            quaternary: true,
            disabled: savingPollChannels.has(row.channel),
            onClick: () => openChannelPoll(row.channel),
          },
          { default: () => "编辑" },
        ),
        h(NPopconfirm, { onPositiveClick: () => deleteChannelPoll(row.channel) }, {
          trigger: () =>
            h(
              NButton,
              { size: "small", quaternary: true, type: "error", disabled: savingPollChannels.has(row.channel) },
              { default: () => "删除" },
            ),
          default: () => `删除后 ${getChannelName(row.channel)} 将恢复全局默认投票设置，确定删除？`,
        }),
      ]),
  },
]);

const autoPollColumns = computed<DataTableColumns<AutoPollRow>>(() => [
  {
    title: "频道",
    key: "channel",
    minWidth: 160,
    ellipsis: { tooltip: true },
    render: (row) => renderChannelTag(getChannelName(row.channel)),
  },
  {
    title: "自动投票",
    key: "enabled",
    width: 150,
    render: (row) =>
      h(NSwitch, {
        size: "small",
        value: getAutoPollChannelEnabled(row.channel),
        disabled: !autoPollEnabled.value || savingAutoPollChannels.has(row.channel),
        loading: savingAutoPollChannels.has(row.channel),
        onUpdateValue: (v: boolean) => {
          void saveChannelAutoPoll(row.channel, v);
        },
      }),
  },
  {
    title: "状态",
    key: "status",
    width: 120,
    render: (row) => {
      const status = getAutoPollChannelStatus(row.channel);
      return renderPill(status.label, status.tone);
    },
  },
  {
    title: "操作",
    key: "actions",
    width: 90,
    align: "right",
    render: (row) =>
      h(NPopconfirm, { onPositiveClick: () => deleteChannelAutoPollSetting(row.channel) }, {
        trigger: () =>
          h(
            NButton,
            {
              size: "small",
              quaternary: true,
              type: "error",
              disabled: !autoPollEnabled.value || !autoPollChannelSettings.value[row.channel],
            },
            { default: () => "重置" },
          ),
        default: () => `重置后 ${getChannelName(row.channel)} 将恢复跟随全局设置，确定继续？`,
      }),
  },
]);

const welcomeColumns = computed<DataTableColumns<WelcomeOverrideRow>>(() => [
  {
    title: "频道",
    key: "channel",
    minWidth: 160,
    ellipsis: { tooltip: true },
    render: (row) => renderChannelTag(getChannelName(row.channel)),
  },
  {
    title: "启用",
    key: "enabled",
    width: 100,
    render: (row) =>
      h(NSwitch, {
        size: "small",
        value: row.enabled ?? false,
        disabled: savingWelcomeChannels.has(row.channel),
        loading: savingWelcomeChannels.has(row.channel),
        onUpdateValue: (v: boolean) => {
          void toggleChannelWelcome(row.channel, v);
        },
      }),
  },
  {
    title: "操作",
    key: "actions",
    width: 90,
    align: "right",
    render: (row) =>
      h(NPopconfirm, { onPositiveClick: () => deleteChannelWelcomeOverride(row.channel) }, {
        trigger: () =>
          h(
            NButton,
            { size: "small", quaternary: true, type: "error" },
            { default: () => "删除" },
          ),
        default: () => `删除后 ${getChannelName(row.channel)} 将恢复使用默认欢迎配置，确定删除？`,
      }),
  },
]);

onMounted(() => {
  void loadData();
});
</script>

<style scoped>
.interaction-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 统一页头：标题 + 副标题 + 右侧主操作，窄屏允许换行 */
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.page-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  line-height: 1.4;
  color: var(--sb-text);
}

.page-subtitle {
  margin: 2px 0 0;
  font-size: 13px;
  color: var(--sb-text-2);
}

.page-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 首屏骨架屏 */
.page-skeleton {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-top: 4px;
}

.load-error {
  padding: 48px 0;
}

/* 页签内的分区卡片纵向排布 */
.tab-sections {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 卡片一句话说明 */
.card-desc {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--sb-text-2);
}

/* 字段：控件 + 帮助文案（配合 label-placement="top"，窄屏自然全宽） */
.field-control {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}

.field-help {
  font-size: 12px;
  line-height: 1.5;
  color: var(--sb-text-3);
}

/* 全局设置双列栅格，≤760px 收为单列 */
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 32px;
}

/* 分区保存 footer */
.card-footer {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
}

.dirty-hint {
  font-size: 12px;
  color: var(--sb-text-3);
}

.threshold-input {
  width: 180px;
}

/* 状态药丸：soft 底 + 语义前景 + 小圆点，覆盖模板与表格渲染两类节点 */
.interaction-page :deep(.sb-pill) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 10px;
  border-radius: var(--sb-radius-pill);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  white-space: nowrap;
}

.interaction-page :deep(.sb-pill-dot) {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}

.interaction-page :deep(.sb-pill--success) {
  background: var(--sb-success-soft);
  color: var(--sb-success);
}

.interaction-page :deep(.sb-pill--info) {
  background: var(--sb-accent-soft);
  color: var(--sb-accent);
}

.interaction-page :deep(.sb-pill--warning) {
  background: var(--sb-warning-soft);
  color: var(--sb-warning);
}

.interaction-page :deep(.sb-pill--neutral) {
  background: var(--sb-surface-2);
  color: var(--sb-text-2);
}

/* 数据表格单元格（渲染函数生成，经 :deep 命中） */
.interaction-page :deep(.cell-switch) {
  display: flex;
  align-items: center;
  gap: 8px;
}

.interaction-page :deep(.cell-hint) {
  font-size: 12px;
}

.interaction-page :deep(.cell-actions) {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
}

/* 弹窗底部：左取消右保存 */
.modal-footer {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.modal-hint {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: var(--sb-text-3);
}

@media (max-width: 760px) {
  .form-grid {
    grid-template-columns: 1fr;
  }

  .threshold-input {
    width: 100%;
  }

  /* 窄屏弹窗按钮纵向堆叠，主操作在上更易触达 */
  .modal-footer {
    flex-direction: column-reverse;
  }

  .modal-footer .n-button {
    width: 100%;
  }
}
</style>
