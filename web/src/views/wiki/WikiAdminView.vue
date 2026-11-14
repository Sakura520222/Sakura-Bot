<template>
  <div class="wiki-admin-page">
    <n-card size="small" class="wiki-toolbar">
      <div class="toolbar-row">
        <n-select
          :value="selected"
          :options="channelOptions"
          filterable
          placeholder="选择频道"
          aria-label="频道"
          class="channel-picker"
        :disabled="busy || coverBusy"
          @update:value="switchChannel"
        />
        <n-tag
          v-if="channel"
          :type="channel.enabled ? 'success' : 'default'"
          :bordered="false"
          >{{ channel.enabled ? "已启用" : "未启用" }}</n-tag
        >
        <n-button
          v-if="channel && !channel.enabled"
          type="primary"
          :disabled="busy"
          @click="enableWiki"
          >启用 Wiki</n-button
        >
        <n-button
          v-else-if="channel"
          :disabled="busy"
          @click="channel.active_job ? (tab = 'jobs') : startBuild()"
          >{{ channel.active_job ? "查看当前任务" : "开始构建" }}</n-button
        >
        <n-button quaternary :loading="busy" :disabled="coverBusy" @click="loadChannels"
          >刷新</n-button
        >
        <div class="toolbar-actions">
          <n-text
            v-if="draft && tab === 'config'"
            :type="saveFailed ? 'error' : undefined"
            class="save-state"
            >{{ saveLabel }}</n-text
          >
          <n-button
            v-if="draft && tab === 'config'"
            type="primary"
            :loading="configSaving"
            :disabled="busy"
            @click="saveConfig"
            >保存设置</n-button
          >
          <RouterLink to="/ai-config"
            ><n-button quaternary>模型设置</n-button></RouterLink
          >
          <RouterLink to="/wiki" target="_blank"
            ><n-button quaternary>公开 Wiki</n-button></RouterLink
          >
        </div>
      </div>
    </n-card>
    <n-alert
      v-if="error || saveFailed"
      type="error"
      title="操作未完成"
      :show-icon="true"
      >{{ error || saveProblem }}</n-alert
    >
    <n-alert
      v-if="notice"
      type="success"
      :show-icon="false"
      closable
      @close="notice = ''"
      >{{ notice }}</n-alert
    >
    <n-skeleton v-if="busy && !channel" :repeat="4" text />
    <n-card v-else-if="!channels.length && !error"
      ><n-empty description="暂无频道"
        ><template #extra
          ><RouterLink to="/channels"
            ><n-button>频道管理</n-button></RouterLink
          ></template
        ></n-empty
      ></n-card
    >
    <template v-if="channel && draft">
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab v-for="item in tabs" :key="item.key" :name="item.key">{{
          item.label
        }}</n-tab>
      </n-tabs>
      <template v-if="tab === 'config'">
        <WikiAccountSettings :origin="draft.public_base_url" />
        <n-card title="频道设置" size="small">
          <n-form
            label-placement="top"
            :show-feedback="false"
            :disabled="busy"
            @compositionstart="autosave?.setComposing(true)"
            @compositionend="autosave?.setComposing(false)"
          >
            <div class="settings-grid">
              <n-form-item label="启用 Wiki"
                ><n-switch v-model:value="draft.enabled"
              /></n-form-item>
              <n-form-item label="公开访问"
                ><n-switch
                  v-model:value="draft.public"
                  :disabled="!draft.enabled || !channel.access_verified"
              /></n-form-item>
              <n-form-item label="暂停自动更新"
                ><n-switch v-model:value="draft.paused"
              /></n-form-item>
              <n-form-item label="发布模式"
                ><n-select
                  v-model:value="draft.publication_mode"
                  :options="publicationOptions"
              /></n-form-item>
              <n-form-item label="更新频率"
                ><n-select
                  v-model:value="draft.frequency"
                  :options="frequencyOptions"
              /></n-form-item>
              <n-form-item label="时区"
                ><n-input
                  v-model:value="draft.timezone"
                  placeholder="Asia/Shanghai"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="公开频道简介">
                <n-input v-model:value="draft.description" type="textarea" :maxlength="280" />
              </n-form-item>
              <n-form-item label="公开频道封面">
                <n-space vertical style="width: 100%">
                  <n-input v-model:value="draft.cover_url" placeholder="备用 HTTPS 图片地址，可留空" :maxlength="2048" />
                  <input ref="coverInput" type="file" accept="image/png,image/jpeg" hidden @change="uploadCover" />
                  <n-space align="center">
                    <n-button :loading="coverBusy" :disabled="busy || coverBusy" @click="coverInput?.click()">
                      {{ channel?.cover_digest ? "替换本机图片" : "上传本机图片" }}
                    </n-button>
                    <n-button v-if="channel?.cover_digest" :disabled="busy || coverBusy" @click="changeCover()">
                      移除本机封面
                    </n-button>
                    <n-text depth="3">PNG / JPEG，最大 8 MiB</n-text>
                  </n-space>
                  <n-text v-if="channel?.cover_digest" depth="3">已使用上传的封面。移除后将使用备用地址。</n-text>
                  <n-text v-if="coverFeedback" :type="coverFeedback.error ? 'error' : 'success'" :role="coverFeedback.error ? 'alert' : 'status'">
                    {{ coverFeedback.text }}
                  </n-text>
                </n-space>
              </n-form-item>
              <n-form-item v-if="draft.frequency === 'cron'" label="Cron 表达式"
                ><n-input
                  v-model:value="draft.cron"
                  placeholder="0 3 * * *"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="内容语言"
                ><n-input
                  v-model:value="draft.language"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="历史起点（留空为全部）"
                ><n-date-picker
                  v-model:value="historyTimestamp"
                  type="datetime"
                  clearable
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="公开站点地址"
                ><n-input
                  v-model:value="draft.public_base_url"
                  :placeholder="siteOrigin"
                  @blur="saveConfig(false)"
              /></n-form-item>
              <n-form-item label="总结来源"
                ><n-checkbox v-model:checked="draft.include_summaries"
                  >使用已有总结</n-checkbox
                ></n-form-item
              >
              <n-form-item label="图片来源"
                ><n-checkbox v-model:checked="draft.include_images"
                  >使用已保存的 OCR 和描述</n-checkbox
                ></n-form-item
              >
              <n-form-item label="引用展示"
                ><n-space
                  ><n-checkbox v-model:checked="draft.show_quotes"
                    >原文摘要</n-checkbox
                  ><n-checkbox v-model:checked="draft.show_telegram_links"
                    >Telegram 原帖链接</n-checkbox
                  ></n-space
                ></n-form-item
              >
              <n-form-item label="消息类别"
                ><n-checkbox-group v-model:value="draft.message_types"
                  ><n-space
                    ><n-checkbox value="text">文字</n-checkbox
                    ><n-checkbox value="image">图片</n-checkbox
                    ><n-checkbox value="other">其他</n-checkbox></n-space
                  ></n-checkbox-group
                ></n-form-item
              >
              <n-form-item label="展示栏目"
                ><n-checkbox-group v-model:value="draft.columns"
                  ><n-space
                    ><n-checkbox value="pages">文章</n-checkbox
                    ><n-checkbox value="graph">知识地图</n-checkbox
                    ><n-checkbox value="timeline">时间轴</n-checkbox
                    ><n-checkbox value="profile">画像</n-checkbox></n-space
                  ></n-checkbox-group
                ></n-form-item
              >
              <n-form-item label="不公开的主题（每行一个）" class="wide"
                ><n-input
                  v-model:value="draft.hiddenTopics"
                  type="textarea"
                  :autosize="{ minRows: 2, maxRows: 5 }"
                  @blur="saveConfig(false)"
              /></n-form-item>
            </div>
          </n-form>
          <n-text v-if="!channel.access_verified" type="warning"
            >频道公开状态尚未确认，请检查 UserBot 连接。</n-text
          >
        </n-card>
        <n-card title="社区协作" size="small">
          <n-form label-placement="top" :show-feedback="false" :disabled="busy">
            <div class="settings-grid">
              <n-form-item label="允许读者提交词条修订">
                <div class="wiki-option"><n-switch :value="!!draft.proposals_enabled" @update:value="draft.proposals_enabled = $event" />
                  <n-text depth="3">Telegram 登录后可提交修订或纠错，审核并发布后才会公开。</n-text></div>
              </n-form-item>
              <n-form-item label="允许读者补充注解和来源">
                <div class="wiki-option"><n-switch :value="!!draft.annotations_enabled" :disabled="!draft.proposals_enabled" @update:value="draft.annotations_enabled = $event" />
                  <n-text depth="3">允许提交补充资料、争议或过期标记，同样需要审核。</n-text></div>
              </n-form-item>
              <n-form-item label="通知管理员审核">
                <div class="wiki-option"><n-switch :value="!!draft.proposal_notifications" :disabled="!draft.proposals_enabled" @update:value="draft.proposal_notifications = $event" />
                  <n-text depth="3">收到修订建议时，由主 Bot 通知管理员。</n-text></div>
              </n-form-item>
            </div>
          </n-form>
        </n-card>
        <n-card title="生成设置" size="small">
          <n-form label-placement="top" :show-feedback="false" :disabled="busy">
            <div class="settings-grid">
              <n-form-item label="AI 来源审校">
                <div class="wiki-option">
                  <n-switch :value="draft.ai_review_enabled !== false" aria-label="AI 来源审校" @update:value="setAIReview" />
                  <n-text depth="3">关闭后跳过 AI 审校与联网核查。发布仍检查引用来源的可用性。</n-text>
                </div>
              </n-form-item>
              <n-form-item label="自动联网核查"><n-switch :value="!!draft.fact_check && draft.ai_review_enabled !== false" :disabled="draft.ai_review_enabled === false" @update:value="draft.fact_check = $event" /></n-form-item>
              <n-form-item label="仅发布已获外部证据核实的修订"><n-switch :value="!!draft.fact_check_required" :disabled="draft.ai_review_enabled === false" @update:value="draft.fact_check_required = $event" /></n-form-item>
              <n-form-item label="温度"
                ><n-input-number
                  v-model:value="draft.temperature"
                  :min="0"
                  :max="1"
                  :step="0.1"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="每任务 Token 上限"
                ><n-input-number
                  v-model:value="draft.token_budget"
                  :min="1000"
                  :step="100000"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="单次最大输出 Token"
                ><n-input-number
                  v-model:value="draft.max_output_tokens"
                  :min="1024"
                  :max="262144"
                  :step="1024"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="发布快照保留数量"
                ><n-input-number
                  v-model:value="draft.retention_publications"
                  :min="2"
                  :max="200"
                  :step="1"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="同时处理批次"
                ><n-input-number
                  v-model:value="draft.concurrency"
                  :min="1"
                  :max="4"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="请求超时（秒）"
                ><n-input-number
                  v-model:value="draft.timeout_seconds"
                  :min="10"
                  :max="1800"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="重试次数"
                ><n-input-number
                  v-model:value="draft.retries"
                  :min="0"
                  :max="8"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="增量合并等待（秒）"
                ><n-input-number
                  v-model:value="draft.debounce_seconds"
                  :min="10"
                  :max="86400"
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="输入单价 / 百万 Token（可选）"
                ><n-input-number
                  v-model:value="draft.input_cost_per_million"
                  :min="0"
                  :step="0.01"
                  clearable
                  style="width: 100%"
              /></n-form-item>
              <n-form-item label="输出单价 / 百万 Token（可选）"
                ><n-input-number
                  v-model:value="draft.output_cost_per_million"
                  :min="0"
                  :step="0.01"
                  clearable
                  style="width: 100%"
              /></n-form-item>
            </div>
          </n-form>
        </n-card>
      </template>
      <WikiCommunityAdmin v-else-if="tab === 'community'" :key="proposalId || 'queue'" :identifier="proposalId" @back="clearProposal" />
      <template v-else-if="tab === 'jobs'">
        <n-card title="历史消息采集" size="small"
          ><n-space
            >已枚举 {{ channel.coverage.enumerated || 0 }} 条
            <n-divider vertical />原文归档
            {{ channel.coverage.indexed || 0 }} 条 <n-divider vertical />
            <WikiExclusions
              :channel="selected"
              :count="channel.coverage.excluded"
              @refreshed="refreshData" /></n-space
        ></n-card>
        <n-space
          ><n-button
            type="primary"
            :disabled="busy || !channel.enabled"
            @click="enqueue('backfill')"
            >全量回溯</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('update')"
            >增量更新</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reconcile')"
            >核对历史</n-button
          ><n-button
            :disabled="busy || !channel.enabled"
            @click="enqueue('reindex')"
            >重建索引</n-button
          ><n-button
            :disabled="busy || !channel.enabled || !!channel.active_job"
            @click="enqueue('albums')"
            >合并历史相册</n-button
          ><n-button :disabled="busy" @click="refreshData"
            >刷新状态</n-button
          ></n-space
        >
        <n-empty v-if="!jobs.length" description="暂无任务" />
        <n-card
          v-for="job in jobs"
          :id="`job-${job.id}`"
          :key="job.id"
          :title="jobLabels[job.kind] || job.kind"
          size="small"
        >
          <template #header-extra
            ><n-tag
              :bordered="false"
              :type="
                ['published', 'completed'].includes(job.status)
                  ? 'success'
                  : job.status === 'failed'
                    ? 'error'
                    : job.status === 'paused'
                      ? 'warning'
                      : 'info'
              "
              >{{ stateLabels[job.status] || job.status }}</n-tag
            ></template
          >
          <WikiJobProgress :job="job" @review-failures="openFailedDrafts(job.id)" />
          <template #action
            ><n-space
              ><n-button
                v-if="['pending', 'running', 'partial'].includes(job.status)"
                @click="jobControl(job.id, 'pause')"
                >暂停</n-button
              ><n-button
                v-if="job.status === 'paused'"
                @click="jobControl(job.id, 'resume')"
                >恢复</n-button
              ><n-button
                v-if="['failed', 'partial'].includes(job.status)"
                @click="jobControl(job.id, 'retry')"
                >立即重试</n-button
              ><n-button
                v-if="
                  job.status === 'completed' && job.progress.review_version &&
                  job.progress.review_complete && Number(job.progress.pages) > 0
                "
                type="primary"
                @click="publish(job.id)"
                >发布</n-button
              ></n-space
            ></template
          >
        </n-card>
      </template>
      <template v-else-if="tab === 'review'">
        <div ref="draftWorkspace" class="draft-workspace">
          <div class="draft-filters" role="search" aria-label="筛选草稿">
            <n-input v-model:value="draftSearch" clearable maxlength="160" placeholder="搜索标题或正文" aria-label="搜索草稿" @update:value="queueDraftSearch" @keydown.enter="applyDraftFilters" />
            <n-select v-model:value="draftStatus" :options="draftStatusOptions" aria-label="审校状态" @update:value="applyDraftFilters" />
            <n-button v-if="draftJob" secondary @click="clearDraftJob">仅查看此任务 ×</n-button>
            <n-button :loading="draftsLoading" @click="refreshDrafts">刷新草稿</n-button>
          </div>
          <div class="review-grid" :class="{ 'reading-draft': selectedDraft }">
            <div class="draft-index">
              <n-spin :show="draftsLoading">
                <n-alert v-if="draftsError" type="error">{{ draftsError }} <n-button text @click="refreshDrafts">重试</n-button></n-alert>
                <n-empty v-else-if="!revisions.length && !draftsLoading" description="没有符合条件的草稿" />
              <div class="revision-list">
                <button v-for="rev in revisions" :key="rev.id" type="button" class="draft-row"
                  :aria-current="selectedDraft === rev.id ? 'true' : undefined" @click="previewRevision(rev)">
                  <strong>{{ rev.title }}</strong>
                  <span v-if="rev.excerpt" class="draft-excerpt">{{ plainWikiText(rev.excerpt) }}</span>
                  <span class="draft-row-meta">
                    <n-tag size="small" :bordered="false" :type="revisionStatus(rev).type">{{ revisionStatus(rev).label }}</n-tag>
                    <span v-if="rev.locked">已锁定</span><span v-if="rev.hidden">已隐藏</span>
                    <time :datetime="rev.created_at">{{ formatDate(rev.created_at) }}</time>
                  </span>
                </button>
              </div>
              </n-spin>
              <nav class="draft-pagination" aria-label="草稿分页">
                <n-button size="small" :disabled="adminPage <= 1 || draftsLoading" @click="changeDraftPage(-1)">上一页</n-button>
                <span class="muted">第 {{ adminPage }} 页</span>
                <n-button size="small" :disabled="revisions.length < 50 || draftsLoading" @click="changeDraftPage(1)">下一页</n-button>
              </nav>
            </div>
            <section ref="draftReader" class="draft-reader" aria-label="草稿阅读区">
              <n-button v-if="selectedDraft" class="draft-back" text @click="clearDraftPreview">返回草稿列表</n-button>
              <n-skeleton v-if="previewLoading" :repeat="5" text />
              <n-alert v-else-if="previewError" type="error">{{ previewError }} <n-button v-if="previewRow" text @click="previewRevision(previewRow)">重试</n-button></n-alert>
              <n-empty v-else-if="!preview" description="选择一条草稿查看正文和审校结果" />
              <article v-else>
                <header class="draft-reader-header">
                  <div><h2>{{ preview.document.title }}</h2><span v-if="previewRow" class="muted">{{ formatDate(previewRow.created_at) }}</span></div>
                  <n-dropdown v-if="previewRow" trigger="click" :options="draftActions" @select="runDraftAction">
                    <n-button size="small" :disabled="busy">草稿操作</n-button>
                  </n-dropdown>
                </header>
                <n-alert v-if="previewRow?.manually_approved" type="success" title="已人工通过" />
                <n-alert v-else-if="preview.review && !preview.review.approved" type="warning" title="来源审校未通过">
                  <p v-for="reason in reviewReasons" :key="reason">{{ reason }}</p>
                  <n-space v-if="!previewRow?.rejected">
                    <n-popconfirm
                      v-if="canManuallyApprove"
                      positive-text="确认通过"
                      negative-text="取消"
                      @positive-click="approveRevision(preview!.id)"
                    >
                      <template #trigger>
                        <n-button size="small" type="primary" :disabled="busy">人工通过</n-button>
                      </template>
                      此修订将按频道的发布设置进入发布流程。
                    </n-popconfirm>
                    <n-button size="small" :disabled="busy" @click="retryRevisionReview(preview.id)">重新审校</n-button>
                  </n-space>
                </n-alert>
                <n-tabs v-model:value="draftReaderTab" type="line" aria-label="草稿详情">
                  <n-tab name="text">正文</n-tab>
                  <n-tab name="diff">修订对比</n-tab>
                  <n-tab name="settings">标题与别名</n-tab>
                </n-tabs>
                <template v-if="draftReaderTab === 'text'">
                  <section
                    v-for="(claim, index) in preview.document.claims"
                    :key="index"
                    class="claim"
                  >
                    <h3 v-if="!claim.key.startsWith('md:')">{{ claim.key }}</h3>
                    <n-tag v-if="claim.status !== 'current'" size="small">{{ statusLabels[claim.status] }}</n-tag>
                    <WikiMarkdown :text="claim.text" :channel="selected" :annotations="claim.community_notes" />
                    <n-alert v-if="reviewedClaims.has(index)" class="mt" type="warning" title="此段未通过审校">{{ reviewedClaims.get(index)?.reason }}</n-alert>
                    <n-collapse
                      ><n-collapse-item title="修订此段" :name="index"
                        ><n-input
                          :value="claimEdits[index] ?? claim.text"
                          @update:value="claimEdits[index] = $event"
                          type="textarea"
                          :autosize="{ minRows: 3, maxRows: 8 }"
                        /><n-button
                          class="mt"
                          :disabled="busy || !claimEdits[index]?.trim() || claimEdits[index] === claim.text"
                          @click="correctClaim(index, claimEdits[index])"
                          >提交修订</n-button
                        ></n-collapse-item
                      ><n-collapse-item v-if="claim.evidence.length" :title="`引用来源（${claim.evidence.length}）`" :name="`sources-${index}`">
                        <blockquote v-for="(ev, i) in claim.evidence" :key="i">
                          <p v-if="ev.quote">{{ ev.quote }}</p>
                          <n-button size="small" quaternary type="error" :disabled="busy" @click="hideSource(ev.source_id)">屏蔽此来源</n-button>
                        </blockquote>
                      </n-collapse-item
                      ></n-collapse
                    >
                  </section>
                </template>
                <template v-else-if="draftReaderTab === 'diff'">
                  <WikiRevisionDiff v-if="preview.changes.length" :changes="preview.changes" />
                  <n-empty v-else description="正文与上一版本一致" />
                  <p v-if="preview.changes_truncated" class="muted">仅显示前 100 处变化。</p>
                </template>
                  <n-form v-else label-placement="top" class="mt"
                    ><n-form-item label="标题"
                      ><n-input v-model:value="editTitle" /></n-form-item
                    ><n-form-item label="链接别名"
                      ><n-input v-model:value="editSlug" /></n-form-item
                    ><n-button @click="renamePage">保存标题与链接</n-button
                    ><n-form-item label="同义名称" class="mt"
                      ><n-input v-model:value="editAlias" /></n-form-item
                    ><n-button @click="bindAlias">合并别名</n-button></n-form>
              </article>
            </section>
          </div>
        </div>
      </template>
      <template v-else-if="tab === 'history'">
        <n-card title="发布记录" size="small"
          ><n-empty
            v-if="!publications.length"
            description="暂无发布记录"
          /><n-list v-else
            ><n-list-item v-for="pub in publications" :key="pub.id"
              ><n-thing
                :title="formatDate(pub.created_at)"
                :description="pub.id"
              /><template #suffix
                ><n-tag
                  v-if="pub.id === channel.published_revision"
                  type="success"
                  >当前版本</n-tag
                ><n-button v-else :disabled="busy" @click="rollback(pub.id)"
                  >回滚</n-button
                ></template
              ></n-list-item
            ></n-list
          ><template #action
            ><n-button
              type="error"
              secondary
              :disabled="busy || !channel.published_revision"
              @click="withdraw"
              >撤回发布</n-button
            ></template
          ></n-card
        >
        <n-card title="操作记录" size="small"
          ><n-empty v-if="!audit.length" description="暂无记录" /><n-list v-else
            ><n-list-item v-for="(item, index) in audit" :key="index"
              ><n-thing
                :title="item.action"
                :description="`${formatDate(item.created_at)} · ${item.actor} · ${item.target_id || ''}`" /></n-list-item></n-list
        ></n-card>
      </template>
      <n-card v-else title="清除 Wiki 数据" size="small"
        ><p>
          清除本频道的页面、修订、发布记录和任务，并关闭
          Wiki。原始消息归档保留。
        </p>
        <n-form-item :label="`输入频道 ID ${channel.id} 确认`"
          ><n-input
            v-model:value="purgeConfirm"
            :placeholder="channel.id"
            style="max-width: 360px" /></n-form-item
        ><n-button
          type="error"
          :disabled="busy || purgeConfirm !== channel.id"
          @click="purge"
          >清除频道 Wiki</n-button
        ></n-card
      >
      <n-space
        v-if="['jobs', 'history'].includes(tab)"
        justify="center"
        ><n-button
          :disabled="adminPage <= 1 || busy"
          @click="
            adminPage--;
            refreshData();
          "
          >上一页</n-button
        ><n-text>第 {{ adminPage }} 页</n-text
        ><n-button
          :disabled="
            busy ||
            (tab === 'jobs'
              ? jobs.length
              : Math.max(publications.length, audit.length)) < 50
          "
          @click="
            adminPage++;
            refreshData();
          "
          >下一页</n-button
        ></n-space
      >
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, nextTick, ref, shallowRef, watch, onMounted, onUnmounted } from "vue";
import { NPopconfirm } from "naive-ui";
import { useRoute, useRouter } from "vue-router";
import {
  wikiAdmin,
  formatDate,
  statusLabels,
  type Coverage,
  type Claim,
  type WikiJob as Job,
} from "../../api/wiki";
import {
  createAutosave,
  saveError,
  type Autosave,
} from "../../composables/autosave";

import WikiJobProgress from "./WikiJobProgress.vue";
import WikiExclusions from "./WikiExclusions.vue";
import WikiMarkdown from "./WikiMarkdown.vue";
import WikiRevisionDiff from "./WikiRevisionDiff.vue";
import { plainWikiText } from "./wiki-utils";
import WikiCommunityAdmin from "./WikiCommunityAdmin.vue";
import WikiAccountSettings from "./WikiAccountSettings.vue";
import { createWikiDraft } from "../../composables/wiki-admin";
interface Config {
  enabled: boolean;
  public: boolean;
  paused: boolean;
  publication_mode: string;
  fact_check?: boolean;
  fact_check_required?: boolean;
  ai_review_enabled?: boolean;
  proposals_enabled?: boolean;
  annotations_enabled?: boolean;
  proposal_notifications?: boolean;
  frequency: string;
  cron: string;
  timezone: string;
  history_since: string | null;
  include_summaries: boolean;
  include_images: boolean;
  message_types: string[];
  language: string;
  columns: string[];
  show_quotes: boolean;
  show_telegram_links: boolean;
  public_base_url: string;
  description?: string;
  cover_url?: string;
  hidden_topics: string[];
  temperature: number;
  timeout_seconds: number;
  retries: number;
  concurrency: number;
  token_budget: number;
  max_output_tokens: number;
  retention_publications: number;
  input_cost_per_million: number | null;
  output_cost_per_million: number | null;
  debounce_seconds: number;
}
interface Channel {
  id: string;
  title: string;
  enabled: boolean;
  access_verified: boolean;
  config: Config;
  coverage: Coverage;
  published_revision: string | null;
  active_job: string | null;
  cover_digest?: string | null;
}
interface Revision {
  id: string;
  page_id: string;
  title: string;
  slug: string;
  approved: boolean;
  rejected: boolean;
  locked: boolean;
  hidden: boolean;
  created_at: string;
  excerpt?: string;
  review_result?: boolean | number | null;
  manually_approved?: boolean | number;
}
const route = useRoute();
const router = useRouter();
const proposalId = computed(() => typeof route.query.proposal === "string" && /^[a-f0-9]{32}$/.test(route.query.proposal) ? route.query.proposal : undefined);
function clearProposal() {
  const query = { ...route.query };
  delete query.proposal;
  void router.replace({ query });
}
const channels = ref<Channel[]>([]),
  selected = ref(localStorage.getItem("sakura_wiki_channel") || ""),
  channel = ref<Channel>(),
  busy = ref(false),
  error = ref(""),
  notice = ref(""),
  tab = ref(proposalId.value ? "community" : route.query.tab === "review" ? "review" : route.query.tab === "jobs" ? "jobs" : "config"),
  purgeConfirm = ref("");
const adminPage = ref(1);
const jobs = ref<Job[]>([]),
  revisions = ref<Revision[]>([]),
  publications = ref<{ id: string; created_at: string }[]>([]),
  audit = ref<
    { action: string; actor: string; target_id: string; created_at: string }[]
  >([]),
  preview = ref<{
    id: string;
    document: { title: string; claims: Claim[] };
    changes: { before: string | null; after: string | null }[];
    changes_truncated?: boolean;
    review?: {
      approved: boolean;
      created_at: string;
      result: {
        batches?: { reason: string; claims: { index: number; reason: string; status?: string }[] }[];
        fact_checks?: { index: number; status: string; reason: string }[];
      };
    };
  }>(),
  previewRow = ref<Revision>(),
  editTitle = ref(""),
  editSlug = ref(""),
  editAlias = ref("");
const draftWorkspace = ref<HTMLElement>();
const draftReader = ref<HTMLElement>();
const draftSearch = ref("");
const draftJob = ref(typeof route.query.job === "string" && /^[a-f0-9]{32}$/.test(route.query.job) ? route.query.job : "");
const draftStatus = ref(draftJob.value ? "failed" : "all");
const draftReaderTab = ref("text");
const draftsLoading = ref(false), draftsError = ref("");
const previewLoading = ref(false), previewError = ref("");
const selectedDraft = ref("");
const claimEdits = ref<Record<number, string>>({});
let listRequest = 0, detailRequest = 0;
let draftSearchTimer: ReturnType<typeof setTimeout> | undefined;
const draftStatusOptions = [
  { label: "全部状态", value: "all" }, { label: "审校未通过", value: "failed" },
  { label: "待审校", value: "pending" }, { label: "审校通过或已准予发布", value: "passed" },
  { label: "已拒绝", value: "rejected" },
];
function revisionStatus(row: Revision) {
  if (row.rejected) return { type: "error", label: "已拒绝" } as const;
  if (row.manually_approved) return { type: "success", label: "人工通过" } as const;
  if (row.review_result === false || row.review_result === 0) return { type: "warning", label: "审校未通过" } as const;
  if (row.approved) return { type: "success", label: "已准予发布" } as const;
  if (row.review_result) return { type: "success", label: "审校通过" } as const;
  return { type: "default", label: "待审校" } as const;
}
const canManuallyApprove = computed(() => {
  const row = previewRow.value;
  return row && !row.rejected && !row.hidden && !row.approved && !row.review_result && !row.manually_approved;
});
const draftActions = computed(() => {
  const row = previewRow.value;
  return row ? [
    { label: row.locked ? "解锁词条" : "锁定词条", key: row.locked ? "unlock" : "lock" },
    { label: row.hidden ? "恢复展示" : "隐藏词条", key: row.hidden ? "show" : "hide" },
    { label: "拒绝此修订", key: "reject", disabled: row.rejected },
    { label: "重新生成词条", key: "regenerate", disabled: !!channel.value?.active_job },
  ] : [];
});

interface FormSettings extends Config {
  hiddenTopics: string;
}
const autosave = shallowRef<Autosave<{ settings: FormSettings }> | null>(null);
const coverInput = ref<HTMLInputElement>();
const coverBusy = ref(false);
const coverFeedback = ref<{ text: string; error?: boolean }>();
const draft = computed(() => autosave.value?.values.settings);
const reviewedClaims = computed(() => new Map(
  preview.value?.review && !preview.value.review.approved ? [
    ...(preview.value.review.result.fact_checks ?? []).filter(claim => ["rejected", "disputed", "outdated"].includes(claim.status)),
    ...(preview.value.review.result.batches ?? []).flatMap(batch => batch.claims.filter(claim => claim.status !== "supported")),
  ].map(claim => [claim.index, claim] as const) : [],
));
const reviewReasons = computed(() => [...new Set(
  preview.value?.review?.result.batches?.map(batch => batch.reason).filter(Boolean) ?? [],
)]);
const siteOrigin = window.location.origin;
const channelOptions = computed(() =>
  channels.value.map((ch) => ({ label: ch.title, value: ch.id })),
);
const publicationOptions = [
  { label: "手动发布", value: "review" },
  { label: "自动发布", value: "auto" },
];
const frequencyOptions = [
  { label: "仅手动", value: "manual" },
  { label: "每日", value: "daily" },
  { label: "每周", value: "weekly" },
  { label: "自定义 Cron", value: "cron" },
];
const configSaving = computed(
  () => autosave.value?.states.settings.status === "saving",
);
const saveFailed = computed(() =>
  ["error", "invalid"].includes(autosave.value?.states.settings.status || ""),
);
const saveProblem = computed(() => autosave.value?.states.settings.error || "");
const saveLabel = computed(
  () =>
    ({
      saved: "已保存",
      pending: "等待保存",
      saving: "保存中…",
      error: "保存失败",
      invalid: "请检查输入",
    })[autosave.value?.states.settings.status || "saved"],
);
const historyTimestamp = computed<number | null>({
  get: () =>
    draft.value?.history_since
      ? new Date(draft.value.history_since).getTime()
      : null,
  set: (value) => {
    if (draft.value)
      draft.value.history_since =
        value === null ? null : new Date(value).toISOString();
  },
});
let stopDraftCache: (() => void) | undefined;
function formSettings(config: Config): FormSettings {
  return {
    ...createWikiDraft(config),
    hiddenTopics: config.hidden_topics.join("\n"),
  };
}
function configPayload(settings: FormSettings): Config {
  const { hiddenTopics, ...config } = createWikiDraft(settings);
  const entered = config.public_base_url.trim() || siteOrigin;
  const address = new URL(
    entered.includes("://") ? entered : `${location.protocol}//${entered}`,
  );
  if (
    !["http:", "https:"].includes(address.protocol) ||
    address.username ||
    address.password
  )
    throw new Error("请填写有效的公开站点地址");
  return {
    ...config,
    public_base_url: address.origin,
    hidden_topics: hiddenTopics
      .split("\n")
      .map((value) => value.trim())
      .filter(Boolean),
  };
}
function applyChannel(loaded: Channel) {
  coverFeedback.value = undefined;
  stopDraftCache?.();
  autosave.value?.stop();
  channel.value = loaded;
  const owner = loaded.id,
    cacheKey = `sakura_wiki_draft_${owner}`;
  const controller = createAutosave(
    { settings: formSettings(loaded.config) },
    {
      delay: 500,
      validate: (_field, values) => {
        try {
          configPayload(values);
          return undefined;
        } catch {
          return "请填写有效的公开站点地址";
        }
      },
      save: async (changes) => {
        const submitted = changes.settings!;
        const saved = await wikiAdmin<Channel>(
          `/channels/${owner}/config`,
          "PUT",
          configPayload(submitted),
        );
        if (selected.value === owner) {
          const coverDigest = channel.value?.cover_digest;
          channel.value = { ...saved, cover_digest: coverDigest };
          channels.value = channels.value.map((ch) =>
            ch.id === owner ? { ...saved, cover_digest: ch.cover_digest } : ch,
          );
        }
        return {
          settings: {
            ...formSettings(saved.config),
            hiddenTopics: submitted.hiddenTopics,
          },
        };
      },
    },
  );
  autosave.value = controller;
  stopDraftCache = watch(
    () => [
      JSON.stringify(controller.values.settings),
      controller.states.settings.status,
      controller.active.value,
    ],
    () => {
      if (controller.hasUnsaved())
        sessionStorage.setItem(
          cacheKey,
          JSON.stringify(controller.values.settings),
        );
      else sessionStorage.removeItem(cacheKey);
    },
    { flush: "sync" },
  );
  const cached = sessionStorage.getItem(cacheKey);
  if (cached) {
    try {
      Object.assign(controller.values.settings, JSON.parse(cached));
    } catch {
      sessionStorage.removeItem(cacheKey);
    }
  }
  if (!controller.values.settings.public_base_url)
    controller.values.settings.public_base_url = siteOrigin;
}
function uploadCover(event: Event) {
  const input = event.currentTarget as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (file) void changeCover(file);
}
async function changeCover(file?: File) {
  const owner = selected.value;
  if (!owner || busy.value || coverBusy.value) return;
  if (file && file.size > 8 * 1024 * 1024) {
    coverFeedback.value = { text: "频道封面不能超过 8 MiB", error: true };
    return;
  }
  coverBusy.value = true;
  coverFeedback.value = undefined;
  try {
    const saved = await wikiAdmin<{ cover_digest: string | null }>(
      `/channels/${owner}/cover`, file ? "PUT" : "DELETE", file,
      file ? { headers: { "Content-Type": file.type || "application/octet-stream" }, timeout: 120_000 } : {},
    );
    channels.value = channels.value.map((ch) => ch.id === owner ? { ...ch, ...saved } : ch);
    if (selected.value === owner && channel.value?.id === owner) {
      channel.value = { ...channel.value, ...saved };
      coverFeedback.value = { text: file ? "封面已上传" : "本机封面已移除" };
    }
  } catch (err) {
    if (selected.value === owner) coverFeedback.value = { text: problem(err), error: true };
  } finally {
    coverBusy.value = false;
  }
}
async function switchChannel(value: string) {
  if (coverBusy.value || value === selected.value) return;
  await autosave.value?.flush();
  selected.value = value;
  localStorage.setItem("sakura_wiki_channel", value);
  adminPage.value = 1;
  draftJob.value = "";
  draftSearch.value = "";
  draftStatus.value = "all";
  await loadChannel();
}

let timer: ReturnType<typeof setInterval> | undefined;
const tabs = [
  { key: "community", label: "社区与联网" },
  { key: "config", label: "设置" },
  { key: "jobs", label: "任务" },
  { key: "review", label: "草稿" },
  { key: "history", label: "发布记录" },
  { key: "purge", label: "数据维护" },
];
const jobLabels: Record<string, string> = {
  proposal: "投稿审校",
  backfill: "全量历史回溯",
  update: "增量更新",
  reconcile: "历史核对",
  reindex: "重建索引",
  albums: "合并历史相册",
  page_edit: "词条修订",
  page: "页面重生成",
};
const stateLabels: Record<string, string> = {
  pending: "排队中",
  running: "处理中",
  partial: "等待续跑",
  paused: "已暂停",
  failed: "失败",
  completed: "已生成待发布",
  published: "已发布",
};
function root() {
  return `/channels/${selected.value}`;
}
function problem(err: unknown) {
  return saveError(err);
}
async function perform(fn: () => Promise<unknown>, message = "操作完成") {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
    notice.value = message;
    if (tab.value !== "config") await refreshData();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function loadChannels() {
  if (autosave.value?.hasUnsaved() && !(await autosave.value.flush(true)))
    return;
  busy.value = true;
  error.value = "";
  try {
    channels.value = await wikiAdmin<Channel[]>("/channels");
    if (typeof route.query.channel === "string" && channels.value.some((ch) => ch.id === route.query.channel)) selected.value = route.query.channel;
    if (!channels.value.some((ch) => ch.id === selected.value))
      selected.value = channels.value[0]?.id || "";
    if (selected.value) await loadChannel();
    await scrollToJob();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function scrollToJob() {
  if (tab.value !== "jobs" || !/^#job-[a-f0-9]{32}$/.test(route.hash)) return;
  await nextTick();
  document.getElementById(route.hash.slice(1))?.scrollIntoView({ block: "center" });
}
watch(proposalId, (id) => { if (id) tab.value = "community"; });
watch(() => [route.query.tab, route.query.channel, route.hash], async () => {
  if (route.query.tab !== "jobs") return;
  try {
    tab.value = "jobs";
    if (typeof route.query.channel === "string" && channels.value.some((ch) => ch.id === route.query.channel) && selected.value !== route.query.channel) await switchChannel(route.query.channel);
    else await refreshData();
    await scrollToJob();
  } catch (cause) { error.value = problem(cause); }
});
async function loadChannel() {
  if (!selected.value) return;
  const owner = selected.value;
  busy.value = true;
  error.value = "";
  notice.value = "";
  clearDraftPreview();
  revisions.value = [];
  try {
    const loaded = await wikiAdmin<Channel>(`/channels/${owner}/config`);
    if (selected.value !== owner) return;
    applyChannel(loaded);
    localStorage.setItem("sakura_wiki_channel", owner);
    if (tab.value !== "config") await refreshData();
  } catch (err) {
    error.value = problem(err);
  } finally {
    busy.value = false;
  }
}
async function refreshData() {
  if (!selected.value) return;
  const current = selected.value;
  const suffix = `?page=${adminPage.value}`;
  if (tab.value === "jobs") {
    const [currentJobs, currentChannel] = await Promise.all([
      wikiAdmin<Job[]>(`${root()}/jobs${suffix}`),
      wikiAdmin<Channel>(`${root()}/config`),
    ]);
    if (selected.value === current) {
      jobs.value = currentJobs;
      channel.value = currentChannel;
    }
  } else if (tab.value === "review") {
    await refreshDrafts();
  } else if (tab.value === "history") {
    const [pubs, actions] = await Promise.all([
      wikiAdmin<typeof publications.value>(`${root()}/publications${suffix}`),
      wikiAdmin<typeof audit.value>(`${root()}/audit${suffix}`),
    ]);
    if (selected.value === current) {
      publications.value = pubs;
      audit.value = actions;
    }
  }
}
function setAIReview(enabled: boolean) {
  if (!draft.value) return;
  draft.value.ai_review_enabled = enabled;
  if (!enabled) draft.value.fact_check_required = false;
}
async function saveConfig(showNotice = true) {
  const saved = await autosave.value?.flush(true);
  if (saved && showNotice) notice.value = "设置已保存";
  return !!saved;
}
async function enqueue(kind: string, page_id?: string) {
  if (!(await saveConfig(false))) return;
  return perform(
    () => wikiAdmin(`${root()}/jobs`, "POST", { kind, page_id }),
    "后台任务已排队",
  );
}
async function enableWiki() {
  if (!draft.value) return;
  draft.value.enabled = true;
  if (await saveConfig(false)) notice.value = "Wiki 已启用";
}
async function startBuild() {
  if (!(await saveConfig(false))) return;
  await enqueue("backfill");
  if (!error.value) tab.value = "jobs";
}
async function jobControl(id: string, action: string) {
  if (action !== "pause" && !(await saveConfig(false))) return;
  return perform(() => wikiAdmin(`${root()}/jobs/${id}/${action}`, "POST"));
}
function publish(job_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/publish`, "POST", { job_id }),
    "知识快照已发布",
  );
}
function retryRevisionReview(id: string) {
  return perform(() => wikiAdmin(`${root()}/revisions/${id}/review/retry`, "POST"), "已安排重新审校");
}
function approveRevision(id: string) {
  return perform(() => wikiAdmin(`${root()}/revisions/${id}/review/approve`, "POST"), "修订已人工通过");
}
function rollback(publication_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/rollback`, "POST", { publication_id }),
    "已切换发布快照",
  );
}
function withdraw() {
  return perform(
    () => wikiAdmin(`${root()}/withdraw`, "POST"),
    "频道发布已撤回",
  );
}
function reject(id: string) {
  return perform(() => wikiAdmin(`${root()}/revisions/${id}/reject`, "POST"));
}
function hideSource(source_id: string) {
  return perform(
    () => wikiAdmin(`${root()}/hide-source`, "POST", { source_id }),
    "来源已屏蔽，公开引用立即停止",
  );
}
function pageAction(rev: Revision, action: string) {
  return perform(() =>
    wikiAdmin(`${root()}/pages/${rev.page_id}`, "POST", { action }),
  );
}
async function previewRevision(rev: Revision) {
  const request = ++detailRequest;
  const owner = selected.value;
  selectedDraft.value = rev.id;
  previewRow.value = rev;
  preview.value = undefined;
  previewError.value = "";
  previewLoading.value = true;
  draftReaderTab.value = "text";
  claimEdits.value = {};
  if (draftReader.value) draftReader.value.scrollTop = 0;
  if (window.matchMedia("(max-width: 800px)").matches) {
    void nextTick(() => draftWorkspace.value?.scrollIntoView({ block: "start" }));
  }
  const query = draftJob.value ? `?job_id=${draftJob.value}` : "";
  try {
    const result = await wikiAdmin<NonNullable<typeof preview.value>>(`${root()}/revisions/${rev.id}${query}`);
    if (request !== detailRequest || owner !== selected.value) return;
    preview.value = result;
    editTitle.value = rev.title;
    editSlug.value = rev.slug;
    editAlias.value = "";
  } catch (err) {
    if (request === detailRequest) previewError.value = problem(err);
  } finally {
    if (request === detailRequest) previewLoading.value = false;
  }
}
function clearDraftPreview() {
  detailRequest++;
  selectedDraft.value = "";
  preview.value = undefined;
  previewRow.value = undefined;
  previewLoading.value = false;
  previewError.value = "";
}
async function refreshDrafts() {
  if (tab.value !== "review") return;
  const request = ++listRequest;
  const owner = selected.value;
  draftsLoading.value = true;
  draftsError.value = "";
  const query = new URLSearchParams({ page: String(adminPage.value), q: draftSearch.value, status: draftStatus.value });
  if (draftJob.value) query.set("job_id", draftJob.value);
  try {
    const rows = await wikiAdmin<Revision[]>(`${root()}/revisions?${query}`);
    if (request !== listRequest || owner !== selected.value || tab.value !== "review") return;
    revisions.value = rows;
    if (selectedDraft.value) {
      const current = rows.find(row => row.id === selectedDraft.value);
      if (current) previewRow.value = current;
      else clearDraftPreview();
    }
  } catch (cause) {
    if (request === listRequest) draftsError.value = problem(cause);
  } finally {
    if (request === listRequest) draftsLoading.value = false;
  }
}
function applyDraftFilters() {
  clearTimeout(draftSearchTimer);
  adminPage.value = 1;
  clearDraftPreview();
  void refreshDrafts();
}
function queueDraftSearch() {
  clearTimeout(draftSearchTimer);
  draftSearchTimer = setTimeout(applyDraftFilters, 250);
}
function changeDraftPage(step: number) {
  adminPage.value += step;
  clearDraftPreview();
  void refreshDrafts();
}
function clearDraftJob() {
  draftJob.value = "";
  applyDraftFilters();
  if (route.query.job) {
    const query = { ...route.query };
    delete query.job;
    void router.replace({ query });
  }
}
async function openFailedDrafts(job: string) {
  draftJob.value = job;
  draftStatus.value = "failed";
  draftSearch.value = "";
  clearDraftPreview();
  tab.value = "review";
  await nextTick();
  draftWorkspace.value?.scrollIntoView({ block: "start" });
}
function runDraftAction(action: string) {
  const row = previewRow.value;
  if (!row) return;
  if (action === "reject") return reject(row.id);
  if (action === "regenerate") return enqueue("page", row.page_id);
  return pageAction(row, action);
}
function renamePage() {
  if (previewRow.value)
    return perform(() =>
      wikiAdmin(`${root()}/pages/${previewRow.value!.page_id}`, "POST", {
        action: "rename",
        title: editTitle.value,
        slug: editSlug.value,
      }),
    );
}
function bindAlias() {
  if (previewRow.value)
    return perform(() =>
      wikiAdmin(`${root()}/pages/${previewRow.value!.page_id}`, "POST", {
        action: "merge",
        alias: editAlias.value,
      }),
    );
}
function correctClaim(claim: number, text: string) {
  if (preview.value)
    return perform(
      () =>
        wikiAdmin(`${root()}/revisions/${preview.value!.id}/correct`, "POST", {
          claim,
          text,
        }),
      "已提交词条审校，可在任务页查看进度",
    );
}
async function purge() {
  await perform(
    () =>
      wikiAdmin(`${root()}/purge`, "POST", {
        confirm_channel: purgeConfirm.value,
      }),
    "Wiki 数据已清除并关闭",
  );
  purgeConfirm.value = "";
  await loadChannel();
}
watch(tab, () => {
  adminPage.value = 1;
  clearTimeout(draftSearchTimer);
  if (tab.value !== "config" && channel.value)
    refreshData().catch((err) => {
      error.value = problem(err);
    });
});
onMounted(() => {
  loadChannels();
  timer = setInterval(() => {
    if (selected.value && tab.value === "jobs" && !busy.value)
      refreshData().catch((err) => {
        error.value = problem(err);
      });
  }, 2000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
  clearTimeout(draftSearchTimer);
  void autosave.value?.flush();
  stopDraftCache?.();
  autosave.value?.stop();
});
</script>
<style scoped>
.wiki-admin-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  color: var(--sb-text);
  padding: 20px 24px;
}
.wiki-toolbar {
  position: sticky;
  top: 0;
  z-index: 20;
  box-shadow: 0 2px 8px rgb(0 0 0 / 4%);
}
.toolbar-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.channel-picker {
  width: 300px;
  max-width: 100%;
}
.toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  flex-wrap: wrap;
}
.save-state {
  font-size: 12px;
  white-space: nowrap;
}
.settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
}
.settings-grid .wide {
  grid-column: 1/-1;
}
.wiki-option { display: grid; justify-items: start; gap: 6px; }
.job-stats {
  margin: 16px 0;
}
.draft-workspace { scroll-margin-top: 100px; }
.draft-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.draft-filters .n-input { flex: 1 1 240px; }
.draft-filters .n-select { width: min(100%, 210px); }
.review-grid {
  display: grid;
  grid-template-columns: minmax(230px, 30%) minmax(0, 1fr);
  border: 1px solid var(--sb-border);
  border-radius: 12px;
  background: var(--sb-surface);
}
.draft-index,
.draft-reader {
  min-width: 0;
  max-height: max(380px, calc(100dvh - 245px));
  overflow-y: auto;
  overscroll-behavior: contain;
}
.draft-index { border-inline-end: 1px solid var(--sb-border); }
.draft-index :deep(.n-empty) { padding: 32px 12px; }
.draft-reader { padding: clamp(16px, 2.5vw, 36px); }
.draft-reader > article { max-width: 82ch; margin-inline: auto; }
.draft-reader > .n-empty { margin-block: 56px; }
.draft-reader-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 16px;
}
.draft-reader-header h2 { margin: 0 0 4px; font-size: 22px; overflow-wrap: anywhere; }
.draft-back { display: none; }
.revision-list {
  display: grid;
}
.draft-row {
  display: grid;
  gap: 6px;
  text-align: start;
  padding: 12px 14px;
  font: inherit;
  cursor: pointer;
  color: var(--sb-text);
  background: transparent;
  border: 0;
  border-bottom: 1px solid var(--sb-border);
}
.draft-row:hover { background: var(--sb-surface-2); }
.draft-row[aria-current] { background: var(--sb-accent-soft); box-shadow: inset 3px 0 var(--sb-accent); }
.draft-row:focus-visible { outline: 2px solid var(--sb-accent); outline-offset: -2px; }
.draft-row strong { overflow-wrap: anywhere; }
.draft-excerpt {
  color: var(--sb-text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.draft-row-meta, .draft-pagination { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; color: var(--sb-text-2); font-size: 12px; }
.draft-pagination { justify-content: space-between; padding: 10px 12px; }
.claim {
  padding: 12px 0;
  border-bottom: 1px solid var(--sb-border);
}
.claim h3 {
  margin: 0 0 8px;
  font-size: 16px;
}
.claim p {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.claim blockquote {
  margin: 12px 0;
  padding: 10px 14px;
  background: var(--sb-surface-2);
  border-left: 3px solid var(--sb-border);
}
.claim blockquote .n-text {
  display: block;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.muted {
  color: var(--sb-text-2);
  font-size: 12px;
}
.mt {
  margin-top: 12px;
}
@media (min-width: 1600px) {
  .settings-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
@media (max-width: 800px) {
  .wiki-admin-page {
    padding: 16px;
  }
  .settings-grid,
  .review-grid {
    grid-template-columns: 1fr;
  }
  .draft-index, .draft-reader { max-height: none; overflow: visible; border: 0; }
  .review-grid:not(.reading-draft) .draft-reader, .reading-draft .draft-index { display: none; }
  .draft-back { display: inline-flex; margin-bottom: 14px; }
  .toolbar-actions {
    margin-left: 0;
    width: 100%;
  }
  .channel-picker {
    width: 100%;
  }
  .wiki-toolbar {
    top: 0;
  }
}
</style>
