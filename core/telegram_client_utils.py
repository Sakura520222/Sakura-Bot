# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可，
# 并附加非商业使用限制条款。
#
# - 署名：必须提供本项目的原始来源链接
# - 非商业：禁止任何商业用途和分发
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

import logging
import re

logger = logging.getLogger(__name__)

def split_message_smart(text, max_length, preserve_md=True):
    """
    智能分割消息，确保md格式实体不被破坏

    Args:
        text: 要分割的文本
        max_length: 每个分段的最大长度
        preserve_md: 是否保护md格式

    Returns:
        list: 分割后的文本片段列表
    """
    if len(text) <= max_length:
        return [text]

    if not preserve_md:
        # 简单分割：按字符数分割
        parts = []
        for i in range(0, len(text), max_length):
            parts.append(text[i:i+max_length])
        return parts

    # 智能分割：保护md实体
    parts = []
    current_pos = 0

    # 定义需要保护的md实体模式
    md_patterns = [
        (r'\*\*.*?\*\*', 'bold'),        # 粗体 **text**
        (r'`.*?`', 'inline_code'),       # 内联代码 `code`
        (r'\[.*?\]\(.*?\)', 'link'),     # 链接 [text](url)
        (r'\*.*?\*', 'italic'),          # 斜体 *text*
        (r'_.*?_', 'italic_underscore'), # 斜体 _text_
        (r'~~.*?~~', 'strikethrough'),   # 删除线 ~~text~~
        (r'__.*?__', 'bold_underscore'), # 粗体 __text__
    ]

    # 查找所有md实体的位置
    entities = []
    for pattern, entity_type in md_patterns:
        for match in re.finditer(pattern, text, re.DOTALL):
            entities.append({
                'start': match.start(),
                'end': match.end(),
                'text': match.group(),
                'type': entity_type
            })

    # 按起始位置排序实体
    entities.sort(key=lambda x: x['start'])

    # 合并重叠的实体
    merged_entities = []
    for entity in entities:
        if not merged_entities or entity['start'] > merged_entities[-1]['end']:
            merged_entities.append(entity)
        else:
            # 合并重叠的实体
            merged_entities[-1]['end'] = max(merged_entities[-1]['end'], entity['end'])
            merged_entities[-1]['text'] = text[merged_entities[-1]['start']:merged_entities[-1]['end']]

    # 优先在段落边界分割（空行）
    paragraph_boundaries = []
    for match in re.finditer(r'\n\s*\n', text):
        paragraph_boundaries.append(match.end())

    # 次优在句子边界分割（句号、问号、感叹号后跟空格或换行）
    sentence_boundaries = []
    for match in re.finditer(r'[。.?!]\s+', text):
        sentence_boundaries.append(match.end())

    # 不再在每个换行处分割，只在连续换行（段落边界）处分割
    # 这样可以避免将列表式内容过度分割

    # 所有分割点，按优先级排序（只包含段落和句子边界）
    all_boundaries = sorted(set(paragraph_boundaries + sentence_boundaries))

    # 分割算法
    min_segment_length = 100  # 最小分段长度，避免产生过短的段落

    while current_pos < len(text):
        # 计算当前分段的最大结束位置
        max_end_pos = min(current_pos + max_length, len(text))

        # 如果剩余文本已经小于最大长度，直接添加
        if len(text) - current_pos <= max_length:
            parts.append(text[current_pos:])
            break

        # 查找最佳分割点
        best_split_pos = -1

        # 1. 查找在边界内的分割点（只查找在合理范围内的）
        for boundary in all_boundaries:
            if current_pos < boundary <= max_end_pos:
                best_split_pos = boundary
                break

        # 2. 如果找到的分割点会导致分段太短，继续查找更远的分割点
        if best_split_pos != -1 and (best_split_pos - current_pos) < min_segment_length:
            # 尝试查找更远的分割点
            for boundary in all_boundaries:
                if boundary > best_split_pos and boundary <= max_end_pos:
                    # 检查这个新的分割点是否会产生合理的长度
                    segment_length = boundary - current_pos
                    if segment_length >= min_segment_length and segment_length <= max_length:
                        best_split_pos = boundary
                    # 如果新的分割点仍然太短，继续查找
                    elif segment_length < min_segment_length:
                        best_split_pos = boundary
                    else:
                        break  # 超过最大长度了

        # 3. 如果没有找到边界分割点，检查是否会在实体中间分割
        if best_split_pos == -1:
            # 检查当前位置到max_end_pos之间是否有实体
            for entity in merged_entities:
                if entity['start'] < max_end_pos < entity['end']:
                    # 如果在实体中间，尝试在实体开始前分割
                    if entity['start'] > current_pos:
                        best_split_pos = entity['start']
                        break
                    # 如果实体开始位置也在当前分段内，尝试在实体结束后分割
                    elif entity['end'] <= current_pos + max_length * 2:  # 允许稍微超过一点
                        best_split_pos = entity['end']
                        break

        # 4. 如果还是没有找到合适的分割点，使用字符边界
        if best_split_pos == -1:
            # 确保不在单词中间分割（如果可能）
            # 查找最后一个空格或标点
            for i in range(max_end_pos - 1, current_pos, -1):
                if text[i].isspace() or text[i] in '，。,.!?;:':
                    best_split_pos = i + 1
                    break

            # 如果还是没有找到，使用字符边界
            if best_split_pos == -1:
                best_split_pos = max_end_pos

        # 添加当前分段
        part = text[current_pos:best_split_pos].strip()
        if part:
            parts.append(part)

        # 更新当前位置
        current_pos = best_split_pos

    # 验证所有分段都不超过最大长度
    validated_parts = []
    for part in parts:
        if len(part) > max_length:
            logger.warning(f"分段长度 {len(part)} 超过最大长度 {max_length}，进行二次分割")
            # 对超长的分段进行简单分割
            for i in range(0, len(part), max_length):
                validated_parts.append(part[i:i+max_length])
        else:
            validated_parts.append(part)

    # 验证所有实体完整性
    for part in validated_parts:
        # 检查是否有未闭合的md实体
        bold_count = part.count('**')
        if bold_count % 2 != 0:
            logger.warning(f"分段中粗体标记不匹配: {bold_count}个**标记")

        # 可以添加其他实体完整性检查

    logger.debug(f"智能分割完成: {len(text)}字符 -> {len(validated_parts)}个分段")
    return validated_parts


def validate_message_entities(text):
    """
    验证消息中的md实体是否完整

    Args:
        text: 要验证的文本

    Returns:
        bool: 实体是否完整
        str: 错误信息（如果有）
    """
    # 检查粗体标记 **
    bold_count = text.count('**')
    if bold_count % 2 != 0:
        return False, f"粗体标记不匹配: 找到{bold_count}个**标记"

    # 检查粗体标记 __
    bold_underscore_count = text.count('__')
    if bold_underscore_count % 2 != 0:
        return False, f"粗体标记不匹配: 找到{bold_underscore_count}个__标记"

    # 检查内联代码标记
    backtick_count = text.count('`')
    if backtick_count % 2 != 0:
        return False, f"内联代码标记不匹配: 找到{backtick_count}个`标记"

    # 检查斜体标记 *
    italic_count = text.count('*') - bold_count * 2  # 排除**中的*
    if italic_count % 2 != 0:
        return False, f"斜体标记不匹配: 找到{italic_count}个*标记"

    # 检查斜体标记 _
    italic_underscore_count = text.count('_') - bold_underscore_count * 2  # 排除__中的_
    if italic_underscore_count % 2 != 0:
        return False, f"斜体标记不匹配: 找到{italic_underscore_count}个_标记"

    # 检查删除线标记 ~~
    strikethrough_count = text.count('~~')
    if strikethrough_count % 2 != 0:
        return False, f"删除线标记不匹配: 找到{strikethrough_count}个~~标记"

    # 检查链接格式 [text](url)
    link_pattern = r'\[.*?\]\(.*?\)'
    links = list(re.finditer(link_pattern, text))
    for link in links:
        link_text = link.group()
        if '](' not in link_text or not link_text.endswith(')'):
            return False, f"链接格式错误: {link_text}"

    return True, "所有实体完整"


def split_by_lines_smart(text, max_length):
    """
    按行智能分割，确保每行不超过最大长度

    Args:
        text: 要分割的文本
        max_length: 每行的最大长度

    Returns:
        list: 分割后的行列表
    """
    lines = text.split('\n')
    result_lines = []

    for line in lines:
        if len(line) <= max_length:
            result_lines.append(line)
        else:
            # 对超长的行进行智能分割
            words = line.split(' ')
            current_line = ""

            for word in words:
                if len(current_line) + len(word) + 1 <= max_length:
                    current_line += (" " if current_line else "") + word
                else:
                    if current_line:
                        result_lines.append(current_line)
                    current_line = word

            if current_line:
                result_lines.append(current_line)

    return result_lines
