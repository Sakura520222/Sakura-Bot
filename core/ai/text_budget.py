# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件
"""保守的 Token 预算：UTF-8 字节数是 byte-level tokenizer 的上界。

不依赖供应商的 tokenizer，宁可多分块，绝不按字符截掉正文。
"""


def token_upper_bound(text: str) -> int:
    return len(text.encode("utf-8"))


def split_text(text: str, budget: int) -> list[str]:
    """按预算无损分块，保证 Unicode 字符和原文顺序完整。"""
    if budget < 4:
        raise ValueError("Token budget must be at least 4")
    chunks, start, size = [], 0, 0
    for index, char in enumerate(text):
        width = len(char.encode("utf-8"))
        if size + width > budget:
            chunks.append(text[start:index])
            start, size = index, 0
        size += width
    if start < len(text):
        chunks.append(text[start:])
    return chunks or [""]


def pack_texts(texts: list[str], budget: int) -> list[str]:
    """无损装箱，超长条目先拆分，所有片段均保留。"""
    groups, current = [], ""
    for text in texts:
        for part in split_text(text, budget):
            candidate = current + ("\n\n" if current else "") + part
            if current and token_upper_bound(candidate) > budget:
                groups.append(current)
                current = part
            else:
                current = candidate
    if current:
        groups.append(current)
    return groups
