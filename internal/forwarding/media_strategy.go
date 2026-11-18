// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"fmt"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// MediaType 标识转发决策用的媒体分类（对齐 media_utils.py:MediaType）。
type MediaType string

const (
	MediaImage    MediaType = "image"
	MediaVideo    MediaType = "video"
	MediaDocument MediaType = "document"
	MediaNone     MediaType = "none"
	MediaUnknown  MediaType = "unknown"
)

// ForwardStrategy 标识媒体转发执行策略（对齐 media_utils.py:ForwardStrategy）。
type ForwardStrategy string

const (
	StrategyMemory   ForwardStrategy = "memory"
	StrategyDownload ForwardStrategy = "download"

	DefaultSmallFileThreshold  int64 = 10 << 20 // 10 MiB
	DefaultMediaGroupThreshold int64 = 50 << 20 // 50 MiB
)

// StrategyDecision 描述转发策略判定结果。
type StrategyDecision struct {
	Strategy  ForwardStrategy
	Reason    string
	TotalSize int64
}

// DetectMediaType 判定消息附件所属媒体分类。
func DetectMediaType(msg *mtproto.IncomingMessage) MediaType {
	if msg == nil || msg.Media == nil {
		return MediaNone
	}
	switch msg.Media.Kind {
	case "photo":
		return MediaImage
	case "video":
		return MediaVideo
	case "document", "audio", "voice", "sticker", "gif":
		if strings.HasPrefix(strings.ToLower(msg.Media.MimeType), "video/") {
			return MediaVideo
		}
		return MediaDocument
	default:
		return MediaUnknown
	}
}

// MediaSize 返回单条消息媒体字节数。
func MediaSize(msg *mtproto.IncomingMessage) int64 {
	if msg == nil || msg.Media == nil || msg.Media.FileSize < 0 {
		return 0
	}
	return msg.Media.FileSize
}

// DecideForwardStrategy 计算单条消息或媒体组的内存/落盘转发策略（对齐 media_utils.py:decide_forward_strategy）。
func DecideForwardStrategy(msgs []*mtproto.IncomingMessage, smallThreshold, groupThreshold int64) StrategyDecision {
	if smallThreshold <= 0 {
		smallThreshold = DefaultSmallFileThreshold
	}
	if groupThreshold <= 0 {
		groupThreshold = DefaultMediaGroupThreshold
	}

	var totalSize int64
	types := make(map[MediaType]bool)
	for _, m := range msgs {
		totalSize += MediaSize(m)
		mt := DetectMediaType(m)
		if mt != MediaNone {
			types[mt] = true
		}
	}

	if len(msgs) > 1 {
		if len(types) == 1 && types[MediaImage] {
			return StrategyDecision{Strategy: StrategyMemory, Reason: "纯图片媒体组", TotalSize: totalSize}
		}
		if types[MediaVideo] || types[MediaDocument] {
			mb := float64(totalSize) / (1024 * 1024)
			if totalSize < groupThreshold {
				return StrategyDecision{
					Strategy:  StrategyMemory,
					Reason:    fmt.Sprintf("小文件混合媒体组（%.2fMB < 50MB）", mb),
					TotalSize: totalSize,
				}
			}
			return StrategyDecision{
				Strategy:  StrategyDownload,
				Reason:    fmt.Sprintf("大文件媒体组（%.2fMB >= 50MB）", mb),
				TotalSize: totalSize,
			}
		}
		return StrategyDecision{Strategy: StrategyMemory, Reason: "默认策略", TotalSize: totalSize}
	}

	mb := float64(totalSize) / (1024 * 1024)
	if totalSize < smallThreshold {
		return StrategyDecision{
			Strategy:  StrategyMemory,
			Reason:    fmt.Sprintf("小文件（%.2fMB < 10MB）", mb),
			TotalSize: totalSize,
		}
	}
	return StrategyDecision{
		Strategy:  StrategyDownload,
		Reason:    fmt.Sprintf("大文件（%.2fMB >= 10MB）", mb),
		TotalSize: totalSize,
	}
}
