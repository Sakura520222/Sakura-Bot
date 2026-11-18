// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	stdhtml "html"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// MaxProposalsPerDayPerUser 是单个读者 24 小时滚动窗口内的最大投稿次数（10 次）。
	MaxProposalsPerDayPerUser = 10

	inPublishedRevisionSQL = `(r.id=e.revision_id OR JSON_CONTAINS(JSON_EXTRACT(r.document,'$.community_contributions'), JSON_QUOTE(e.id)))`
)

// ProposalLabels 定义全部 7 种社区投稿类型的中文展示名称（对齐 community.py:PROPOSAL_LABELS）。
var ProposalLabels = map[string]string{
	"edit":       "正文修订",
	"correction": "纠错",
	"annotation": "注解",
	"source":     "补充来源",
	"dispute":    "争议标注",
	"outdated":   "过期标注",
	"fact_check": "事实核查",
}

var (
	reSelectionMdLink   = regexp.MustCompile(`!?\[([^\]\n]*)\]\([^\n)]*\)`)
	reSelectionWikiLink = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
	reSelectionStyle    = regexp.MustCompile("[`*_~]")
)

// ProposalInput 表示读者提交的单条词条修订建议或段落注解输入契约（对齐 community.py:Proposal）。
type ProposalInput struct {
	Revision          string   `json:"revision"`
	Kind              string   `json:"kind"`
	Claim             *int     `json:"claim"`
	Text              string   `json:"text"`
	Sources           []string `json:"sources"`
	Excerpt           string   `json:"excerpt"`
	ExcerptOccurrence *int     `json:"excerpt_occurrence"`
	AnnotationIndex   *int     `json:"annotation_index"`
}

// ValidateAndNormalize 执行字段边界、空字节、注解索引一致性与 SSRF 公网 URL 去重校验。
func (p *ProposalInput) ValidateAndNormalize() error {
	if !reHex32.MatchString(p.Revision) {
		return errors.New("无效的基准修订 ID")
	}
	if _, ok := ProposalLabels[p.Kind]; !ok {
		return fmt.Errorf("不支持的投稿类型: %q", p.Kind)
	}
	if p.Claim != nil && (*p.Claim < 0 || *p.Claim > 10000) {
		return errors.New("段落序号超出有效范围")
	}
	if strings.ContainsRune(p.Text, '\x00') || strings.ContainsRune(p.Excerpt, '\x00') {
		return errors.New("内容含无效字符")
	}
	p.Text = strings.TrimSpace(p.Text)
	if utf8.RuneCountInString(p.Text) > 10000 {
		return errors.New("投稿内容不能超过 10000 个字符")
	}
	if utf8.RuneCountInString(p.Excerpt) > 500 {
		return errors.New("划选原文不能超过 500 个字符")
	}
	if p.ExcerptOccurrence != nil && (*p.ExcerptOccurrence < 0 || *p.ExcerptOccurrence > 10000) {
		return errors.New("划选位置序号无效")
	}
	if p.AnnotationIndex != nil {
		if *p.AnnotationIndex < 0 {
			return errors.New("注解序号无效")
		}
		if p.Kind != "annotation" {
			return errors.New("修改注解时请选择注解类型")
		}
	}
	if p.Text == "" {
		if p.AnnotationIndex == nil {
			return errors.New("内容不能为空")
		}
		p.Sources = []string{}
	}
	if len(p.Sources) > 5 {
		return errors.New("最多附加 5 个来源链接")
	}
	normalizedURLs := make([]string, 0, len(p.Sources))
	for _, rawURL := range p.Sources {
		cleanURL, err := PublicURL(rawURL)
		if err != nil {
			return fmt.Errorf("来源链接无效: %w", err)
		}
		normalizedURLs = append(normalizedURLs, cleanURL)
	}
	p.Sources = UniqueStrings(normalizedURLs)
	if (p.Kind == "source" || p.Kind == "fact_check") && len(p.Sources) == 0 {
		return errors.New("请提供来源链接")
	}
	return nil
}

// PayloadMap 序列化为存入 wiki_edit_proposals.payload 的标准字典。
func (p ProposalInput) PayloadMap(diff string) map[string]any {
	var claimVal, occVal, annIdxVal any
	if p.Claim != nil {
		claimVal = *p.Claim
	}
	if p.ExcerptOccurrence != nil {
		occVal = *p.ExcerptOccurrence
	}
	if p.AnnotationIndex != nil {
		annIdxVal = *p.AnnotationIndex
	}
	srcs := p.Sources
	if srcs == nil {
		srcs = []string{}
	}
	return map[string]any{
		"revision":           p.Revision,
		"kind":               p.Kind,
		"claim":              claimVal,
		"text":               p.Text,
		"sources":            srcs,
		"excerpt":            p.Excerpt,
		"excerpt_occurrence": occVal,
		"annotation_index":   annIdxVal,
		"diff":               diff,
	}
}

// SelectionText 剥离 Markdown 链接/图片、Wiki 双括号链接、HTML 实体与行内样式标记后压缩空白（对齐 community.py:selection_text）。
func SelectionText(value string) string {
	s := reSelectionMdLink.ReplaceAllString(value, "$1")
	s = reSelectionWikiLink.ReplaceAllString(s, "$1")
	s = stdhtml.UnescapeString(s)
	s = reSelectionStyle.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func proposalOriginalText(doc map[string]any, claim *int) (string, bool) {
	if doc == nil {
		return "", false
	}
	if claim == nil {
		title, ok := doc["title"].(string)
		return title, ok
	}
	claimsRaw, _ := doc["claims"].([]any)
	if *claim < 0 || *claim >= len(claimsRaw) {
		return "", false
	}
	cm, ok := claimsRaw[*claim].(map[string]any)
	if !ok {
		return "", false
	}
	text, ok := cm["text"].(string)
	return text, ok
}

func findProposalAnnotation(doc map[string]any, claim *int, index int) (map[string]any, error) {
	var notes []map[string]any
	if claim == nil {
		notes = extractMapSlice(doc["title_notes"])
	} else {
		claimsRaw, _ := doc["claims"].([]any)
		if *claim >= 0 && *claim < len(claimsRaw) {
			if cm, ok := claimsRaw[*claim].(map[string]any); ok {
				notes = extractMapSlice(cm["community_notes"])
			}
		}
	}
	if index < 0 || index >= len(notes) || mapString(notes[index], "kind") != "annotation" {
		return nil, errors.New("找不到所选注解，请刷新词条后重试")
	}
	return notes[index], nil
}

func splitNonEmptyLines(text string) []string {
	if text == "" {
		return nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	return strings.Split(normalized, "\n")
}

// ProposalQueryOptions 控制管理端与个人中心查询投稿列表的过滤选项。
type ProposalQueryOptions struct {
	UserID     string
	Status     string
	PageID     string
	ProposalID string
	Offset     int
	Review     bool
	Queued     bool
}

// CommunityService 管理全部 7 类社区投稿的提交、24h 限流、审批、修订合并审批、撤回与队列派发（对齐 core/wiki/community.py）。
type CommunityService struct {
	store     *Store
	knowledge *Knowledge
}

// NewCommunityService 创建社区协作服务。
func NewCommunityService(store *Store, knowledge *Knowledge) *CommunityService {
	if knowledge == nil {
		knowledge = NewKnowledge(store)
	}
	return &CommunityService{
		store:     store,
		knowledge: knowledge,
	}
}

// Submit 校验频道投稿策略、用户状态、24h 10 次限流与线上基准修订一致性后写入待审投稿，返回 (proposalID, shouldNotifyAdmins, error)。
func (c *CommunityService) Submit(ctx context.Context, userID, channelID, pageID string, in ProposalInput) (string, bool, error) {
	if err := in.ValidateAndNormalize(); err != nil {
		return "", false, err
	}
	var (
		proposalID string
		notify     bool
	)
	err := c.store.WithTx(ctx, func(tx *sql.Tx) error {
		var cfgRaw []byte
		if err := tx.QueryRowContext(ctx, "SELECT config FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cfgRaw); err != nil {
			return errors.New("此频道尚未开放该类投稿")
		}
		cfg, _ := ParseWikiConfig(cfgRaw)
		if !cfg.ProposalsEnabled || (in.Kind != "edit" && in.Kind != "correction" && !cfg.AnnotationsEnabled) {
			return errors.New("此频道尚未开放该类投稿")
		}

		var blocked bool
		if err := tx.QueryRowContext(ctx, "SELECT blocked FROM wiki_users WHERE id=? FOR UPDATE", userID).Scan(&blocked); err != nil || blocked {
			return errors.New("账户不可投稿")
		}

		var recentCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM wiki_edit_proposals
			WHERE user_id=? AND created_at > UTC_TIMESTAMP(6) - INTERVAL 1 DAY
		`, userID).Scan(&recentCount); err != nil {
			return err
		}
		if recentCount >= MaxProposalsPerDayPerUser {
			return errors.New("今天的投稿次数已用完")
		}

		var docRaw []byte
		qVis := fmt.Sprintf(`
			SELECT r.document
			FROM wiki_channels c
			JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision
			JOIN wiki_pages p ON p.id=pp.page_id
			JOIN wiki_page_revisions r ON r.id=pp.revision_id
			WHERE c.id=? AND p.id=? AND r.id=? AND %s
		`, VisibleSQL("c", "r", "p"))
		if err := tx.QueryRowContext(ctx, qVis, channelID, pageID, in.Revision).Scan(&docRaw); err != nil {
			return errors.New("当前版本已变化或不可读取")
		}

		doc := decodeMapJSON(docRaw)
		original, ok := proposalOriginalText(doc, in.Claim)
		if !ok {
			return errors.New("找不到所选段落")
		}

		var origNoteSources []string
		if in.AnnotationIndex != nil {
			origNote, nErr := findProposalAnnotation(doc, in.Claim, *in.AnnotationIndex)
			if nErr != nil {
				return nErr
			}
			original = mapString(origNote, "text")
			origNoteSources = mapStringSlice(origNote["sources"])
			if in.Text == original && slices.Equal(in.Sources, origNoteSources) {
				return errors.New("注解没有变化")
			}
		} else if in.Excerpt != "" {
			var normExcerpt, normOriginal string
			if in.Claim != nil {
				normExcerpt = SelectionText(in.Excerpt)
				normOriginal = SelectionText(original)
			} else {
				normExcerpt = strings.TrimSpace(in.Excerpt)
				normOriginal = strings.TrimSpace(original)
			}
			if normExcerpt == "" || !strings.Contains(normOriginal, normExcerpt) {
				return errors.New("所选文字与当前版本不一致，请重新划选")
			}
		}

		if in.Claim == nil && (in.Kind == "edit" || in.Kind == "correction") {
			if err := ValidateTitleCorrection(in.Text); err != nil {
				return err
			}
		}
		if (in.Kind == "edit" || in.Kind == "correction") && in.Text == original {
			return errors.New("修订内容没有变化")
		}

		var diffStr string
		if in.Kind == "edit" || in.Kind == "correction" || in.AnnotationIndex != nil {
			oldLines := splitNonEmptyLines(original)
			newLines := splitNonEmptyLines(in.Text)
			if in.AnnotationIndex != nil {
				oldLines = append(oldLines, origNoteSources...)
				newLines = append(newLines, in.Sources...)
			}
			diffStr = strings.Join(UnifiedDiff(oldLines, newLines, "published", "proposed"), "\n")
		}

		proposalID = NewID()
		payload := in.PayloadMap(diffStr)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO wiki_edit_proposals
			(id, user_id, channel_id, page_id, base_revision, kind, payload, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))
		`, proposalID, userID, channelID, pageID, in.Revision, in.Kind, EncodeJSON(payload)); err != nil {
			return err
		}
		notify = cfg.ProposalNotifications
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return proposalID, notify, nil
}

// Approve 批准待审投稿并排入词条审校队列。
func (c *CommunityService) Approve(ctx context.Context, proposalID, actor, note string) (map[string]any, error) {
	return c.Decide(ctx, proposalID, "approved", actor, note, false)
}

// Reject 拒绝待审投稿。
func (c *CommunityService) Reject(ctx context.Context, proposalID, actor, note string) (map[string]any, error) {
	return c.Decide(ctx, proposalID, "rejected", actor, note, false)
}

// ReturnProposal 退回待审投稿供读者重提。
func (c *CommunityService) ReturnProposal(ctx context.Context, proposalID, actor, note string) (map[string]any, error) {
	return c.Decide(ctx, proposalID, "returned", actor, note, false)
}

// Decide 执行管理员对社区投稿的 approved / rejected / returned 决策（fromTelegram=true 时附带 24h 按钮时效保护）。
func (c *CommunityService) Decide(ctx context.Context, proposalID, action, actor, note string, fromTelegram bool) (map[string]any, error) {
	switch action {
	case "approved", "rejected", "returned":
	default:
		return nil, errors.New("审核操作无效")
	}

	var channelID string
	if err := c.store.DB.QueryRowContext(ctx, "SELECT channel_id FROM wiki_edit_proposals WHERE id=?", proposalID).Scan(&channelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("投稿不存在")
		}
		return nil, err
	}

	var newRevID, jobID string
	err := c.store.WithTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		var pubRev sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT enabled, published_revision FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&enabled, &pubRev); err != nil {
			return errors.New("投稿已处理")
		}

		prop, err := getProposalForUpdateTx(ctx, tx, proposalID)
		if err != nil || prop == nil || prop.Status != "pending" {
			return errors.New("投稿已处理")
		}
		if fromTelegram {
			var expired bool
			if err := tx.QueryRowContext(ctx, "SELECT created_at < UTC_TIMESTAMP(6) - INTERVAL 1 DAY FROM wiki_edit_proposals WHERE id=?", proposalID).Scan(&expired); err != nil {
				return err
			}
			if expired {
				return errors.New("按钮已过期，请在 WebUI 处理")
			}
		}

		if action == "approved" {
			if !enabled {
				return errors.New("请先启用频道 Wiki")
			}
			if err := c.validateApprovalTx(ctx, tx, pubRev.String, prop); err != nil {
				return err
			}
			newRevID, jobID, err = c.buildApprovedTx(ctx, tx, prop, actor)
			if err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_edit_proposals
			SET status=?, revision_id=?, actor=?, note=?, updated_at=UTC_TIMESTAMP(6)
			WHERE id=?
		`, action, nilIfEmpty(newRevID), actor, note, proposalID); err != nil {
			return err
		}
		return c.store.Audit(ctx, tx, channelID, "proposal_"+action, proposalID, actor, map[string]any{
			"revision_id": nilIfEmpty(newRevID),
			"note":        note,
		})
	})
	if err != nil {
		return nil, err
	}

	var revOut, jobOut any
	if newRevID != "" {
		revOut = newRevID
	}
	if jobID != "" {
		jobOut = jobID
	}
	return map[string]any{
		"status":      action,
		"revision_id": revOut,
		"job_id":      jobOut,
		"queued":      action == "approved" && jobID == "",
	}, nil
}

// EditAndApprove 允许管理员在批准社区投稿时润色修订正文与附加来源链接，重算 Diff 后原子生成新修订并排入 page_edit 队列。
func (c *CommunityService) EditAndApprove(ctx context.Context, proposalID, editedText string, editedSources []string, actor, note string) (map[string]any, error) {
	if strings.ContainsRune(editedText, '\x00') {
		return nil, errors.New("内容含无效字符")
	}
	editedText = strings.TrimSpace(editedText)
	if utf8.RuneCountInString(editedText) > 10000 {
		return nil, errors.New("修订内容不能超过 10000 个字符")
	}
	if len(editedSources) > 5 {
		return nil, errors.New("最多附加 5 个来源链接")
	}
	cleanSources := make([]string, 0, len(editedSources))
	for _, raw := range editedSources {
		u, err := PublicURL(raw)
		if err != nil {
			return nil, fmt.Errorf("来源链接无效: %w", err)
		}
		cleanSources = append(cleanSources, u)
	}
	cleanSources = UniqueStrings(cleanSources)

	var channelID string
	if err := c.store.DB.QueryRowContext(ctx, "SELECT channel_id FROM wiki_edit_proposals WHERE id=?", proposalID).Scan(&channelID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("投稿不存在")
		}
		return nil, err
	}

	var newRevID, jobID string
	err := c.store.WithTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		var pubRev sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT enabled, published_revision FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&enabled, &pubRev); err != nil {
			return errors.New("投稿已处理")
		}
		if !enabled {
			return errors.New("请先启用频道 Wiki")
		}
		prop, err := getProposalForUpdateTx(ctx, tx, proposalID)
		if err != nil || prop == nil || prop.Status != "pending" {
			return errors.New("投稿已处理")
		}
		if err := c.validateApprovalTx(ctx, tx, pubRev.String, prop); err != nil {
			return err
		}

		claim := parseOptionalIntPtr(prop.Payload["claim"])
		annIdx := parseOptionalIntPtr(prop.Payload["annotation_index"])
		if editedText == "" && annIdx == nil {
			return errors.New("内容不能为空")
		}
		if (prop.Kind == "source" || prop.Kind == "fact_check") && len(cleanSources) == 0 {
			return errors.New("请提供来源链接")
		}
		if claim == nil && (prop.Kind == "edit" || prop.Kind == "correction") {
			if err := ValidateTitleCorrection(editedText); err != nil {
				return err
			}
		}

		var baseDocRaw []byte
		if err := tx.QueryRowContext(ctx, "SELECT document FROM wiki_page_revisions WHERE id=? AND page_id=?", prop.BaseRevision, prop.PageID).Scan(&baseDocRaw); err != nil {
			return errors.New("当前版本已变化或不可读取")
		}
		baseDoc := decodeMapJSON(baseDocRaw)
		origText, _ := proposalOriginalText(baseDoc, claim)
		var origNoteSources []string
		if annIdx != nil {
			if origNote, nErr := findProposalAnnotation(baseDoc, claim, *annIdx); nErr == nil {
				origText = mapString(origNote, "text")
				origNoteSources = mapStringSlice(origNote["sources"])
			}
		}

		prop.Payload["text"] = editedText
		if editedText == "" && annIdx != nil {
			prop.Payload["sources"] = []string{}
		} else {
			prop.Payload["sources"] = cleanSources
		}
		if prop.Kind == "edit" || prop.Kind == "correction" || annIdx != nil {
			oldLines := splitNonEmptyLines(origText)
			newLines := splitNonEmptyLines(editedText)
			if annIdx != nil {
				oldLines = append(oldLines, origNoteSources...)
				newLines = append(newLines, mapStringSlice(prop.Payload["sources"])...)
			}
			prop.Payload["diff"] = strings.Join(UnifiedDiff(oldLines, newLines, "published", "proposed"), "\n")
		}

		newRevID, jobID, err = c.buildApprovedTx(ctx, tx, prop, actor)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_edit_proposals
			SET status='approved', payload=?, revision_id=?, actor=?, note=?, updated_at=UTC_TIMESTAMP(6)
			WHERE id=?
		`, EncodeJSON(prop.Payload), newRevID, actor, note, proposalID); err != nil {
			return err
		}
		return c.store.Audit(ctx, tx, channelID, "proposal_edit_approved", proposalID, actor, map[string]any{
			"revision_id": newRevID,
			"note":        note,
		})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":      "approved",
		"revision_id": newRevID,
		"job_id":      jobID,
		"queued":      jobID == "",
	}, nil
}

// Withdraw 撤回当前读者本人处于 pending 或 returned 状态的投稿。
func (c *CommunityService) Withdraw(ctx context.Context, proposalID, userID string) (bool, error) {
	res, err := c.store.DB.ExecContext(ctx, `
		UPDATE wiki_edit_proposals
		SET status='withdrawn', updated_at=UTC_TIMESTAMP(6)
		WHERE id=? AND user_id=? AND status IN ('pending','returned')
	`, proposalID, userID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// DispatchApproved 派发已批准但尚未构造修订任务的排队投稿；若基准修订已漂移则自动退回为 returned（对齐 community.py:dispatch_approved）。
func (c *CommunityService) DispatchApproved(ctx context.Context, channelID string) (bool, error) {
	var dispatched bool
	err := c.store.WithTx(ctx, func(tx *sql.Tx) error {
		var enabled bool
		var pubRev sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT enabled, published_revision FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&enabled, &pubRev); err != nil || !enabled {
			return nil
		}
		var propID string
		qErr := tx.QueryRowContext(ctx, `
			SELECT id FROM wiki_edit_proposals
			WHERE channel_id=? AND status='approved' AND revision_id IS NULL
			ORDER BY updated_at, id LIMIT 1 FOR UPDATE
		`, channelID).Scan(&propID)
		if errors.Is(qErr, sql.ErrNoRows) {
			return nil
		}
		if qErr != nil {
			return qErr
		}
		prop, err := getProposalForUpdateTx(ctx, tx, propID)
		if err != nil || prop == nil {
			return err
		}

		if valErr := c.validateApprovalTx(ctx, tx, pubRev.String, prop); valErr != nil {
			note := valErr.Error()
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_edit_proposals SET status='returned', note=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", note, prop.ID); err != nil {
				return err
			}
			dispatched = true
			return c.store.Audit(ctx, tx, channelID, "proposal_returned", prop.ID, "wiki", map[string]any{"note": note})
		}

		actor := prop.Actor
		if actor == "" {
			actor = "wiki"
		}
		revID, _, err := c.buildApprovedTx(ctx, tx, prop, actor)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_edit_proposals SET revision_id=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", revID, prop.ID); err != nil {
			return err
		}
		dispatched = true
		return nil
	})
	return dispatched, err
}

type proposalRow struct {
	ID           string
	UserID       string
	ChannelID    string
	PageID       string
	BaseRevision string
	RevisionID   string
	Kind         string
	Payload      map[string]any
	Status       string
	Actor        string
	Note         string
}

func getProposalForUpdateTx(ctx context.Context, tx *sql.Tx, id string) (*proposalRow, error) {
	var (
		p                  proposalRow
		revID, actor, note sql.NullString
		payloadRaw         []byte
	)
	err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, channel_id, page_id, base_revision, revision_id, kind, payload, status, actor, note
		FROM wiki_edit_proposals WHERE id=? FOR UPDATE
	`, id).Scan(&p.ID, &p.UserID, &p.ChannelID, &p.PageID, &p.BaseRevision, &revID, &p.Kind, &payloadRaw, &p.Status, &actor, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.RevisionID = revID.String
	p.Actor = actor.String
	p.Note = note.String
	p.Payload = decodeMapJSON(payloadRaw)
	return &p, nil
}

func (c *CommunityService) validateApprovalTx(ctx context.Context, tx *sql.Tx, publishedRevision string, prop *proposalRow) error {
	var blocked bool
	if err := tx.QueryRowContext(ctx, "SELECT blocked FROM wiki_users WHERE id=? FOR UPDATE", prop.UserID).Scan(&blocked); err != nil || blocked {
		return errors.New("投稿账户不可用")
	}
	var validRev string
	q := fmt.Sprintf(`
		SELECT r.id
		FROM wiki_publication_pages pp
		JOIN wiki_page_revisions r ON r.id=pp.revision_id
		JOIN wiki_pages p ON p.id=pp.page_id
		WHERE pp.publication_id=? AND pp.page_id=? AND r.id=? AND %s
		  AND NOT EXISTS (
		      SELECT 1 FROM wiki_edit_proposals other
		      WHERE other.page_id=p.id AND other.base_revision=r.id AND other.status='approved' AND other.id<>?
		  )
	`, RevisionVisibleSQL("r", "p"))
	if err := tx.QueryRowContext(ctx, q, publishedRevision, prop.PageID, prop.BaseRevision, prop.ID).Scan(&validRev); err != nil || validRev == "" {
		return errors.New("发布基线已变化，或同一版本已有其他建议获批，请重新投稿")
	}
	return nil
}

func parseOptionalIntPtr(raw any) *int {
	if raw == nil {
		return nil
	}
	v := int(anyToInt64(raw))
	return &v
}

func (c *CommunityService) buildApprovedTx(ctx context.Context, tx *sql.Tx, prop *proposalRow, actor string) (string, string, error) {
	replace := prop.Kind == "edit" || prop.Kind == "correction"
	sources := mapStringSlice(prop.Payload["sources"])
	if sources == nil {
		sources = []string{}
	}
	text := mapString(prop.Payload, "text")

	var annotation map[string]any
	if !replace || len(sources) > 0 {
		annotation = map[string]any{
			"proposal_id": prop.ID,
			"kind":        prop.Kind,
			"text":        text,
			"sources":     sources,
		}
		if excerpt := mapString(prop.Payload, "excerpt"); excerpt != "" {
			annotation["excerpt"] = excerpt
			if prop.Payload["excerpt_occurrence"] != nil {
				annotation["excerpt_occurrence"] = int(anyToInt64(prop.Payload["excerpt_occurrence"]))
			}
		}
	}

	claim := parseOptionalIntPtr(prop.Payload["claim"])
	annIdx := parseOptionalIntPtr(prop.Payload["annotation_index"])
	revID, err := c.knowledge.CreateCorrection(ctx, tx, prop.ChannelID, prop.BaseRevision, claim, text, actor, CorrectionOptions{
		Annotation:      annotation,
		AnnotationIndex: annIdx,
		ProposalID:      prop.ID,
		ReplaceText:     replace,
		Standalone:      true,
	})
	if err != nil {
		return "", "", err
	}
	jobID, err := c.store.EnqueuePageEdit(ctx, tx, prop.ChannelID, prop.PageID, revID, prop.BaseRevision, actor)
	if err != nil {
		return "", "", err
	}
	return revID, jobID, nil
}

// ListProposals 查询社区投稿列表（支持个人中心历史、管理端待审队列与详情展开，对齐 community.py:proposals）。
func (c *CommunityService) ListProposals(ctx context.Context, opts ProposalQueryOptions) ([]map[string]any, error) {
	where := []string{"1=1"}
	var args []any
	if opts.UserID != "" {
		where = append(where, "e.user_id=?")
		args = append(args, opts.UserID)
	}
	if opts.Status != "" {
		where = append(where, "e.status=?")
		args = append(args, opts.Status)
	}
	if opts.Queued {
		where = append(where, "e.status='approved' AND e.revision_id IS NULL")
	}
	if opts.PageID != "" {
		where = append(where, "e.page_id=?")
		args = append(args, opts.PageID)
	}
	if opts.ProposalID != "" {
		where = append(where, "e.id=?")
		args = append(args, opts.ProposalID)
	}

	reviewFields := ""
	reviewJoins := ""
	if opts.Review {
		reviewFields = `, c.title AS channel_title, j.id AS review_job_id, j.status AS review_job_status,
			(JSON_EXTRACT(j.progress,'$.review_complete')=TRUE) AS review_complete,
			(JSON_EXTRACT(j.progress,'$.review_skipped')=TRUE) AS review_skipped,
			base.document AS base_document`
		reviewJoins = `JOIN wiki_channels c ON c.id=e.channel_id
			LEFT JOIN wiki_page_revisions base ON base.id=e.base_revision AND base.page_id=e.page_id
			LEFT JOIN wiki_page_revisions edit ON edit.id=e.revision_id
			LEFT JOIN wiki_jobs j ON j.id=edit.job_id AND j.kind='page_edit'`
	}

	publicStatus := ""
	if opts.UserID != "" {
		publicStatus = fmt.Sprintf(`, EXISTS(
			SELECT 1 FROM wiki_channels c
			JOIN wiki_publication_pages pp ON pp.publication_id=c.published_revision AND pp.page_id=p.id
			JOIN wiki_page_revisions r ON r.id=pp.revision_id AND r.page_id=p.id AND r.channel_id=c.id
			WHERE c.id=e.channel_id AND %s AND %s
		) AS currently_public`, VisibleSQL("c", "r", "p"), inPublishedRevisionSQL)
	}

	args = append(args, max(0, opts.Offset))
	q := fmt.Sprintf(`
		SELECT e.id, e.channel_id, e.page_id, e.base_revision, e.revision_id, e.kind, e.payload,
		       e.status, e.note, e.created_at, p.title,
		       EXISTS(SELECT 1 FROM wiki_contributions x WHERE x.proposal_id=e.id) AS published%s%s
		FROM wiki_edit_proposals e
		JOIN wiki_pages p ON p.id=e.page_id
		%s
		WHERE %s
		ORDER BY e.created_at DESC, e.id
		LIMIT 30 OFFSET ?
	`, publicStatus, reviewFields, reviewJoins, strings.Join(where, " AND "))

	rows, err := QueryRowsMap(ctx, c.store.DB, q, args...)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		payload := decodeMapJSON([]byte(mapString(row, "payload")))
		row["payload"] = payload
		row["published"] = anyToInt64(row["published"]) != 0 || mapBool(row, "published")
		if opts.UserID != "" {
			row["currently_public"] = anyToInt64(row["currently_public"]) != 0 || mapBool(row, "currently_public")
		}
		if opts.Review {
			baseDoc := decodeMapJSON([]byte(mapString(row, "base_document")))
			delete(row, "base_document")
			row["review_complete"] = anyToInt64(row["review_complete"]) != 0 || mapBool(row, "review_complete")
			row["review_skipped"] = anyToInt64(row["review_skipped"]) != 0 || mapBool(row, "review_skipped")

			claim := parseOptionalIntPtr(payload["claim"])
			annIdx := parseOptionalIntPtr(payload["annotation_index"])
			if annIdx == nil {
				if orig, ok := proposalOriginalText(baseDoc, claim); ok {
					row["original"] = orig
				} else {
					row["original"] = nil
				}
			} else {
				if origNote, nErr := findProposalAnnotation(baseDoc, claim, *annIdx); nErr == nil {
					row["original"] = mapString(origNote, "text")
				} else {
					row["original"] = nil
				}
			}
		}
	}
	return rows, nil
}

// ListContributions 查询公开贡献榜或单人贡献历史（与 PublicReader.Contributors 共享契约）。
func (c *CommunityService) ListContributions(ctx context.Context, opts ContributorsOptions) ([]map[string]any, error) {
	return NewPublicReader(c.store).Contributors(ctx, opts)
}

// ProposalSummary 生成发送给管理员 Telegram 私聊的社区投稿摘要文本（对齐 commands.py:proposal_summary）。
func ProposalSummary(proposal map[string]any) string {
	payload, _ := proposal["payload"].(map[string]any)
	if payload == nil {
		payload = map[string]any{}
	}
	kind := mapString(proposal, "kind")

	excerpt := func(val string, limit int) string {
		runes := []rune(val)
		if len(runes) <= limit {
			return val
		}
		return string(runes[:limit]) + "…"
	}

	target := "词条标题"
	if claim := parseOptionalIntPtr(payload["claim"]); claim != nil {
		target = fmt.Sprintf("第 %d 段", *claim+1)
	}
	annEdit := payload["annotation_index"] != nil
	text := mapString(payload, "text")
	annDelete := annEdit && text == ""

	label := ProposalLabels[kind]
	if annDelete {
		label = "删除注解"
	} else if annEdit {
		label = "修改注解"
	}

	lines := []string{
		fmt.Sprintf("Wiki 待审 · %s", label),
		fmt.Sprintf("%s / %s · %s",
			excerpt(mapString(proposal, "channel_title"), 100),
			excerpt(mapString(proposal, "title"), 160),
			target,
		),
	}

	if kind == "edit" || kind == "correction" || annEdit {
		diffLines := strings.Split(strings.ReplaceAll(mapString(payload, "diff"), "\r\n", "\n"), "\n")
		var changes []string
		if len(diffLines) > 2 {
			for _, ln := range diffLines[2:] {
				if strings.HasPrefix(ln, "-") || strings.HasPrefix(ln, "+") {
					changes = append(changes, ln)
				}
			}
		}
		type diffSec struct {
			marker   string
			secLabel string
			fallback string
		}
		for _, sec := range []diffSec{
			{"-", "修改前", mapString(proposal, "original")},
			{"+", "修改后", text},
		} {
			if annDelete && sec.marker == "+" {
				lines = append(lines, sec.secLabel+"\n（删除整条注解）")
				continue
			}
			var picked []string
			for _, ch := range changes {
				if strings.HasPrefix(ch, sec.marker) {
					picked = append(picked, ch[1:])
				}
			}
			joined := strings.Join(picked, "\n")
			if joined == "" {
				if len(changes) > 0 {
					joined = "（未改动）"
				} else if sec.fallback != "" {
					joined = sec.fallback
				} else {
					joined = "（原版本不可用）"
				}
			}
			lines = append(lines, fmt.Sprintf("%s\n%s", sec.secLabel, excerpt(joined, 500)))
		}
	} else {
		exc := mapString(payload, "excerpt")
		orig := exc
		if orig == "" {
			orig = mapString(proposal, "original")
		}
		if orig != "" {
			hdr := "原文段落"
			if exc != "" {
				hdr = "所选原文"
			}
			lines = append(lines, fmt.Sprintf("%s\n%s", hdr, excerpt(orig, 500)))
		}
		lines = append(lines, fmt.Sprintf("%s\n%s", ProposalLabels[kind], excerpt(text, 500)))
	}

	sources := mapStringSlice(payload["sources"])
	if len(sources) > 0 {
		lines = append(lines, fmt.Sprintf("附 %d 个来源，完整链接见详情。", len(sources)))
	}
	return strings.Join(lines, "\n\n")
}
