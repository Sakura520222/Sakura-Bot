// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Stream 一次流式响应的增量迭代器。
// 单 goroutine 消费（对齐 Python 单协程消费流对象），非并发安全。
type Stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
	idle    time.Duration
	resp    *http.Response
	scanner *bufio.Scanner
	closed  bool
}

// Next 返回下一个增量分片。
//
// 正常终止（收到 [DONE]）返回 io.EOF，并自动释放连接；
// 断流（连接在 [DONE] 前中断）返回包装 io.ErrUnexpectedEOF 的错误；
// 空闲读超时 / 上游取消 / 网络错误返回相应包装错误。
// choices 为空或 delta 缺失的分片被跳过（对齐 qa_engine_v3.py:452-458 的 None 分支，
// 例如部分兼容端点在尾部追加的 usage 分片）。
func (s *Stream) Next() (Delta, error) {
	for {
		if s.closed {
			return Delta{}, io.EOF
		}
		if !s.scanner.Scan() {
			scanErr := s.scanner.Err()
			cause := context.Cause(s.ctx)
			s.Close()
			if scanErr == nil {
				// 连接干净关闭但未收到 [DONE]：断流
				return Delta{}, fmt.Errorf("SSE 流在 [DONE] 之前中断: %w", io.ErrUnexpectedEOF)
			}
			if cause != nil {
				return Delta{}, fmt.Errorf("SSE 流读取失败: %w", cause)
			}
			return Delta{}, fmt.Errorf("SSE 流读取失败: %w", scanErr)
		}

		payload, ok := sseDataPayload(s.scanner.Text())
		if !ok {
			continue // event:/id:/注释行按 SSE 协议忽略
		}
		if payload == "[DONE]" {
			s.Close()
			return Delta{}, io.EOF
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 无法解析的 data 行跳过（对齐引擎逐片容错的消费方式）
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		w := chunk.Choices[0].Delta
		return Delta{Content: w.Content, ToolCalls: w.ToolCalls}, nil
	}
}

// Close 释放底层连接并停止空闲定时器；幂等，可多次调用。
func (s *Stream) Close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.resp != nil && s.resp.Body != nil {
		_ = s.resp.Body.Close()
	}
}

// sseDataPayload 从一行 SSE 提取 data 载荷；非 data 行返回 false。
// 兼容 "data:{...}" 与 "data: {...}" 两种写法。
func sseDataPayload(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(line[len("data:"):]), true
}

// bufioScanner 以放大后的行缓冲构造 Scanner（默认 64KB 对超长 JSON 行不够）。
func bufioScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanTokenSize)
	return sc
}

// idleReader 包装响应体：每次读取前重置空闲定时器；
// 定时器到期触发取消请求上下文，从而中断阻塞中的 Read。
// 一旦读到错误（含取消）即停表，避免已终止流的定时器继续存续。
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

// Read 实现 io.Reader。
func (ir *idleReader) Read(p []byte) (int, error) {
	ir.timer.Reset(ir.idle)
	n, err := ir.r.Read(p)
	if err != nil {
		ir.timer.Stop()
	}
	return n, err
}
