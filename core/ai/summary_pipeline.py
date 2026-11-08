# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件
"""输入不截断，超出预算时分层摘要，每次运行写入记录文件（兼容历史字符串 API）。"""

import asyncio
import json
import logging
import os
import re
from datetime import UTC, datetime
from pathlib import Path
from tempfile import NamedTemporaryFile
from uuid import uuid4

from core.ai.text_budget import pack_texts, split_text, token_upper_bound
from core.system.error_handler import RetryExhaustedError

logger = logging.getLogger(__name__)
SYSTEM = "你是资讯摘要助手。保留关键事实、来源编号和链接，按原文顺序汇总，不得编造。"
MERGE = "合并以下分段摘要，覆盖每一段的重要信息，保留来源编号与链接：\n"


class SummaryIntegrityError(RetryExhaustedError):
    """只要无法确认所有输入都完整处理，就不返回成功。"""

    def __init__(self, message, last_exception=None):
        super().__init__(message, last_exception)


def _save_run(path, record):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = None
    try:
        with NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, delete=False) as file:
            temporary = Path(file.name)
            json.dump(record, file, ensure_ascii=False, indent=2)
            file.flush()
            os.fsync(file.fileno())
        temporary.replace(path)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


async def summarize_complete(messages, prompt, client, model):
    """逐块分析，再逐层合并；记录原始输入和成功/失败计数。"""
    run_id = uuid4().hex
    path = Path(os.getenv("SUMMARY_AUDIT_DIR", "data/summary_runs")) / f"{run_id}.json"
    record = {
        "run_id": run_id,
        "created_at": datetime.now(UTC).isoformat(),
        "status": "processing",
        "input_count": len(messages),
        "processed_count": 0,
        "failed_count": 0,
        "chunk_count": 0,
        "processed_chunks": 0,
        "error": None,
        "messages": list(messages),
        "model": model,
    }
    _save_run(path, record)
    try:
        context_limit = int(os.getenv("SUMMARY_CONTEXT_TOKENS", "1000000"))
        output_limit = int(os.getenv("SUMMARY_OUTPUT_TOKENS", "64000"))
        # 为 chat 包装、来源标签和输出预留空间，提示词本身也计入预算。
        budget = context_limit - output_limit - token_upper_bound(SYSTEM) - 256
        data_budget = budget - token_upper_bound(prompt + MERGE) - 128
        if output_limit <= 0 or data_budget < 256:
            raise SummaryIntegrityError("Summary context budget is too small for prompt and output")

        async def request(text, instruction):
            input_bound = token_upper_bound(text + instruction)
            if input_bound > budget:
                raise SummaryIntegrityError("Summary request exceeds configured context budget")
            # finish_reason=length 是确定性失败：同参数重试必然再次截断。
            # 在上下文余量内放大输出上限后立即重试；余量耗尽则快速失败，
            # 绝不用相同参数退避重试（那只会白白烧掉时间和配额）。
            max_output = output_limit
            for attempt in range(3):
                try:
                    response = await client.chat.completions.create(
                        model=model,
                        max_tokens=max_output,
                        messages=[
                            {"role": "system", "content": SYSTEM},
                            {"role": "user", "content": instruction + text},
                        ],
                    )
                except Exception:
                    if attempt == 2:
                        raise
                    await asyncio.sleep(2**attempt)
                    continue
                if not response.choices:
                    if attempt == 2:
                        raise SummaryIntegrityError("Model returned no choices")
                    await asyncio.sleep(2**attempt)
                    continue
                choice = response.choices[0]
                if choice.finish_reason == "length":
                    headroom = context_limit - input_bound - token_upper_bound(SYSTEM) - 128
                    if attempt == 2 or max_output >= headroom:
                        raise SummaryIntegrityError(
                            f"Output truncated at max_tokens={max_output} with context "
                            f"headroom {headroom}; raise SUMMARY_OUTPUT_TOKENS or "
                            "SUMMARY_CONTEXT_TOKENS"
                        )
                    max_output = min(max_output * 2, headroom)
                    logger.warning(
                        "Summary run=%s output truncated, retrying with max_tokens=%s",
                        run_id,
                        max_output,
                    )
                    continue
                if (
                    choice.finish_reason != "stop"
                    or not choice.message.content
                    or not choice.message.content.strip()
                ):
                    if attempt == 2:
                        raise SummaryIntegrityError(
                            f"Incomplete model output: finish_reason={choice.finish_reason}"
                        )
                    await asyncio.sleep(2**attempt)
                    continue
                return choice.message.content

        combined = "\n\n---\n\n".join(str(message) for message in messages)
        if token_upper_bound(combined) <= data_budget:
            record["chunk_count"] = 1
            result = await request(combined, prompt)
            record.update(
                status="completed",
                summary=result,
                processed_count=len(messages),
                processed_chunks=1,
            )
            _save_run(path, record)
            logger.info(
                "Summary integrity run=%s input=%s processed=%s failed=0 chunks=1",
                run_id,
                len(messages),
                len(messages),
            )
            return result

        chunks = []
        counts = []
        for index, message in enumerate(messages, 1):
            message = str(message)
            metadata = message.split("内容:", 1)[0] if "内容:" in message else ""
            links = "\n".join(re.findall(r"https://t\.me/[^\s]+", message))
            source = f"[source {index}]\n{metadata}\n{links}\n"
            part_budget = data_budget - token_upper_bound(source)
            if part_budget < 4:
                raise SummaryIntegrityError("Source metadata exceeds summary chunk budget")
            parts = split_text(message, part_budget)
            counts.append(len(parts))
            chunks.extend(
                f"{source}[part {n}/{len(parts)}]\n{part}" for n, part in enumerate(parts, 1)
            )
        record["chunk_count"] = len(chunks)
        _save_run(path, record)
        summaries = []
        completed = 0
        for index, count in enumerate(counts):
            for chunk in chunks[completed : completed + count]:
                summaries.append(await request(chunk, prompt))
                record["processed_chunks"] += 1
            completed += count
            record["processed_count"] = index + 1
            _save_run(path, record)
        # 每一轮都处理上一步的全部结果；不收敛就报错，避免无限循环或丢内容。
        for _ in range(16):
            if len(summaries) == 1:
                break
            previous_size = sum(token_upper_bound(s) for s in summaries)
            groups = pack_texts(summaries, data_budget)
            summaries = [await request(group, MERGE + prompt) for group in groups]
            if len(summaries) > 1 and sum(token_upper_bound(s) for s in summaries) >= previous_size:
                raise SummaryIntegrityError("Hierarchical summary did not converge")
        else:
            raise SummaryIntegrityError("Hierarchical summary exceeded maximum depth")
        result = summaries[0]
        record.update(status="completed", summary=result)
        _save_run(path, record)
        logger.info(
            "Summary integrity run=%s input=%s processed=%s failed=0 chunks=%s",
            run_id,
            len(messages),
            record["processed_count"],
            record["chunk_count"],
        )
        return result
    except Exception as exc:
        record.update(
            status="failed",
            failed_count=max(1, len(messages) - record["processed_count"]),
            error=(str(exc) if isinstance(exc, SummaryIntegrityError) else type(exc).__name__),
        )
        _save_run(path, record)
        logger.error(
            "Summary integrity failed: %s", {k: v for k, v in record.items() if k != "messages"}
        )
        raise SummaryIntegrityError(f"Summary run {run_id} failed: {record['error']}", exc) from exc
