<template>
  <n-space vertical :size="20">
    <n-card title="Sakura Guard · 樱花智能群管">
      <n-alert type="info" :bordered="false">
        每个群独立启用，默认关闭。主 Bot 需要删除消息、限制/封禁成员权限；自动批准入群还需要邀请用户权限。
        昵称会在入群、申请、发言和编辑消息时检查。验证通过恢复原权限，解封不会自动拉人回群。
      </n-alert>
    </n-card>

    <bot-chats-panel @changed="loadJoinedGroups()" />

    <n-card title="群组设置" :segmented="{ content: true }">
      <n-space align="center" class="toolbar">
        <n-select v-model:value="selectedId" :options="groupOptions" :disabled="loading || adding" placeholder="选择受管群组" style="width: 280px" />
        <n-select v-model:value="joinedChatId" :options="joinedOptions" :loading="discovering" filterable clearable placeholder="从已加入的群组选择" style="width: 350px" @update:value="chooseJoinedGroup" />
        <n-input v-model:value="newChatId" placeholder="新群 Telegram ID，如 -1001234567890" style="width: 310px" />
        <n-button :loading="adding" :disabled="loading" @click="addGroup">添加群组</n-button>
        <n-button :loading="discovering" @click="loadJoinedGroups(true)">刷新已加入群组</n-button>
        <n-button :loading="loading" :disabled="adding" @click="reload">刷新</n-button>
      </n-space>
      <n-text depth="3">添加后立即保存，默认关闭群管；群名自动解析。{{ discovering ? `正在核实已加入群组 ${discoveryChecked}/${discoveryTotal}。` : '' }}
        列表来自主 Bot 会话与群事件；未出现的群可发送一条群消息后刷新，或直接输入群 ID。
      </n-text>
      <n-form v-if="form" label-placement="top" class="settings" :disabled="loading || adding" @focusout="flushGroup" @compositionstart="startComposition" @compositionend="endComposition">
        <n-space class="toolbar" align="center">
          <n-text depth="3">所有设置自动保存。文字与数字输入暂停片刻或离开输入框后保存；规则填写完整后生效。</n-text>
          <n-text role="status" aria-live="polite">{{ composing ? '正在输入，完成后自动保存…' : autosaveStatus(currentEditor) }}</n-text>
        </n-space>
        <n-alert v-if="groupIssues.length" type="warning" :bordered="false" class="toolbar">
          <div v-for="issue in groupIssues" :key="issue.field">{{ issue.label }}：{{ issue.error }}</div>
          <n-button v-if="groupIssues.some(issue => issue.status === 'error')" size="small" @click="currentEditor?.flush(true)">重试自动保存</n-button>
        </n-alert>
        <div class="switch-grid">
          <n-form-item v-for="field in switchFields" :key="field.key" :label="field.label">
            <n-switch v-model:value="form[field.key]" :aria-label="field.label" :loading="currentEditor?.active.value === field.key" :disabled="loading || adding || currentEditor?.active.value === field.key" />
          </n-form-item>
        </div>
        <n-alert type="info" :bordered="false">已开始的验证仅在本人答对或管理员点击“跳过验证”后恢复权限。超时或答错后的警告、禁言及等待人工处理均保留验证禁言；关闭开关或加入白名单也不会自动解禁正在验证的成员。</n-alert>
        <div class="switch-grid">
          <n-form-item label="验证超时（秒）"><n-input-number v-model:value="form.verification_timeout" :min="30" :max="600" /></n-form-item>
          <n-form-item label="最多答错次数"><n-input-number v-model:value="form.verification_max_attempts" :min="1" :max="10" /></n-form-item>
          <n-form-item label="入群申请流程">
            <n-select v-model:value="form.join_requests" :options="joinOptions" />
          </n-form-item>
          <n-form-item label="入群申请命中规则后的动作"><n-select v-model:value="form.join_request_match_action" :options="requestMatchActions" /></n-form-item>
          <n-form-item label="验证超时后的动作"><n-select v-model:value="form.verification_timeout_action" :options="verificationActions" /></n-form-item>
          <n-form-item label="答错达到上限后的动作"><n-select v-model:value="form.verification_wrong_action" :options="verificationActions" /></n-form-item>
          <n-form-item label="验证结束后的消息处理"><n-select v-model:value="form.verification_message_mode" :options="verificationMessageModes" /></n-form-item>
          <n-form-item label="验证管理按钮（群内及通知）"><n-select v-model:value="form.verification_admin_actions" multiple clearable :options="verificationAdminActions" placeholder="清空后隐藏全部管理按钮" /></n-form-item>
          <n-form-item label="昵称及用户名关键词命中动作"><n-select v-model:value="form.nickname_action" :options="memberActions" /></n-form-item>
          <n-form-item label="机器人发言命中动作"><n-select v-model:value="form.bot_message_action" :options="actions" /></n-form-item>
          <n-form-item label="消息关键词命中动作"><n-select v-model:value="form.message_action" :options="actions" /></n-form-item>
          <n-form-item label="禁言时长（秒）"><n-input-number v-model:value="form.mute_seconds" :min="30" :max="2592000" /></n-form-item>
        </div>
        <n-form-item label="昵称关键词（包括用户名；动作见上方配置）"><n-dynamic-tags v-model:value="form.nickname_keywords" /></n-form-item>
        <n-form-item label="消息与链接关键词"><n-dynamic-tags v-model:value="form.message_keywords" /></n-form-item>
        <div class="switch-grid">
          <n-form-item v-for="field in idFields" :key="field.key" :label="field.label">
            <n-dynamic-tags :value="form[field.key].map(String)" @update:value="(values: string[]) => setIds(field.key, values)" />
          </n-form-item>
          <n-form-item label="审核群 ID（私聊失败的备选；留空禁用）"><n-input-number v-model:value="form.review_chat_id" :max="-1" :show-button="false" /></n-form-item>
        </div>
        <n-form-item label="通知事件（默认全部通知管理员）"><n-select v-model:value="form.notification_events" multiple clearable :options="notificationEvents" /></n-form-item>
        <n-text depth="3">通知收件人留空时复用 REPORT_ADMIN_IDS；可按事件关闭通知。验证管理按钮仅群主、具有封禁成员权限的管理员及 REPORT_ADMIN_IDS 可操作。跳过验证不会加入白名单或解除其他规则的限制。</n-text>
        <n-alert type="warning" :bordered="false" class="toolbar">
          通知目标不自动获得审核权限。审核用户列表为空时复用 REPORT_ADMIN_IDS；其他群只在启用共享后拦截永久黑名单。
          机器人发言处理独立于消息关键词过滤；管理员、受信任 Bot 和本群白名单继续豁免，原有昵称及共享黑名单规则仍生效。
          要接收其他机器人的普通消息，需在 BotFather 为主 Bot 开启 Bot-to-Bot Communication Mode，并确保主 Bot 是群管理员。
        </n-alert>
        <n-divider title-placement="left">自定义规则（按顺序匹配）</n-divider>
        <n-space v-for="(rule, index) in form.rules" :key="index" align="center" class="rule-row">
          <n-input v-model:value="rule.id" placeholder="唯一规则 ID" style="width: 150px" />
          <n-select v-model:value="rule.target" :options="targets" style="width: 130px" />
          <n-select v-model:value="rule.kind" :options="kinds" style="width: 110px" />
          <n-input v-model:value="rule.value" placeholder="关键词 / 正则表达式" style="width: 240px" />
          <n-select v-model:value="rule.action" :options="rule.target === 'nickname' || rule.target === 'username' ? nicknameRuleActions : actions" style="width: 180px" />
          <n-button :disabled="index === 0" @click="moveRule(index, -1)">上移</n-button>
          <n-button :disabled="index === form.rules.length - 1" @click="moveRule(index, 1)">下移</n-button>
          <n-button type="error" tertiary @click="form.rules.splice(index, 1)">删除</n-button>
        </n-space>
        <n-space class="toolbar">
          <n-button @click="addRule">添加规则</n-button>
        </n-space>
        <n-text depth="3">正则匹配归一化后的文本，限时执行。昵称与用户名规则可指定动作，或跟随本群昵称命中动作。</n-text>
      </n-form>
      <n-empty v-else description="选择已加入的群组或输入群 ID，点击添加后立即保存" />
    </n-card>

    <n-card title="案件、名单与处罚日志">
      <n-space class="toolbar">
        <n-select v-model:value="table" :options="tables" style="width: 190px" @update:value="changeTable" />
        <n-input v-model:value="filterChat" placeholder="群组 ID（可选）" style="width: 190px" />
        <n-input v-model:value="filterUser" placeholder="用户 ID（可选）" style="width: 180px" />
        <n-select v-model:value="filterStatus" :options="statuses" clearable placeholder="审核/执行状态" style="width: 200px" />
        <n-button :loading="recordsLoading" @click="resetRecords">查询 / 刷新</n-button>
      </n-space>
      <n-space v-if="table === 'whitelist'" class="toolbar" align="center">
        <n-input v-model:value="whitelistChat" placeholder="白名单群组 ID" style="width: 200px" />
        <n-input v-model:value="whitelistUser" placeholder="用户 ID" style="width: 180px" />
        <n-button @click="addWhitelist">添加本群白名单</n-button>
        <n-text depth="3">白名单不会解除已有封禁；请通过案件审核解封。</n-text>
      </n-space>
      <n-data-table :columns="columns" :data="records" :loading="recordsLoading" :scroll-x="1150" :bordered="false" />
      <n-space justify="end" class="toolbar">
        <n-pagination v-model:page="page" :page-size="50" :item-count="total" @update:page="loadRecords" />
      </n-space>
    </n-card>

    <n-card title="审计留存">
      <n-space v-if="retentionEditor" align="center">
        <n-text>保留天数</n-text><n-input-number v-model:value="retentionEditor.values.retention_days" :disabled="loading" :min="7" :max="3650" @blur="retentionEditor?.flush()" />
        <n-text role="status" aria-live="polite">{{ autosaveStatus(retentionEditor) }}</n-text>
        <n-text v-if="retentionEditor.states.retention_days.error" type="warning">{{ retentionEditor.states.retention_days.error }}</n-text>
        <n-button v-if="retentionEditor.states.retention_days.status === 'error'" size="small" @click="retentionEditor.flush(true)">重试自动保存</n-button>
        <n-text depth="3">仅清理过期的已结束案件和验证；永久名单、待审核和恢复中的操作持续保留。</n-text>
      </n-space>
    </n-card>

    <n-modal v-model:show="showDetail" preset="card" title="案件与证据" style="width: min(760px, 95vw)">
      <n-descriptions v-if="detail" :column="1" bordered label-placement="left">
        <n-descriptions-item label="案件">{{ detail.id || detail.case_id }}</n-descriptions-item>
        <n-descriptions-item label="群组 / 用户">{{ detail.chat_id || detail.source_chat_id }} / {{ detail.user_id }}</n-descriptions-item>
        <n-descriptions-item label="封禁快照">{{ detail.payload?.name }} · @{{ detail.payload?.username || '—' }}</n-descriptions-item>
        <n-descriptions-item label="规则 / 原因">{{ detail.payload?.evidence?.rule_id }} · {{ detail.payload?.evidence?.reason }}</n-descriptions-item>
        <n-descriptions-item label="命中原文"><span class="evidence">{{ detail.payload?.evidence?.original }}</span></n-descriptions-item>
        <n-descriptions-item label="归一化结果"><span class="evidence">{{ detail.payload?.evidence?.normalized }}</span></n-descriptions-item>
        <n-descriptions-item label="消息证据">
          <a v-if="evidenceUrl(detail)" :href="evidenceUrl(detail)" target="_blank" rel="noopener noreferrer">打开 Telegram 消息</a>
          <span v-else>{{ detail.payload?.message_id || '—' }}</span>
        </n-descriptions-item>
        <n-descriptions-item label="状态">{{ statusLabel(detail.status || detail.synced_status) }}</n-descriptions-item>
        <n-descriptions-item label="审核人 / UTC 时间">{{ detail.reviewer_actor || detail.reviewer_id || '—' }} / {{ detail.reviewed_at || '—' }}</n-descriptions-item>
        <n-descriptions-item label="错误">{{ detail.error || '—' }}</n-descriptions-item>
        <n-descriptions-item v-if="detail.message_cleanup_pending !== undefined" label="验证消息清理">{{ detail.message_cleanup_pending ? '待清理，后台自动重试' : '已清理或当前无需清理' }}</n-descriptions-item>
      </n-descriptions>
    </n-modal>
  </n-space>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NDivider,
  NDynamicTags, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal,
  NPagination, NSelect, NSpace, NSwitch, NText, useDialog, useMessage,
  type DataTableColumns,
} from "naive-ui";
import {
  addGuardGroup, getGuardJoinedGroups, getGuardConfig, getGuardRecords, removeGuardBlacklist, retryGuardCase,
  reviewGuardCase, saveGuardSettings, saveGuardRetention, setGuardWhitelist,
  type GuardChat, type GuardGroup, type GuardRecord, type GuardTable,
} from "@/api/guard";
import { createAutosave, type Autosave, saveError } from "@/composables/autosave";
import { guardLabels, guardSwitches, immediateGuardFields, integerError, validateGuardField } from "@/composables/guard-form";
import BotChatsPanel from "@/components/BotChatsPanel.vue";

const message = useMessage();
const dialog = useDialog();
const loading = ref(false), recordsLoading = ref(false), adding = ref(false), composing = ref(false);
const switchFields = guardSwitches.map(key => ({ key, label: guardLabels[key] }));
const editors = new Map<string, Autosave<GuardGroup>>();
const retentionEditor = shallowRef<Autosave<{ retention_days: number }> | null>(null);
let composingEditor: Autosave<GuardGroup> | undefined;
const groups = ref<Record<string, GuardGroup>>({});
const groupMetadata = ref<Record<string, GuardChat>>({}), joinedGroups = ref<GuardChat[]>([]);
const joinedChatId = ref<number | null>(null), discovering = ref(false);
const discoveryChecked = ref(0), discoveryTotal = ref(0);
let discoveryTimer: ReturnType<typeof setTimeout> | null = null;
let disposed = false, discoveryRequest = 0;
const selectedId = ref<string | null>(null), newChatId = ref("");
const form = computed(() => selectedId.value ? groups.value[selectedId.value] : undefined);
const currentEditor = computed(() => form.value && selectedId.value ? editors.get(selectedId.value) : undefined);
const groupIssues = computed(() => currentEditor.value ? (Object.entries(currentEditor.value.states) as [keyof GuardGroup, { status: string; error?: string }][]).filter(([, state]) => state.error).map(([field, state]) => ({ field, label: guardLabels[field], ...state })) : []);
const groupOptions = computed(() => Object.entries(groups.value).map(([value, group]) => ({ label: `${groupMetadata.value[value]?.title || '群名待解析'}（${value}）· ${group.enabled ? '已启用' : '未启用'}${editors.get(value)?.hasUnsaved() ? ' · 未保存' : ''}`, value })));
const joinedOptions = computed(() => joinedGroups.value.map(group => ({ label: `${group.title || '未命名群组'}（${group.chat_id}）${groups.value[String(group.chat_id)] ? ' · 已添加' : ''}`, value: group.chat_id })));
const table = ref<GuardTable>("cases"), records = ref<GuardRecord[]>([]);
const filterChat = ref(""), filterUser = ref(""), filterStatus = ref<string | null>("pending");
const whitelistChat = ref(""), whitelistUser = ref("");
const page = ref(1), total = ref(0);
const detail = ref<GuardRecord | null>(null), showDetail = ref(false);
const actions = [{ label: "删除消息", value: "delete" }, { label: "警告", value: "warn" }, { label: "禁言", value: "mute" }, { label: "踢出", value: "kick" }, { label: "封禁并通知审核", value: "ban" }];
const memberActions = actions.filter(action => action.value !== "delete");
const nicknameRuleActions = [{ label: "跟随昵称命中动作", value: "delete" }, ...memberActions];
const verificationActions = [{ label: "封禁并通知审核", value: "ban" }, { label: "踢出（可再加入）", value: "kick" }, { label: "保持验证禁言，等待管理员", value: "mute" }, { label: "警告并保持验证禁言", value: "warn" }, { label: "等待管理员处理（保持禁言）", value: "allow" }];
const verificationMessageModes = [{ label: "删除验证消息（默认）", value: "delete" }, { label: "保留处理结果，移除所有按钮", value: "result" }];
const verificationAdminActions = [{ label: "一键封禁", value: "ban" }, { label: "封禁并加入黑名单", value: "blacklist" }, { label: "跳过验证", value: "skip" }];
const requestMatchActions = [{ label: "拒绝申请（默认）", value: "reject" }, { label: "保持待审，通知管理员", value: "manual" }, { label: "批准后继续按入群规则处理", value: "approve" }];
const notificationEvents = [{ label: "过滤及处罚", value: "moderation" }, { label: "验证开始", value: "verification_start" }, { label: "验证通过 / 取消", value: "verification_end" }, { label: "验证失败处理", value: "verification_fail" }, { label: "管理员验证操作", value: "admin_action" }, { label: "入群申请处理", value: "join_request" }, { label: "白名单操作", value: "whitelist" }, { label: "黑名单操作", value: "blacklist" }];
const recordActionNames: Record<string, string> = { verify_skip: "管理员跳过验证", verify_hold: "保持验证禁言，等待管理员", verify_allow: "历史配置放行", verify_start: "开始入群验证", verify_pass: "入群验证通过", verify_cancel: "结束 / 取消验证", whitelist_add: "添加白名单", whitelist_remove: "移除白名单", blacklist_remove: "撤销永久黑名单", approve: "批准入群申请", reject: "拒绝入群申请", manual: "保留申请待审" };
const joinOptions = [{ label: "保持待审，由管理员处理", value: "manual" }, { label: "规则通过后批准；开启验证时入群后验证", value: "approve" }];
const targets = [{ label: "昵称", value: "nickname" }, { label: "用户名", value: "username" }, { label: "消息 / 媒体说明", value: "message" }, { label: "链接", value: "link" }];
const kinds = [{ label: "关键词", value: "keyword" }, { label: "正则", value: "regex" }];
const tables = [{ label: "案件 / 处罚日志", value: "cases" }, { label: "本群白名单", value: "whitelist" }, { label: "全局永久黑名单", value: "blacklist" }, { label: "入群验证 / 错误", value: "verifications" }, { label: "通知投递 / 错误", value: "notifications" }];
const statusNames: Record<string, string> = { pending: "已封禁 · 待审核", executing: "执行中", execution_failed: "执行失败", reviewing_allow: "解封恢复中", reviewing_blacklist: "拉黑恢复中", allowed: "已解封并加入白名单", blacklisted: "已永久拉黑", done: "已完成", cancelled: "已取消", manual: "申请待人工处理", preparing: "准备验证", waiting: "等待作答", passing: "恢复权限中", kicking: "踢出恢复中", cancelling: "取消验证中", passed: "验证通过", expired: "验证超时 / 已踢出", left: "成员已离开", blocked: "已被规则拦截", permanent: "永久拉黑" };
Object.assign(statusNames, { admin_pending: "管理员操作恢复中", admin_banned: "管理员已封禁", admin_blacklisted: "管理员已封禁并拉黑", admin_skipped: "管理员已跳过验证", failed_banned: "验证失败 / 已封禁", kicking: "验证封禁恢复中" });
Object.assign(statusNames, { failed_kicked: "验证失败 / 已踢出", failed_muted: "验证失败 / 已禁言", failed_warned: "验证失败 / 已警告", failed_allowed: "验证失败 / 按配置放行" });
Object.assign(statusNames, { held: "未通过验证 / 保持禁言待管理员处理" });
Object.assign(statusNames, { reviewing_unban: "解除限制恢复中", reviewing_ban: "管理员封禁恢复中", unbanned: "已仅解除封禁", unmuted: "已撤销本次禁言", ignored: "已忽略本次警告", allowed: "已处理并加入本群白名单" });
Object.assign(statusNames, { deleting: "旧通知清理中", removed: "原通知已清理" });
const statuses = Object.entries(statusNames).map(([value, label]) => ({ label, value }));
const statusLabel = (value?: string) => value ? statusNames[value] || value : "—";
type IdField = "trusted_bot_ids" | "reviewer_ids" | "notification_ids";
const idFields: { key: IdField; label: string }[] = [{ key: "trusted_bot_ids", label: "受信任 Bot 的用户 ID" }, { key: "reviewer_ids", label: "授权审核用户 ID（留空复用 REPORT_ADMIN_IDS）" }, { key: "notification_ids", label: "通知收件用户 ID（留空复用 REPORT_ADMIN_IDS）" }];

function report(error: unknown) {
  message.error(saveError(error));
}
function registerGroup(chatId: string, group: GuardGroup) {
  const editor = createAutosave(group, {
    save: (changes, previous) => saveGuardSettings(chatId, changes, previous),
    validate: validateGuardField, immediate: immediateGuardFields, rollback: guardSwitches,
  });
  editors.set(chatId, editor);
  groups.value[chatId] = editor.values;
}
function autosaveStatus<T extends object>(editor?: Autosave<T> | null) {
  if (!editor) return "正在加载设置…";
  if (editor.active.value !== null) return "正在自动保存…";
  const states = Object.values(editor.states) as { status: string }[];
  if (states.some(state => state.status === "error")) return "部分设置保存失败，请查看提示";
  if (states.some(state => state.status === "invalid")) return "部分输入未完成，完成后自动保存";
  return editor.hasUnsaved() ? "等待自动保存…" : "全部设置已自动保存";
}
async function flushGroup() { await nextTick(); if (!composing.value) await currentEditor.value?.flush(); }
function startComposition() {
  composing.value = true;
  composingEditor = currentEditor.value;
  composingEditor?.setComposing(true);
}
async function endComposition() {
  const editor = composingEditor;
  await nextTick();
  editor?.setComposing(false);
  if (composingEditor === editor) { composing.value = false; composingEditor = undefined; }
}
async function flushAll() {
  const results = await Promise.all([...editors.values()].map(editor => editor.flush()));
  if (retentionEditor.value) results.push(await retentionEditor.value.flush());
  return results.every(Boolean);
}
function hasUnsaved() { return composing.value || [...editors.values()].some(editor => editor.hasUnsaved()) || retentionEditor.value?.hasUnsaved(); }
function beforeUnload(event: BeforeUnloadEvent) { if (hasUnsaved()) { event.preventDefault(); event.returnValue = ""; } }
function flushHidden() { if (document.visibilityState === "hidden") void flushAll(); }
watch(selectedId, (_, previous) => { if (previous) void editors.get(previous)?.flush(); });
function numeric(value: string, negative = false) {
  const number = Number(value);
  if (!/^-?\d+$/.test(value) || !Number.isSafeInteger(number) || (negative ? number >= 0 : number <= 0)) throw new Error(negative ? "请输入有效的负数群组 ID" : "请输入有效的用户 ID");
  return number;
}
async function addGroup() {
  if (adding.value || loading.value) return;
  adding.value = true;
  try {
    const key = String(numeric(newChatId.value.trim(), true));
    const result = await addGuardGroup(Number(key));
    if (disposed) return;
    if (!groups.value[key]) registerGroup(key, result.group);
    selectedId.value = key;
    newChatId.value = "";
    joinedChatId.value = null;
    message.success(result.created ? "群组已添加并保存，群管默认关闭" : "群组已存在，已切换到该群组");
    await loadJoinedGroups();
  } catch (error) { report(error); } finally { adding.value = false; }
}
function chooseJoinedGroup(value: number | null) { if (value !== null) newChatId.value = String(value); }
async function loadJoinedGroups(force = false) {
  if (discoveryTimer) { clearTimeout(discoveryTimer); discoveryTimer = null; }
  const request = ++discoveryRequest;
  try {
    const result = await getGuardJoinedGroups(force);
    if (disposed || request !== discoveryRequest) return;
    groupMetadata.value = result.metadata;
    joinedGroups.value = result.groups;
    discovering.value = result.discovering;
    discoveryChecked.value = result.checked;
    discoveryTotal.value = result.total;
    if (result.discovering) discoveryTimer = setTimeout(() => loadJoinedGroups(), 2000);
  } catch (error) { if (!disposed && request === discoveryRequest) { discovering.value = false; report(error); } }
}
function setIds(field: IdField, values: string[]) {
  try { if (form.value) form.value[field] = [...new Set(values.map(value => numeric(value.trim())))]; }
  catch (error) { report(error); }
}
function addRule() {
  form.value?.rules.push({ id: `rule_${Date.now()}`, target: "message", kind: "keyword", value: "", action: "delete" });
}
function moveRule(index: number, direction: number) {
  if (!form.value) return;
  const rules = form.value.rules;
  [rules[index], rules[index + direction]] = [rules[index + direction], rules[index]];
}
async function reload() {
  if (loading.value || adding.value) return;
  loading.value = true;
  try {
    if (!await flushAll()) { message.warning("仍有未保存的输入，请先修正或重试；刷新会保留当前草稿。"); return; }
    const data = await getGuardConfig();
    if (disposed) return;
    for (const editor of editors.values()) editor.stop();
    editors.clear();
    groups.value = {};
    for (const [chatId, group] of Object.entries(data.config.groups)) registerGroup(chatId, group);
    retentionEditor.value?.stop();
    retentionEditor.value = createAutosave({ retention_days: data.config.retention_days }, {
      save: (changes, previous) => saveGuardRetention(changes.retention_days!, previous.retention_days!),
      validate: (_, value) => integerError(value, 7, 3650),
    });
    if (!selectedId.value || !groups.value[selectedId.value]) selectedId.value = Object.keys(groups.value)[0] || null;
    await loadJoinedGroups();
  } catch (error) { report(error); } finally { loading.value = false; }
}
let recordRequest = 0;
async function loadRecords() {
  const request = ++recordRequest;
  recordsLoading.value = true;
  try {
    const params: Record<string, unknown> = { limit: 50, offset: (page.value - 1) * 50 };
    if (filterChat.value.trim()) params.chat_id = numeric(filterChat.value.trim(), true);
    if (filterUser.value.trim()) params.user_id = numeric(filterUser.value.trim());
    if (filterStatus.value) params.status = filterStatus.value;
    const result = await getGuardRecords(table.value, params);
    if (request === recordRequest) { records.value = result.rows; total.value = result.total; }
  } catch (error) { if (request === recordRequest) report(error); }
  finally { if (request === recordRequest) recordsLoading.value = false; }
}
function resetRecords() { page.value = 1; loadRecords(); }
function changeTable() { filterStatus.value = table.value === "cases" ? "pending" : null; resetRecords(); }
async function act(row: GuardRecord, action: "allow" | "blacklist" | "unban" | "ban" | "retry" | "removeWhite" | "removeBlack") {
  dialog.warning({
    title: "确认群管操作",
    content: action === "unban" ? "仅撤销本次案件的封禁、禁言或警告，不新增白名单。其他限制及入群验证继续生效。" : action === "ban" ? "将该用户在本群封禁，保留后续审核入口，不加入永久黑名单。" : action === "allow" ? "撤销本次限制并加入本群白名单。其他限制及入群验证继续生效，不会自动重新拉入群。" : action === "blacklist" ? "封禁并将用户 ID 记入永久黑名单；其他群按共享开关生效。" : action === "removeBlack" ? "撤销全局永久拉黑记录。各群现有封禁仍需分别通过案件解除。" : action === "removeWhite" ? "删除本群白名单后，该用户会重新接受本群规则检测。" : "重试此前失败的操作，实际结果会记录在案件中。",
    positiveText: "确认", negativeText: "取消",
    onPositiveClick: async () => {
      try {
        if (action === "removeBlack") await removeGuardBlacklist(row.user_id!);
        else if (action === "removeWhite") await setGuardWhitelist(row.chat_id!, row.user_id!, false);
        else if (action === "retry") await retryGuardCase(row.id!);
        else await reviewGuardCase(row.id!, action, row.payload?.review_generation || 0);
        message.success("操作已完成");
        await loadRecords();
      } catch (error) { report(error); return false; }
    },
  });
}
async function addWhitelist() {
  try { await setGuardWhitelist(numeric(whitelistChat.value.trim(), true), numeric(whitelistUser.value.trim()), true); message.success("本群白名单已添加"); await loadRecords(); }
  catch (error) { report(error); }
}
function evidenceUrl(row: GuardRecord) {
  const url = row.payload?.evidence_link || "";
  return /^https:\/\/t\.me\/(?:c\/\d+|[a-zA-Z0-9_]+)\/\d+$/.test(url) ? url : undefined;
}
const columns = computed<DataTableColumns<GuardRecord>>(() => [
  { title: "群组 / 收件人", key: "chat_id", width: 180, render: row => String(row.chat_id || row.source_chat_id || row.target_id || "—") },
  { title: "用户 ID", key: "user_id", width: 150 },
  { title: "动作", key: "action", width: 150, render: row => row.payload?.review_ban_confirmed ? "管理员封禁" : row.action ? recordActionNames[row.action] || actions.find(action => action.value === row.action)?.label || row.action : "—" },
  { title: "状态", key: "status", width: 190, render: row => statusLabel(row.status || row.synced_status) },
  { title: "时间（UTC）", key: "created_at", width: 190, render: row => row.updated_at || row.created_at || row.last_seen_at || "—" },
  { title: "错误", key: "error", width: 180, ellipsis: { tooltip: true } },
  { title: "操作", key: "actions", width: 440, render: row => h(NSpace, { size: "small" }, { default: () => [
    h(NButton, { size: "small", onClick: () => { detail.value = row; showDetail.value = true; } }, { default: () => "详情" }),
    ...(table.value === "cases" && (row.status === "pending" || (row.status === "done" && ["mute", "warn"].includes(row.action || "") && !row.payload?.verification_failure)) ? [
      h(NButton, { size: "small", onClick: () => act(row, "unban") }, { default: () => row.status === "pending" ? "仅解除封禁" : row.action === "mute" ? "仅解除本次禁言" : "忽略本次警告" }),
      h(NButton, { size: "small", type: "primary", onClick: () => act(row, "allow") }, { default: () => row.action === "warn" && row.status !== "pending" ? "加入本群白名单" : "解除并加入白名单" }),
      ...(row.status !== "pending" ? [h(NButton, { size: "small", type: "warning", onClick: () => act(row, "ban") }, { default: () => "封禁" })] : []),
      h(NButton, { size: "small", type: "error", onClick: () => act(row, "blacklist") }, { default: () => "永久拉黑并入库标记" }),
    ] : []),
    ...(table.value === "cases" && row.status === "execution_failed" ? [h(NButton, { size: "small", onClick: () => act(row, "retry") }, { default: () => "重试" })] : []),
    ...(table.value === "blacklist" ? [h(NButton, { size: "small", type: "warning", onClick: () => act(row, "removeBlack") }, { default: () => "撤销永久拉黑" })] : []),
    ...(table.value === "whitelist" ? [h(NButton, { size: "small", type: "error", onClick: () => act(row, "removeWhite") }, { default: () => "删除白名单" })] : []),
  ] }) },
]);
onBeforeRouteLeave(async () => {
  if (await flushAll()) return true;
  message.warning("仍有未保存的输入，请先修正或重试自动保存。");
  return false;
});
onMounted(() => {
  reload(); loadRecords();
  window.addEventListener("beforeunload", beforeUnload);
  document.addEventListener("visibilitychange", flushHidden);
});
onUnmounted(() => {
  disposed = true;
  if (discoveryTimer) clearTimeout(discoveryTimer);
  for (const editor of editors.values()) editor.stop();
  retentionEditor.value?.stop();
  window.removeEventListener("beforeunload", beforeUnload);
  document.removeEventListener("visibilitychange", flushHidden);
});
</script>

<style scoped>
.toolbar { margin: 12px 0; }
.settings { margin-top: 24px; }
.switch-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(230px, 1fr)); gap: 0 24px; }
.rule-row { margin: 12px 0; }
.evidence { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
