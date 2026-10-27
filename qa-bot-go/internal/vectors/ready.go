// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package vectors

import (
	"context"
	"fmt"
	"os"
)

// Ready 探测底层只读库当前是否就绪（契约元数据可读且两向量表存在），
// 对齐 core/ai/vector_store.py::is_available / is_messages_available 的
// 语义（vector_store.py:154-160,281-285）：
//
//   - 不加载检索矩阵，仅做文件存在性与 store_meta 两表核查（微秒级）；
//   - 失败不锁存：每次调用重新探测，迁移窗口后自动恢复可用；
//   - 与 Search 一致地在文件被外部删除/重建时丢弃旧连接句柄。
//
// main 装配层实现工具层 VectorStore 窄接口的 IsAvailable/
// IsMessagesAvailable 时调用本方法（契约下两表同时创建，逐 collection
// 探测与整体探测等价）。
func (s *Store) Ready(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 文件已被外部删除/重建（如迁移窗口重建库）→ 按未就绪处理并丢弃旧句柄
	//（与 ensureFresh 的头部判定保持一致）。
	if fi, err := os.Stat(s.dbFile); err != nil || fi.IsDir() {
		_ = s.resetDBLocked()
		return fmt.Errorf("%w: 向量库文件缺失: %s", ErrNotReady, s.dbFile)
	}

	if _, err := s.readVersions(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	return nil
}
