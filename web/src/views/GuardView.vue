<template>
  <div class="guard-page">
    <!-- 页头：标题 + 一句话用途 + 状态徽标 + 主操作（重新加载全部配置） -->
    <header class="page-head">
      <div>
        <h2 class="page-title">Sakura Guard · 樱花智能群管</h2>
        <p class="page-subtitle">按群管理入群验证、消息过滤与处罚审核，全部设置自动保存。</p>
      </div>
      <div class="head-actions">
        <span class="gpill" :class="configLoaded && managedCount > 0 ? 'accent' : 'neutral'">
          <span class="gpdot"></span>{{ headStatusText }}
        </span>
        <n-button quaternary circle :loading="loading" title="重新加载全部配置" aria-label="重新加载全部配置" @click="reload">
          <template #icon><component :is="RefreshIcon" /></template>
        </n-button>
      </div>
    </header>

    <!-- 使用说明：保留原页顶的完整能力说明，弱化为信息卡 -->
    <div class="intro-card">
      <span class="gpdot dot-info"></span>
      <p>
        每个群独立启用，默认关闭。主 Bot 需要删除消息、限制/封禁成员权限；自动批准入群还需要邀请用户权限。
        昵称会在入群、申请、发言和编辑消息时检查。验证通过恢复原权限，解封不会自动拉人回群。
        在主 Bot 担任管理员的群，成员可引用其他用户消息发送“@admin 举报 [原因]”，提交管理员审核；无需开启自动群管。
      </p>
    </div>

    <!-- 配置区三态：首屏骨架 -->
    <n-card v-if="!configLoaded && !configError" :bordered="true">
      <div class="skel">
        <n-skeleton text round :repeat="2" />
        <n-skeleton text round :repeat="6" class="mt-md" />
        <n-skeleton text round :repeat="3" class="mt-md" />
      </div>
    </n-card>
    <!-- 配置区三态：首次加载失败，整卡错误 + 重试 -->
    <n-card v-else-if="configError && !configLoaded" :bordered="true">
      <n-result status="error" title="群管配置加载失败" description="无法获取全局词表、群组设置与审计留存配置，请检查后端连接后重试。">
        <template #footer>
          <n-button type="primary" :loading="loading" @click="reload">重试</n-button>
        </template>
      </n-result>
    </n-card>

    <template v-else>
      <!-- 已加载后的刷新失败：降级提示，不吞掉已有内容 -->
      <n-alert v-if="configError" type="error" :bordered="false">
        <div class="alert-row">
          <span>刷新群管配置失败，当前显示的可能是旧数据。</span>
          <n-button size="small" :loading="loading" @click="reload">重试</n-button>
        </div>
      </n-alert>

      <!-- ==================== 全局昵称关键词 ==================== -->
      <n-card title="全局昵称关键词" :bordered="true">
        <template #header-extra>
          <span v-if="nicknameEditor" class="gpill neutral">{{ nicknameEditor.values.nickname_keywords.length }} 个关键词</span>
        </template>
        <p class="section-hint">
          所有受管群共用这份词表，同时检查昵称和用户名。各群需开启“昵称及用户名过滤”，命中后执行该群配置的动作。新增、删除后自动保存，清空则停用关键词匹配。
        </p>
        <div v-if="!nicknameEditor" class="skel"><n-skeleton text round :repeat="2" /></div>
        <template v-else>
          <!-- 中文输入法组词期间暂停自动保存，结束或失焦后立即保存 -->
          <div @compositionstart="nicknameEditor.setComposing(true)" @compositionend="endNicknameComposition" @focusout="nicknameEditor?.flush()">
            <n-dynamic-tags v-model:value="nicknameEditor.values.nickname_keywords" :disabled="loading" />
          </div>
          <div class="save-status">
            <span role="status" aria-live="polite" class="hint-line">{{ autosaveStatus(nicknameEditor) }}</span>
            <span v-if="nicknameEditor.states.nickname_keywords.error" class="warn-text">{{ nicknameEditor.states.nickname_keywords.error }}</span>
            <n-button v-if="nicknameEditor.states.nickname_keywords.status === 'error'" size="small" @click="nicknameEditor.flush(true)">重试自动保存</n-button>
          </div>
        </template>
      </n-card>

      <!-- ==================== 群组设置 ==================== -->
      <n-card title="群组设置" :bordered="true">
        <!-- 工具栏：选择已管群组 / 从已加入群组挑选 / 输入新群 ID -->
        <div class="page-toolbar">
          <div class="toolbar-left">
            <n-select v-model:value="selectedId" class="sel-group" :options="groupOptions" :disabled="loading || adding" placeholder="选择受管群组" />
            <n-select v-model:value="joinedChatId" class="sel-joined" :options="joinedOptions" :loading="discovering" filterable clearable placeholder="从已加入的群组选择" @update:value="chooseJoinedGroup" />
            <n-input v-model:value="newChatId" class="inp-chat" placeholder="新群 Telegram ID，如 -1001234567890" clearable />
            <n-button type="primary" secondary :loading="adding" :disabled="loading" @click="addGroup">添加群组</n-button>
            <n-button quaternary :loading="discovering" @click="loadJoinedGroups(true)">刷新已加入群组</n-button>
          </div>
        </div>
        <p class="hint-line">
          添加后立即保存，默认关闭群管；群名自动解析。{{ discovering ? `正在核实已加入群组 ${discoveryChecked}/${discoveryTotal}。` : '' }}列表来自主 Bot 会话与群事件；未出现的群可发送一条群消息后刷新，或直接输入群 ID。
        </p>

        <template v-if="form">
          <!-- 自动保存状态行 + 校验问题汇总 -->
          <div class="save-status">
            <span class="hint-line">所有设置自动保存。文字与数字输入暂停片刻或离开输入框后保存；规则填写完整后生效。</span>
            <span role="status" aria-live="polite" class="hint-line">{{ composing ? '正在输入，完成后自动保存…' : autosaveStatus(currentEditor) }}</span>
          </div>
          <n-alert v-if="groupIssues.length" type="warning" :bordered="false" class="mt-sm">
            <div v-for="issue in groupIssues" :key="issue.field">{{ issue.label }}：{{ issue.error }}</div>
            <n-button v-if="groupIssues.some(issue => issue.status === 'error')" size="small" class="mt-sm" @click="currentEditor?.flush(true)">重试自动保存</n-button>
          </n-alert>

          <!-- 分区卡片式设置表单：composition 事件挂在整个表单上，组词期间不触发保存 -->
          <n-form label-placement="top" class="settings-form" :disabled="loading || adding" @focusout="flushGroup" @compositionstart="startComposition" @compositionend="endComposition">
            <!-- 总开关：群管的第一操作，状态一目了然 -->
            <div class="sub-block sub-block--first">
              <div class="master-strip">
                <div>
                  <div class="master-label">
                    启用本群 Sakura Guard
                    <span class="gpill" :class="form.enabled ? 'success' : 'neutral'">
                      <span class="gpdot"></span>{{ form.enabled ? '已启用' : '未启用' }}
                    </span>
                  </div>
                  <div class="master-hint">每个群独立启用，默认关闭；关闭开关不会解除已生效的处罚或验证禁言。</div>
                </div>
                <n-switch v-model:value="form.enabled" aria-label="启用本群 Sakura Guard" :loading="currentEditor?.active.value === 'enabled'" :disabled="loading || adding || currentEditor?.active.value === 'enabled'" />
              </div>
            </div>

            <!-- 功能开关：一屏扫完所有布尔项 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">功能开关</span>
                  <span class="sub-hint">逐项控制本群的检测与联动范围；管理员、受信任 Bot 和本群白名单始终豁免。</span>
                </div>
              </div>
              <div class="switch-grid">
                <n-form-item v-for="field in switchFields" :key="field.key" :label="field.label">
                  <n-switch v-model:value="form[field.key]" :aria-label="field.label" :loading="currentEditor?.active.value === field.key" :disabled="loading || adding || currentEditor?.active.value === field.key" />
                </n-form-item>
              </div>
            </div>

            <!-- 入群验证：阈值 + 流程动作集中在一处 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">入群验证</span>
                  <span class="sub-hint">新成员先答题后发言，通过后自动恢复权限；超时与答错的处罚动作在此配置。</span>
                </div>
              </div>
              <n-alert type="info" :bordered="false" class="block-gap-sm">
                已开始的验证仅在本人答对或管理员点击“跳过验证”后恢复权限。超时或答错后的警告、禁言及等待人工处理均保留验证禁言；关闭开关或加入白名单也不会自动解禁正在验证的成员。
              </n-alert>
              <div class="field-grid">
                <n-form-item label="验证超时（秒）"><n-input-number v-model:value="form.verification_timeout" :min="30" :max="600" /></n-form-item>
                <n-form-item label="最多答错次数"><n-input-number v-model:value="form.verification_max_attempts" :min="1" :max="10" /></n-form-item>
                <n-form-item label="入群申请流程"><n-select v-model:value="form.join_requests" :options="joinOptions" /></n-form-item>
                <n-form-item label="入群申请命中规则后的动作"><n-select v-model:value="form.join_request_match_action" :options="requestMatchActions" /></n-form-item>
                <n-form-item label="验证超时后的动作"><n-select v-model:value="form.verification_timeout_action" :options="verificationActions" /></n-form-item>
                <n-form-item label="答错达到上限后的动作"><n-select v-model:value="form.verification_wrong_action" :options="verificationActions" /></n-form-item>
                <n-form-item label="验证结束后的消息处理"><n-select v-model:value="form.verification_message_mode" :options="verificationMessageModes" /></n-form-item>
                <n-form-item label="验证管理按钮（仅踢出由上方独立开关控制）">
                  <n-select v-model:value="form.verification_admin_actions" multiple clearable :options="verificationAdminActions" placeholder="清空后隐藏封禁、拉黑和跳过按钮" />
                </n-form-item>
              </div>
            </div>

            <!-- 过滤与处罚：各检测项的命中动作 + 关键词 + 名单 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">过滤与处罚</span>
                  <span class="sub-hint">各类消息命中后的动作互相独立；禁言时长与关键词在此统一配置。</span>
                </div>
              </div>
              <div class="field-grid">
                <n-form-item label="昵称及用户名关键词命中动作"><n-select v-model:value="form.nickname_action" :options="memberActions" /></n-form-item>
                <n-form-item label="机器人发言命中动作"><n-select v-model:value="form.bot_message_action" :options="actions" /></n-form-item>
                <n-form-item label="带按钮消息命中动作"><n-select v-model:value="form.button_message_action" :options="actions" /></n-form-item>
                <n-form-item label="转发消息命中动作"><n-select v-model:value="form.forwarded_message_action" :options="actions" /></n-form-item>
                <n-form-item label="联系人消息命中动作"><n-select v-model:value="form.contact_message_action" :options="actions" /></n-form-item>
                <n-form-item label="消息关键词命中动作"><n-select v-model:value="form.message_action" :options="actions" /></n-form-item>
                <!-- 消息与链接关键词：恢复旧版唯一维护入口。直接 v-model 编辑器响应式值，
                     深监听自动保存（立即触发，校验 100 项/128 字符）；错误经 guardLabels 汇总到顶部告警，
                     中文组词期间由整个表单的 composition 保护暂停保存。整行展示便于浏览较多标签。 -->
                <n-form-item label="消息与链接关键词" class="span-all">
                  <n-dynamic-tags v-model:value="form.message_keywords" />
                </n-form-item>
                <n-form-item label="禁言时长（秒）"><n-input-number v-model:value="form.mute_seconds" :min="30" :max="2592000" /></n-form-item>
                <n-form-item label="受信任 Bot 的用户 ID" class="span-all">
                  <n-dynamic-tags :value="form.trusted_bot_ids.map(String)" @update:value="(values: string[]) => setIds('trusted_bot_ids', values)" />
                </n-form-item>
              </div>
              <n-alert type="warning" :bordered="false" class="block-gap-sm">
                通知目标不自动获得审核权限。审核用户列表为空时复用 REPORT_ADMIN_IDS；其他群只在启用共享后拦截永久黑名单。
                机器人发言、带按钮消息、转发消息和联系人消息各自独立于消息关键词过滤；管理员、受信任 Bot 和本群白名单继续豁免，原有昵称及共享黑名单规则仍生效。联系人过滤处罚消息发送者，卡片中的联系人不作为处罚对象。
                要接收其他机器人的普通消息，需在 BotFather 为主 Bot 开启 Bot-to-Bot Communication Mode，并确保主 Bot 是群管理员。
              </n-alert>
            </div>

            <!-- 审核与通知 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">审核与通知</span>
                  <span class="sub-hint">决定谁能审核案件、私聊失败时的备选审核群，以及哪些事件通知管理员。</span>
                </div>
              </div>
              <div class="field-grid">
                <n-form-item label="授权审核用户 ID（留空复用 REPORT_ADMIN_IDS）">
                  <n-dynamic-tags :value="form.reviewer_ids.map(String)" @update:value="(values: string[]) => setIds('reviewer_ids', values)" />
                </n-form-item>
                <n-form-item label="通知收件用户 ID（留空复用 REPORT_ADMIN_IDS）">
                  <n-dynamic-tags :value="form.notification_ids.map(String)" @update:value="(values: string[]) => setIds('notification_ids', values)" />
                </n-form-item>
                <n-form-item label="审核群 ID（私聊失败的备选；留空禁用）">
                  <n-input-number v-model:value="form.review_chat_id" :max="-1" :show-button="false" />
                </n-form-item>
                <n-form-item label="通知事件（默认全部通知管理员）">
                  <n-select v-model:value="form.notification_events" multiple clearable :options="notificationEvents" />
                </n-form-item>
              </div>
              <p class="hint-line">
                通知收件人留空时复用 REPORT_ADMIN_IDS；可按事件关闭通知。验证管理按钮仅群主、具有封禁成员权限的管理员及 REPORT_ADMIN_IDS 可操作。跳过验证不会加入白名单或解除其他规则的限制。
              </p>
            </div>

            <!-- 举报超时 AI 审核 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">举报超时 AI 审核</span>
                  <span class="sub-hint">管理员未及时处理的举报，超时后交由 AI 复核并按结论执行。</span>
                </div>
              </div>
              <div class="field-grid">
                <n-form-item label="AI 审核等待时间（分钟）"><n-input-number v-model:value="form.ai_report_review_delay_minutes" :min="1" :max="1440" /></n-form-item>
                <n-form-item label="AI 明确违规默认动作"><n-select v-model:value="form.ai_report_review_action" :options="actions" /></n-form-item>
              </div>
              <n-alert type="info" :bordered="false" class="block-gap-sm">
                举报超时 AI 审核需同时启用本群群管与上方开关，仅处理启用后的新举报。默认等待 10 分钟；管理员先处理即停止 AI 审核。复用“AI 配置”中的模型，核对被举报消息、昵称、用户名、简介及现有规则和黑名单。命中现有规则时执行该规则动作；其他明确违规执行默认动作。不确定、证据变化或调用失败则继续等待管理员。AI 不修改全局黑名单或白名单，封禁后立即清理该用户在本群的全部消息。
              </n-alert>
            </div>

            <!-- 自定义规则：表格 + 新建/编辑弹窗，按顺序匹配 -->
            <div class="sub-block">
              <div class="sub-head">
                <div>
                  <span class="sub-title">自定义规则（按顺序匹配）</span>
                  <span class="sub-hint">正则匹配归一化后的文本，限时执行。昵称与用户名规则可指定动作，或跟随本群昵称命中动作。</span>
                </div>
                <n-button type="primary" secondary @click="openRuleModal(-1)">
                  <template #icon><component :is="PlusIcon" /></template>
                  添加规则
                </n-button>
              </div>
              <n-data-table
                size="small"
                :columns="ruleColumns"
                :data="form.rules"
                :bordered="false"
                :scroll-x="1020"
                :loading="currentEditor?.active.value === 'rules'"
              />
              <p class="hint-line">规则填写完整后自动保存并生效，最多 100 条；可用上方箭头调整匹配顺序。</p>
            </div>
          </n-form>
        </template>
        <!-- 未选择群组时的空态：告诉用户下一步 -->
        <n-empty v-else class="table-empty" description="选择已加入的群组或输入群 ID，点击添加后立即保存" />
      </n-card>

      <!-- ==================== 案件、名单与处罚日志 ==================== -->
      <n-card title="案件、名单与处罚日志" :bordered="true">
        <template #header-extra>
          <span class="gpill neutral">共 {{ total }} 条</span>
        </template>
        <!-- 工具栏：名单类型 + 筛选 + 查询 -->
        <div class="page-toolbar">
          <div class="toolbar-left">
            <n-select v-model:value="table" class="sel-table" :options="tables" @update:value="changeTable" />
            <n-input v-model:value="filterChat" class="inp-filter" placeholder="群组 ID（可选）" clearable />
            <n-input v-model:value="filterUser" class="inp-filter" placeholder="用户 ID（可选）" clearable />
            <n-select v-model:value="filterStatus" class="sel-status" :options="statuses" clearable placeholder="审核/执行状态" />
          </div>
          <n-button secondary :loading="recordsLoading" @click="resetRecords">查询 / 刷新</n-button>
        </div>

        <!-- 本群白名单快捷添加 -->
        <div v-if="table === 'whitelist'" class="white-row">
          <n-input v-model:value="whitelistChat" class="inp-filter" placeholder="白名单群组 ID" clearable />
          <n-input v-model:value="whitelistUser" class="inp-filter" placeholder="用户 ID" clearable />
          <n-button size="small" @click="addWhitelist">添加本群白名单</n-button>
          <span class="hint-line">白名单不会解除已有封禁；请通过案件审核解封。</span>
        </div>

        <!-- 三态：首屏骨架 -->
        <div v-if="!recordsLoaded" class="skel">
          <n-skeleton v-for="i in 5" :key="i" height="40px" :sharp="false" class="skel-row" />
        </div>
        <template v-else>
          <!-- 三态：查询失败（保留旧数据并给出重试） -->
          <n-alert v-if="recordsError" type="error" :bordered="false" class="block-gap-sm">
            <div class="alert-row">
              <span>记录查询失败，请检查筛选条件或后端连接后重试。</span>
              <n-button size="small" :loading="recordsLoading" @click="resetRecords">重试</n-button>
            </div>
          </n-alert>
          <!-- 三态：空态，提供清除筛选的出口 -->
          <n-empty v-else-if="records.length === 0" class="table-empty" description="没有符合条件的记录">
            <template #extra>
              <n-button size="small" @click="clearRecordFilters">清除筛选并刷新</n-button>
            </template>
          </n-empty>
          <n-data-table
            v-else
            size="small"
            :columns="columns"
            :data="records"
            :loading="recordsLoading"
            :scroll-x="1210"
            :bordered="false"
          />
        </template>
        <div class="pager">
          <n-pagination v-model:page="page" :page-size="50" :item-count="total" @update:page="loadRecords" />
        </div>
      </n-card>

      <!-- ==================== 审计留存 ==================== -->
      <n-card title="审计留存" :bordered="true">
        <p class="section-hint">仅清理过期的已结束案件和验证；永久名单、待审核和恢复中的操作持续保留。</p>
        <div v-if="retentionEditor" class="retention-row">
          <span class="retention-label">保留天数</span>
          <n-input-number
            v-model:value="retentionEditor.values.retention_days"
            :disabled="loading"
            :min="7"
            :max="3650"
            class="retention-input"
            @blur="retentionEditor?.flush()"
          />
          <span role="status" aria-live="polite" class="hint-line">{{ autosaveStatus(retentionEditor) }}</span>
          <span v-if="retentionEditor.states.retention_days.error" class="warn-text">{{ retentionEditor.states.retention_days.error }}</span>
          <n-button v-if="retentionEditor.states.retention_days.status === 'error'" size="small" @click="retentionEditor.flush(true)">重试自动保存</n-button>
        </div>
        <div v-else class="skel"><n-skeleton text round :repeat="1" /></div>
      </n-card>
    </template>

    <!-- ==================== 新建 / 编辑自定义规则 ==================== -->
    <n-modal
      v-model:show="showRuleModal"
      preset="card"
      class="modal-responsive"
      :title="ruleEditingIndex >= 0 ? '编辑规则' : '新建规则'"
      :mask-closable="false"
    >
      <n-form ref="ruleFormRef" class="modal-form" :model="ruleForm" :rules="ruleFormRules" label-placement="left" label-width="88">
        <n-form-item label="规则 ID" path="id">
          <n-input v-model:value="ruleForm.id" placeholder="唯一 ID，如 link_block" />
        </n-form-item>
        <n-form-item label="匹配目标">
          <n-select v-model:value="ruleForm.target" :options="targets" />
        </n-form-item>
        <n-form-item label="匹配类型">
          <n-select v-model:value="ruleForm.kind" :options="kinds" />
        </n-form-item>
        <n-form-item label="内容" path="value">
          <n-input v-model:value="ruleForm.value" :placeholder="ruleForm.kind === 'keyword' ? '关键词' : '正则表达式'" />
        </n-form-item>
        <n-form-item label="命中动作">
          <n-select v-model:value="ruleForm.action" :options="ruleForm.target === 'nickname' || ruleForm.target === 'username' ? nicknameRuleActions : actions" />
        </n-form-item>
      </n-form>
      <p class="modal-hint">规则按列表顺序匹配，保存后自动写入该群配置并生效。</p>
      <template #footer>
        <div class="modal-footer">
          <n-button quaternary @click="showRuleModal = false">取消</n-button>
          <n-button type="primary" @click="saveRuleModal">确定</n-button>
        </div>
      </template>
    </n-modal>

    <!-- ==================== 案件与证据详情 ==================== -->
    <n-modal v-model:show="showDetail" preset="card" style="width: min(760px, calc(100vw - 32px))" title="案件与证据">
      <n-descriptions v-if="detail" :column="1" bordered label-placement="left">
        <n-descriptions-item label="案件">{{ detail.id || detail.case_id }}</n-descriptions-item>
        <n-descriptions-item label="群组 / 用户">{{ detail.chat_id || detail.source_chat_id }} / {{ detail.user_id }}</n-descriptions-item>
        <n-descriptions-item label="用户快照">{{ detail.payload?.name }} · @{{ detail.payload?.username || '—' }}</n-descriptions-item>
        <n-descriptions-item v-if="detail.payload?.reporter" label="举报人">{{ detail.payload.reporter.name || '—' }} · {{ detail.payload.reporter.user_id }} · @{{ detail.payload.reporter.username || '—' }}</n-descriptions-item>
        <n-descriptions-item label="规则 / 原因">{{ detail.payload?.evidence?.rule_id }} · {{ detail.payload?.evidence?.reason }}</n-descriptions-item>
        <n-descriptions-item label="命中原文"><span class="evidence">{{ detail.payload?.evidence?.original }}</span></n-descriptions-item>
        <n-descriptions-item label="归一化结果"><span class="evidence">{{ detail.payload?.evidence?.normalized }}</span></n-descriptions-item>
        <n-descriptions-item label="消息证据">
          <a v-if="evidenceUrl(detail)" :href="evidenceUrl(detail)" target="_blank" rel="noopener noreferrer">打开 Telegram 消息</a>
          <span v-else>{{ detail.payload?.message_id || '—' }}</span>
        </n-descriptions-item>
        <n-descriptions-item label="状态">{{ recordStatus(detail) }}</n-descriptions-item>
        <n-descriptions-item v-if="detail.payload?.guest_actor" label="访客机器人调用者">{{ detail.payload.guest_ban?.status === 'blacklisted' ? '已联动封禁并加入全局黑名单' : detail.payload.guest_ban?.status === 'banned' ? '已联动封禁' : detail.payload.guest_ban?.status === 'skipped' ? '已按权限或豁免规则跳过' : detail.payload.guest_ban?.action === 'blacklist' ? '联动封禁及全局拉黑待处理' : detail.payload.guest_ban ? '联动封禁待处理' : '仅记录来源' }}<span v-if="detail.payload.guest_ban?.error"> · {{ detail.payload.guest_ban.error }}</span><div v-if="detail.payload.guest_ban?.case_id">关联案件：{{ detail.payload.guest_ban.case_id }}</div></n-descriptions-item>
        <n-descriptions-item v-if="detail.payload?.ai_report_review" label="AI 超时审核">
          {{ detail.payload.ai_report_review.state === 'applied' ? '已自动处罚' : detail.payload.ai_report_review.state === 'executing' ? '正在执行处罚' : '继续等待管理员' }}
          · {{ detail.payload.ai_report_review.model || '—' }} · {{ detail.payload.ai_report_review.action || '—' }}
          <div v-if="detail.payload.ai_report_review.verdict">{{ detail.payload.ai_report_review.verdict.reason }}（置信度 {{ Math.round(detail.payload.ai_report_review.verdict.confidence * 100) }}%）</div>
          <div v-if="detail.payload.ai_report_review.error">{{ detail.payload.ai_report_review.error }}</div>
          <div v-if="detail.payload.ai_report_review.input" class="evidence">资料：{{ detail.payload.ai_report_review.input.profile.nickname }} · {{ detail.payload.ai_report_review.input.profile.usernames.join(' / ') }}
简介：{{ detail.payload.ai_report_review.input.profile.bio ?? '无法获取' }}
规则：{{ detail.payload.ai_report_review.input.rule_match?.reason || '未直接命中' }} · 黑名单：{{ detail.payload.ai_report_review.input.blacklist.matched ? '命中' : '未命中' }}</div>
          <div v-for="(item, index) in detail.payload.ai_report_review.verdict?.evidence || []" :key="index" class="evidence">{{ item.source }}：{{ item.quote }}</div>
        </n-descriptions-item>
        <n-descriptions-item v-if="detail.payload?.ban_message_cleanup" label="封禁后消息清理">{{ banHistoryStatus(detail) }} · {{ detail.payload.ban_message_cleanup.actor }} · {{ detail.payload.ban_message_cleanup.completed_at || detail.payload.ban_message_cleanup.requested_at }} UTC<span v-if="detail.payload.ban_message_cleanup.error"> · {{ detail.payload.ban_message_cleanup.error }}</span></n-descriptions-item>
        <n-descriptions-item v-if="detail.payload?.report_message_deletion" label="原消息删除">{{ detail.payload.report_message_deletion.status === 'deleted' ? '已删除或已不存在' : '待确认删除，后台自动重试' }} · {{ detail.payload.report_message_deletion.actor }} · {{ detail.payload.report_message_deletion.completed_at || detail.payload.report_message_deletion.requested_at }} UTC<span v-if="detail.payload.report_message_deletion.error"> · {{ deletionError(detail.payload.report_message_deletion.error) }}</span></n-descriptions-item>
        <n-descriptions-item label="审核人 / UTC 时间">{{ detail.reviewer_actor || detail.reviewer_id || '—' }} / {{ detail.reviewed_at || '—' }}</n-descriptions-item>
        <n-descriptions-item label="错误">{{ detail.error || '—' }}</n-descriptions-item>
        <n-descriptions-item v-if="detail.message_cleanup_pending !== undefined" label="验证消息清理">{{ detail.message_cleanup_pending ? '待清理，后台自动重试' : '已清理或当前无需清理' }}</n-descriptions-item>
      </n-descriptions>
      <template #footer>
        <div class="modal-footer modal-footer--end">
          <n-button quaternary @click="showDetail = false">关闭</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NDropdown,
  NDynamicTags, NEmpty, NForm, NFormItem, NIcon, NInput, NInputNumber, NModal,
  NPagination, NPopconfirm, NResult, NSelect, NSkeleton, NSwitch, useDialog, useMessage,
  type DataTableColumns, type FormInst, type FormItemRule, type FormRules,
} from "naive-ui";
import {
  addGuardGroup, getGuardJoinedGroups, getGuardConfig, getGuardRecords, removeGuardBlacklist, retryGuardCase,
  reviewGuardCase, saveGuardSettings, saveGuardRetention, saveGuardNicknameKeywords, setGuardWhitelist,
  type GuardAction, type GuardChat, type GuardGroup, type GuardRecord, type GuardRule, type GuardTable,
} from "@/api/guard";
import { createAutosave, type Autosave, saveError } from "@/composables/autosave";
import { guardLabels, guardSwitches, immediateGuardFields, integerError, validateGuardField, validateNicknameKeywords } from "@/composables/guard-form";

const message = useMessage();
const dialog = useDialog();

// ── 本地图标（与已完成页面同款内联 SVG 方式，页面私有图标不进共享文件） ──
function svgIcon(viewBox: string, pathD: string) {
  return () =>
    h(NIcon, null, {
      default: () =>
        h("svg", { viewBox, fill: "currentColor", width: "1em", height: "1em" }, [
          h("path", { d: pathD }),
        ]),
    });
}
const PlusIcon = svgIcon("0 0 24 24", "M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z");
const RefreshIcon = svgIcon(
  "0 0 24 24",
  "M17.65 6.35C16.2 4.9 14.21 4 12 4c-4.42 0-7.99 3.58-8 8s3.57 8 8 8c3.73 0 6.84-2.55 7.73-6h-2.08c-.82 2.33-3.04 4-5.65 4-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
);

const loading = ref(false), recordsLoading = ref(false), adding = ref(false), composing = ref(false);
// 配置区三态：首次加载骨架 / 失败整卡错误 / 已加载后刷新失败仅降级提示
const configLoaded = ref(false), configError = ref(false);
// 记录区三态：首屏骨架 / 空态 / 失败告警
const recordsLoaded = ref(false), recordsError = ref(false);
// 总开关单独展示，其余开关进网格
const switchFields = guardSwitches.filter(key => key !== "enabled").map(key => ({ key, label: guardLabels[key] }));
const editors = new Map<string, Autosave<GuardGroup>>();
const retentionEditor = shallowRef<Autosave<{ retention_days: number }> | null>(null);
const nicknameEditor = shallowRef<Autosave<{ nickname_keywords: string[] }> | null>(null);
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
// 页头状态徽标：启用数 / 受管总数
const managedCount = computed(() => Object.keys(groups.value).length);
const enabledCount = computed(() => Object.values(groups.value).filter(group => group.enabled).length);
const headStatusText = computed(() => {
  if (!configLoaded.value) return "加载中…";
  if (managedCount.value === 0) return "暂无受管群组";
  return `已启用 ${enabledCount.value} / ${managedCount.value} 群`;
});
const table = ref<GuardTable>("cases"), records = ref<GuardRecord[]>([]);
const filterChat = ref(""), filterUser = ref(""), filterStatus = ref<string | null>("pending");
const whitelistChat = ref(""), whitelistUser = ref("");
const page = ref(1), total = ref(0);
const detail = ref<GuardRecord | null>(null), showDetail = ref(false);

// ── 选项常量（与旧版一致的取值与文案） ──
const actions = [{ label: "删除消息", value: "delete" }, { label: "警告", value: "warn" }, { label: "禁言", value: "mute" }, { label: "踢出", value: "kick" }, { label: "封禁并通知审核", value: "ban" }];
const memberActions = actions.filter(action => action.value !== "delete");
const nicknameRuleActions = [{ label: "跟随昵称命中动作", value: "delete" }, ...memberActions];
const verificationActions = [{ label: "封禁并通知审核", value: "ban" }, { label: "踢出（可再加入）", value: "kick" }, { label: "保持验证禁言，等待管理员", value: "mute" }, { label: "警告并保持验证禁言", value: "warn" }, { label: "等待管理员处理（保持禁言）", value: "allow" }];
const verificationMessageModes = [{ label: "删除验证消息（默认）", value: "delete" }, { label: "保留处理结果，移除所有按钮", value: "result" }];
const verificationAdminActions = [{ label: "一键封禁", value: "ban" }, { label: "封禁并加入黑名单", value: "blacklist" }, { label: "跳过验证", value: "skip" }];
const requestMatchActions = [{ label: "拒绝申请（默认）", value: "reject" }, { label: "保持待审，通知管理员", value: "manual" }, { label: "批准后继续按入群规则处理", value: "approve" }];
const notificationEvents = [{ label: "过滤、举报及处罚", value: "moderation" }, { label: "验证开始", value: "verification_start" }, { label: "验证通过 / 取消", value: "verification_end" }, { label: "验证失败处理", value: "verification_fail" }, { label: "管理员验证操作", value: "admin_action" }, { label: "入群申请处理", value: "join_request" }, { label: "白名单操作", value: "whitelist" }, { label: "黑名单操作", value: "blacklist" }];
const recordActionNames: Record<string, string> = { report: "用户举报", verify_skip: "管理员跳过验证", verify_hold: "保持验证禁言，等待管理员", verify_allow: "历史配置放行", verify_start: "开始入群验证", verify_pass: "入群验证通过", verify_cancel: "结束 / 取消验证", whitelist_add: "添加白名单", whitelist_remove: "移除白名单", blacklist_remove: "撤销永久黑名单", approve: "批准入群申请", reject: "拒绝入群申请", manual: "保留申请待审" };
recordActionNames.guest_blacklist = "访客调用者全局拉黑";
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
Object.assign(statusNames, { kicked: "已仅踢出 · 可再次加入", reviewing_kick: "仅踢出恢复中", admin_kicked: "管理员已仅踢出", admin_kick_pending: "管理员仅踢出处理中" });
const statuses = Object.entries(statusNames).map(([value, label]) => ({ label: value === "pending" ? "待审核（封禁 / 用户举报）" : value === "ignored" ? "已忽略（警告 / 举报）" : label, value }));
const statusLabel = (value?: string) => value ? statusNames[value] || value : "—";

const pendingReport = (row: GuardRecord) => row.action === "report" && !row.payload?.review_ban_confirmed;
function canKick(row: GuardRecord) {
  return ["ban", "mute", "warn", "report"].includes(row.action || "") &&
    (row.status === "pending" || (row.status === "done" && ["mute", "warn"].includes(row.action || "")) ||
      (row.action === "report" && ["allowed", "blacklisted", "unbanned", "unmuted", "ignored", "reviewing_kick"].includes(row.status || ""))) &&
    !row.payload?.verification_failure && (groups.value[String(row.chat_id)]?.kick_button_enabled ?? true);
}
function deletionError(value: string) {
  const names: Record<string, string> = {
    "guard.report_delete_permission_error": "主 Bot 缺少管理员或删除消息权限",
    "guard.report_delete_target_changed": "原消息身份与举报记录不符，已停止删除",
    "guard.report_delete_unconfirmed": "仍能读取原消息，等待重试确认",
    MessageDeleteForbiddenError: "Bot 没有删除该消息的权限",
    ChatAdminRequiredError: "Bot 需要群管理员权限",
    ChatWriteForbiddenError: "Bot 当前无法操作该群组",
    TimeoutError: "Telegram 请求超时，稍后重试",
  };
  return names[value] || "删除未确认，请检查 Bot 权限或稍后重试";
}
function recordStatus(row: GuardRecord) {
  if (pendingReport(row) && row.status === "pending") return "用户举报 · 待审核（尚未处罚）";
  if (pendingReport(row) && row.status === "ignored") return "已忽略举报";
  if (pendingReport(row) && row.status === "reviewing_unban") return "忽略举报 · 正在保存审核决定";
  if (!row.action && row.synced_status === "pending") return "待审核";
  return statusLabel(row.status || row.synced_status);
}
function banHistoryStatus(row: GuardRecord) {
  const cleanup = row.payload?.ban_message_cleanup;
  return cleanup?.status === "deleted" ? "已清理该用户在本群的全部历史消息" : cleanup?.status === "cancelled" ? "封禁状态已变化，已取消清理" : cleanup?.error ? "清理尚未完成，后台自动重试" : "等待清理";
}

// ── 自动保存基础设施：composition 保护、离开页面前强制冲刷 ──
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
async function endNicknameComposition() {
  await nextTick();
  nicknameEditor.value?.setComposing(false);
}
async function flushAll() {
  const results = await Promise.all([...editors.values()].map(editor => editor.flush()));
  if (retentionEditor.value) results.push(await retentionEditor.value.flush());
  if (nicknameEditor.value) results.push(await nicknameEditor.value.flush());
  return results.every(Boolean);
}
function hasUnsaved() { return composing.value || [...editors.values()].some(editor => editor.hasUnsaved()) || retentionEditor.value?.hasUnsaved() || nicknameEditor.value?.hasUnsaved(); }
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
type IdField = "trusted_bot_ids" | "reviewer_ids" | "notification_ids";
function setIds(field: IdField, values: string[]) {
  try { if (form.value) form.value[field] = [...new Set(values.map(value => numeric(value.trim())))]; }
  catch (error) { report(error); }
}

// ── 自定义规则：表格行内排序/删除，编辑走弹窗；保存仍由 autosave 深度监听触发 ──
function moveRule(index: number, direction: number) {
  if (!form.value) return;
  const rules = form.value.rules;
  [rules[index], rules[index + direction]] = [rules[index + direction], rules[index]];
}
const showRuleModal = ref(false);
const ruleEditingIndex = ref(-1);
const ruleFormRef = ref<FormInst | null>(null);
const ruleForm = ref<GuardRule>({ id: "", target: "message", kind: "keyword", value: "", action: "delete" });
function openRuleModal(index: number) {
  ruleEditingIndex.value = index;
  ruleForm.value = index >= 0 && form.value
    ? { ...form.value.rules[index] }
    : { id: `rule_${Date.now()}`, target: "message", kind: "keyword", value: "", action: "delete" };
  ruleFormRef.value?.restoreValidation();
  showRuleModal.value = true;
}
const ruleFormRules: FormRules = {
  id: {
    required: true, trigger: ["blur", "input"],
    validator: (_rule: FormItemRule, value: string): boolean | Error => {
      const id = (value || "").trim();
      if (!/^[a-zA-Z0-9_-]{1,64}$/.test(id)) return new Error("请填写 1–64 位字母、数字、下划线或短横线");
      const clash = form.value?.rules.some((other, index) => index !== ruleEditingIndex.value && other.id === id);
      return clash ? new Error("该 ID 已被其他规则使用") : true;
    },
  },
  value: {
    required: true, trigger: ["blur", "input"],
    validator: (_rule: FormItemRule, value: string): boolean | Error => {
      const text = value || "";
      if (!text || (ruleForm.value.kind === "keyword" && !text.trim())) return new Error(ruleForm.value.kind === "keyword" ? "请填写关键词" : "请填写正则表达式");
      if (text.length > 256) return new Error("内容最多 256 字符");
      return true;
    },
  },
};
async function saveRuleModal() {
  try { await ruleFormRef.value?.validate(); } catch { return; }
  const target = form.value;
  if (!target) return;
  const entry: GuardRule = { ...ruleForm.value, id: ruleForm.value.id.trim() };
  const editing = ruleEditingIndex.value;
  if (editing >= 0) target.rules.splice(editing, 1, entry);
  else {
    if (target.rules.length >= 100) { message.error("最多可添加 100 条规则"); return; }
    target.rules.push(entry);
  }
  showRuleModal.value = false;
  message.success(editing >= 0 ? "规则已更新，稍后自动保存" : "规则已添加，稍后自动保存");
}
function targetLabel(value: GuardRule["target"]) { return targets.find(target => target.value === value)?.label || value; }
function ruleActionLabel(rule: GuardRule) {
  if ((rule.target === "nickname" || rule.target === "username") && rule.action === "delete") return "跟随昵称命中动作";
  return actions.find(action => action.value === rule.action)?.label || rule.action;
}
function actionSeverity(action: GuardAction) {
  return action === "ban" || action === "kick" ? "error" : action === "mute" || action === "warn" ? "warning" : "accent";
}
// 单条规则的本地完整性检查，用于"生效情况"列（不做网络请求）
function ruleIssue(rule: GuardRule, index: number): string | null {
  if (!/^[a-zA-Z0-9_-]{1,64}$/.test(rule.id)) return "ID 需为 1–64 位字母、数字、下划线或短横线";
  if (form.value?.rules.some((other, i) => i !== index && other.id === rule.id)) return "ID 与其他规则重复";
  if (rule.value.length > 256) return "内容最多 256 字符";
  if (!rule.value || (rule.kind === "keyword" && !rule.value.trim())) return "填写关键词或正则后自动生效";
  return null;
}
const ruleColumns = computed<DataTableColumns<GuardRule>>(() => [
  { title: "#", key: "index", width: 52, render: (_row, index) => h("span", { class: "g-idx" }, String(index + 1)) },
  { title: "规则 ID", key: "id", width: 150, ellipsis: { tooltip: true }, render: row => h("span", { class: "g-rid" }, row.id) },
  { title: "目标", key: "target", width: 120, render: row => targetLabel(row.target) },
  {
    title: "类型", key: "kind", width: 100,
    render: row => h("span", { class: `gpill ${row.kind === "regex" ? "warning" : "accent"}` }, [h("span", { class: "gpdot" }), row.kind === "regex" ? "正则" : "关键词"]),
  },
  { title: "内容", key: "value", minWidth: 180, ellipsis: { tooltip: true } },
  {
    title: "命中动作", key: "action", width: 160,
    render: row => h("span", { class: `gpill ${actionSeverity(row.action)}` }, [h("span", { class: "gpdot" }), ruleActionLabel(row)]),
  },
  {
    title: "生效情况", key: "ready", width: 104,
    render: (row, index) => {
      const issue = ruleIssue(row, index);
      return issue
        ? h("span", { class: "gpill warning", title: issue }, [h("span", { class: "gpdot" }), "待补全"])
        : h("span", { class: "gpill success" }, [h("span", { class: "gpdot" }), "完整"]);
    },
  },
  {
    title: "操作", key: "actions", width: 212, align: "right",
    render: (_row, index) => [
      h(NButton, { size: "small", quaternary: true, circle: true, title: "上移", disabled: index === 0, onClick: () => moveRule(index, -1) }, () => "↑"),
      h(NButton, { size: "small", quaternary: true, circle: true, title: "下移", disabled: index === (form.value?.rules.length ?? 0) - 1, onClick: () => moveRule(index, 1) }, () => "↓"),
      h(NButton, { size: "small", quaternary: true, type: "primary", title: "编辑规则", onClick: () => openRuleModal(index) }, () => "编辑"),
      h(NPopconfirm, { onPositiveClick: () => form.value?.rules.splice(index, 1) }, {
        trigger: () => h(NButton, { size: "small", quaternary: true, type: "error", circle: true, title: "删除规则" }, () => "×"),
        default: () => "删除后该规则停止匹配并自动保存，如需恢复请重新添加。确定删除？",
      }),
    ],
  },
]);

// ── 配置加载 / 刷新（保留冲刷未保存输入的保护逻辑） ──
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
    nicknameEditor.value?.stop();
    nicknameEditor.value = createAutosave({ nickname_keywords: data.config.nickname_keywords }, {
      save: (changes, previous) => saveGuardNicknameKeywords(changes.nickname_keywords!, previous.nickname_keywords!),
      validate: (_, value) => validateNicknameKeywords(value), immediate: ["nickname_keywords"],
    });
    retentionEditor.value?.stop();
    retentionEditor.value = createAutosave({ retention_days: data.config.retention_days }, {
      save: (changes, previous) => saveGuardRetention(changes.retention_days!, previous.retention_days!),
      validate: (_, value) => integerError(value, 7, 3650),
    });
    if (!selectedId.value || !groups.value[selectedId.value]) selectedId.value = Object.keys(groups.value)[0] || null;
    configLoaded.value = true;
    configError.value = false;
    await loadJoinedGroups();
  } catch (error) {
    if (!disposed) { configLoaded.value = true; configError.value = true; report(error); }
  } finally { loading.value = false; }
}

// ── 案件 / 名单记录：筛选 + 分页 + 三态 ──
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
    if (request === recordRequest) { records.value = result.rows; total.value = result.total; recordsError.value = false; }
  } catch (error) {
    if (request === recordRequest) { recordsError.value = true; report(error); }
  } finally {
    if (request === recordRequest) { recordsLoading.value = false; recordsLoaded.value = true; }
  }
}
function resetRecords() { page.value = 1; loadRecords(); }
function changeTable() { filterStatus.value = table.value === "cases" ? "pending" : null; resetRecords(); }
function clearRecordFilters() { filterChat.value = ""; filterUser.value = ""; filterStatus.value = table.value === "cases" ? "pending" : null; resetRecords(); }
async function addWhitelist() {
  try { await setGuardWhitelist(numeric(whitelistChat.value.trim(), true), numeric(whitelistUser.value.trim()), true); message.success("本群白名单已添加"); await loadRecords(); }
  catch (error) { report(error); }
}
function evidenceUrl(row: GuardRecord) {
  const url = row.payload?.evidence_link || "";
  return /^https:\/\/t\.me\/(?:c\/\d+|[a-zA-Z0-9_]+)\/\d+$/.test(url) ? url : undefined;
}

// ── 案件审核操作：保留全部条件分支与后果说明弹窗 ──
async function act(row: GuardRecord, action: "allow" | "blacklist" | "unban" | "ban" | "delete" | "kick" | "retry" | "removeWhite" | "removeBlack") {
  dialog.warning({
    title: action === "kick" ? "确认仅踢出（允许再次加入）" : action === "delete" ? "确认删除被举报消息" : "确认群管操作",
    content: action === "delete" ? "删除本案被举报的原消息，删除后无法恢复。用户权限及黑白名单由其他审核操作决定。" : action === "unban" && pendingReport(row) ? "忽略本次举报，保留记录，不修改成员权限或黑白名单。" : action === "unban" ? "仅撤销本次案件的封禁、禁言或警告，不新增白名单。其他限制及入群验证继续生效。" : action === "ban" ? "将该用户在本群封禁，并清理其在本群的全部历史消息；保留审核记录，不加入永久黑名单。" : action === "allow" ? "撤销本次限制并加入本群白名单。其他限制及入群验证继续生效，不会自动重新拉入群。" : action === "blacklist" ? "封禁并清理该用户在本群的全部历史消息，再将用户 ID 记入永久黑名单；其他群按共享开关生效。" : action === "removeBlack" ? "撤销全局永久拉黑记录。各群现有封禁仍需分别通过案件解除。" : action === "removeWhite" ? "删除本群白名单后，该用户会重新接受本群规则检测。" : "重试此前失败的操作，实际结果会记录在案件中。",
    ...(action === "kick" ? { content: "仅踢出该用户，允许其再次加入本群。" } : {}),
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

// 行内可用操作：与旧版按钮的条件分支一一对应
type RowAction = "allow" | "blacklist" | "unban" | "ban" | "delete" | "kick" | "retry" | "removeWhite" | "removeBlack";
const ROW_ACTION_TYPES: Record<RowAction, "default" | "primary" | "warning" | "error"> = {
  allow: "primary", unban: "default", retry: "default",
  kick: "warning", ban: "warning", delete: "warning",
  blacklist: "error", removeWhite: "error", removeBlack: "warning",
};
function rowActions(row: GuardRecord): { key: RowAction; label: string }[] {
  const list: { key: RowAction; label: string }[] = [];
  if (table.value === "cases") {
    if (canKick(row)) list.push({ key: "kick", label: "仅踢出" });
    if (row.action === "report" && row.payload?.message_id && row.payload?.report_message_deletion?.status !== "deleted" && row.payload?.ban_message_cleanup?.status !== "deleted") {
      list.push({ key: "delete", label: row.payload?.report_message_deletion?.error ? "重试删除被举报消息" : "删除被举报消息" });
    }
    if (row.status === "pending" || (row.status === "done" && ["mute", "warn"].includes(row.action || "") && !row.payload?.verification_failure)) {
      list.push({ key: "unban", label: pendingReport(row) ? "忽略本次举报" : row.status === "pending" ? "仅解除封禁" : row.action === "mute" ? "仅解除本次禁言" : "忽略本次警告" });
      if (!pendingReport(row)) list.push({ key: "allow", label: row.action === "warn" && row.status !== "pending" ? "加入本群白名单" : "解除并加入白名单" });
      if (row.status !== "pending" || pendingReport(row)) list.push({ key: "ban", label: "封禁并清理消息" });
      list.push({ key: "blacklist", label: "永久拉黑并入库标记" });
    }
    if (row.status === "execution_failed" || (row.payload?.ban_message_cleanup?.status === "pending" && row.payload.ban_message_cleanup.error)) {
      list.push({ key: "retry", label: row.status === "execution_failed" ? "重试" : "重试清理消息" });
    }
  } else if (table.value === "blacklist") {
    list.push({ key: "removeBlack", label: "撤销永久拉黑" });
  } else if (table.value === "whitelist") {
    list.push({ key: "removeWhite", label: "删除白名单" });
  }
  return list;
}
const columns = computed<DataTableColumns<GuardRecord>>(() => [
  { title: "群组 / 收件人", key: "chat_id", width: 180, render: row => String(row.chat_id || row.source_chat_id || row.target_id || "—") },
  { title: "用户 ID", key: "user_id", width: 150 },
  { title: "动作", key: "action", width: 150, render: row => ["kicked", "reviewing_kick"].includes(row.status || "") ? "仅踢出" : row.payload?.review_ban_confirmed ? "管理员封禁" : row.action ? recordActionNames[row.action] || actions.find(action => action.value === row.action)?.label || row.action : "—" },
  { title: "状态", key: "status", width: 190, render: recordStatus },
  { title: "时间（UTC）", key: "created_at", width: 190, render: row => row.updated_at || row.created_at || row.last_seen_at || "—" },
  { title: "错误", key: "error", width: 180, ellipsis: { tooltip: true } },
  {
    // 详情常驻；剩余操作 ≤2 个直接平铺，更多则收进下拉，避免一行塞满按钮
    title: "操作", key: "actions", width: 190, align: "right",
    render: row => {
      const acts = rowActions(row);
      const buttons = [h(NButton, { size: "small", quaternary: true, type: "primary", onClick: () => { detail.value = row; showDetail.value = true; } }, () => "详情")];
      if (!acts.length) return buttons;
      if (acts.length <= 2) {
        for (const item of acts) {
          buttons.push(h(NButton, { size: "small", quaternary: true, type: ROW_ACTION_TYPES[item.key], onClick: () => act(row, item.key) }, () => item.label));
        }
        return buttons;
      }
      buttons.push(h(NDropdown, {
        trigger: "click",
        options: acts.map(item => ({ key: item.key, label: item.label })),
        onSelect: (key: string | number) => act(row, key as RowAction),
      }, { default: () => h(NButton, { size: "small", quaternary: true }, () => `更多操作（${acts.length}）`) }));
      return buttons;
    },
  },
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
  nicknameEditor.value?.stop();
  window.removeEventListener("beforeunload", beforeUnload);
  document.removeEventListener("visibilitychange", flushHidden);
});
</script>

<style>
/*
 * 非 scoped 样式：表格列的 render 函数在 Naive UI 内部渲染上下文里执行，
 * 拿不到本组件的 scoped 属性，因此徽标/规则单元格样式使用页面级前缀类名，
 * 并全部走 --sb-* 令牌，深浅主题自动生效。
 */
.gpill { display: inline-flex; align-items: center; gap: 6px; padding: 2px 10px; border-radius: var(--sb-radius-pill); font-size: 12px; font-weight: 600; line-height: 18px; white-space: nowrap; }
.gpdot { width: 6px; height: 6px; border-radius: 50%; flex: none; }
.gpill.accent { background: var(--sb-accent-soft); color: var(--sb-accent); }
.gpill.accent .gpdot { background: var(--sb-accent); }
.gpill.success { background: var(--sb-success-soft); color: var(--sb-success); }
.gpill.success .gpdot { background: var(--sb-success); }
.gpill.warning { background: var(--sb-warning-soft); color: var(--sb-warning); }
.gpill.warning .gpdot { background: var(--sb-warning); }
.gpill.error { background: var(--sb-error-soft); color: var(--sb-error); }
.gpill.error .gpdot { background: var(--sb-error); }
.gpill.neutral { background: var(--sb-surface-3); color: var(--sb-text-2); }
.gpill.neutral .gpdot { background: var(--sb-text-3); }
.g-idx { color: var(--sb-text-3); font-variant-numeric: tabular-nums; }
.g-rid { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
.dot-info { background: var(--sb-accent); }
</style>

<style scoped>
/* 页面骨架：纵向卡片流 */
.guard-page { display: flex; flex-direction: column; gap: 16px; }

/* 页头：标题 20px/650 + 副标题 + 状态徽标 + 刷新 */
.page-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.page-title { font-size: 20px; font-weight: 650; }
.page-subtitle { margin-top: 4px; font-size: 13px; color: var(--sb-text-2); }
.head-actions { display: flex; align-items: center; gap: 12px; flex: none; }

/* 使用说明卡：accent 圆点 + 弱化长文案 */
.intro-card { display: flex; gap: 10px; border: 1px solid var(--sb-border); background: var(--sb-surface); border-radius: var(--sb-radius); padding: 12px 16px; }
.intro-card .gpdot { margin-top: 7px; }
.intro-card p { margin: 0; font-size: 12.5px; line-height: 1.8; color: var(--sb-text-2); }

/* 工具栏：左筛选右动作，窄屏换行 */
.page-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.toolbar-left { display: flex; align-items: center; gap: 12px; flex: 1; min-width: 0; flex-wrap: wrap; }
.sel-group { width: 280px; }
.sel-joined { width: 280px; }
.inp-chat { width: 250px; }
.sel-table { width: 190px; }
.inp-filter { width: 185px; }
.sel-status { width: 200px; }

/* 说明与状态行 */
.hint-line { margin: 6px 0 0; font-size: 12.5px; line-height: 1.7; color: var(--sb-text-2); }
.section-hint { margin: 0 0 12px; font-size: 12.5px; line-height: 1.7; color: var(--sb-text-2); }
.save-status { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; margin-top: 10px; }
.warn-text { color: var(--sb-warning); font-size: 12.5px; }
.alert-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }

/* 骨架屏与空态 */
.skel { padding: 4px 0; }
.skel-row { margin-bottom: 10px; }
.skel-row:last-child { margin-bottom: 0; }
.table-empty { padding: 40px 0; }

/* 分区小节：标题 + 一句说明 + 控件 */
.sub-block { margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--sb-border); }
.sub-block--first { border-top: none; padding-top: 0; margin-top: 16px; }
.sub-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-bottom: 10px; }
.sub-title { display: block; font-size: 14px; font-weight: 650; }
.sub-hint { display: block; margin-top: 2px; font-size: 12.5px; line-height: 1.6; color: var(--sb-text-2); max-width: 720px; }

/* 总开关条：surface-2 底突出第一操作 */
.master-strip { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; padding: 14px 16px; border: 1px solid var(--sb-border); border-radius: var(--sb-radius); background: var(--sb-surface-2); }
.master-label { display: flex; align-items: center; gap: 10px; font-weight: 650; }
.master-hint { margin-top: 4px; font-size: 12.5px; color: var(--sb-text-2); }

/* 开关网格与字段网格：自适应多列 */
.switch-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 0 24px; }
.field-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 0 24px; margin-top: 8px; }
.field-grid :deep(.n-input-number),
.field-grid :deep(.n-select) { width: 100%; }
.span-all { grid-column: 1 / -1; }
.block-gap-sm { margin-top: 10px; }

/* 白名单快捷添加行 */
.white-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
.white-row .hint-line { margin: 0; }

/* 分页 */
.pager { display: flex; justify-content: flex-end; margin-top: 12px; }

/* 审计留存 */
.retention-row { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; }
.retention-label { font-size: 13px; font-weight: 600; }
.retention-input { width: 160px; }
.retention-row .hint-line { margin: 0; }

/* 弹窗：footer 左取消右确定 */
.modal-hint { margin: 4px 0 0; font-size: 12.5px; color: var(--sb-text-2); }
.modal-footer { display: flex; align-items: center; justify-content: space-between; }
.modal-footer--end { justify-content: flex-end; }

/* 详情弹窗证据文本：保留换行、长串可断行 */
.evidence { white-space: pre-wrap; overflow-wrap: anywhere; }

/* ≤760px：筛选控件全宽、工具栏纵排 */
@media (max-width: 760px) {
  .page-head { flex-direction: column; }
  .head-actions { width: 100%; justify-content: space-between; }
  .page-toolbar { flex-wrap: wrap; }
  .sel-group, .sel-joined, .inp-chat, .sel-table, .inp-filter, .sel-status { width: 100%; }
  .sub-head { flex-direction: column; }
  .retention-input { flex: 1; }
}
</style>
