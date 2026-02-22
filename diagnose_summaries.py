#!/usr/bin/env python3
"""
诊断脚本 - 检查数据库中的总结记录
用于排查为什么关键词检索返回0条结果
"""

import asyncio
from datetime import UTC, datetime, timedelta

from dotenv import load_dotenv

from core.database import get_db_manager

# 加载环境变量
load_dotenv("data/.env")


async def main():
    """主诊断函数"""
    print("=" * 60)
    print("总结记录诊断工具")
    print("=" * 60)

    # 初始化数据库
    db = get_db_manager()
    await db.init_database()

    print(f"\n数据库类型: {db.get_database_type()}")
    print(f"数据库版本: {db.get_database_version()}")

    # 1. 查询总记录数
    print("\n" + "-" * 60)
    print("1. 总体统计")
    print("-" * 60)

    all_summaries = await db.get_summaries(limit=10000)
    print(f"📊 总总结记录数: {len(all_summaries)} 条")

    if not all_summaries:
        print("\n⚠️  数据库中没有任何总结记录！")
        return

    # 2. 按时间分布统计
    print("\n" + "-" * 60)
    print("2. 时间分布（调试模式）")
    print("-" * 60)

    now = datetime.now(UTC)
    print(f"  🕐 当前时间: {now}")
    print(f"  🕐 当前时间(ISO): {now.isoformat()}")

    # 先显示所有记录的created_at
    print("\n  📋 所有记录的created_at:")
    for i, s in enumerate(all_summaries, 1):
        created_at = s.get("created_at")
        print(f"     {i}. {created_at} (类型: {type(created_at).__name__})")

    print("\n  开始时间范围测试:")
    time_ranges = [
        ("1天内", timedelta(days=1)),
        ("7天内", timedelta(days=7)),
        ("30天内", timedelta(days=30)),
        ("90天内", timedelta(days=90)),
    ]

    for name, delta in time_ranges:
        start_date = now - delta
        print(f"\n  测试范围: {name}")
        print(f"    - start_date: {start_date} (ISO: {start_date.isoformat()})")
        print(f"    - end_date: {now} (ISO: {now.isoformat()})")

        summaries_in_range = await db.get_summaries(
            limit=10000, start_date=start_date, end_date=now
        )
        count = len(summaries_in_range)
        print(f"    - 结果: {count} 条")

    # 3. 最早和最晚的总结
    print("\n" + "-" * 60)
    print("3. 时间范围")
    print("-" * 60)

    if all_summaries:
        dates = [s.get("created_at") for s in all_summaries if s.get("created_at")]
        if dates:
            earliest = min(dates)
            latest = max(dates)
            print(f"  📌 最早总结: {earliest}")
            print(f"  📌 最新总结: {latest}")
            if isinstance(earliest, str):
                earliest = datetime.fromisoformat(earliest.replace("Z", "+00:00"))
            if isinstance(latest, str):
                latest = datetime.fromisoformat(latest.replace("Z", "+00:00"))
            days_span = (latest - earliest).days if latest > earliest else 0
            print(f"  📌 跨度: {days_span} 天")

    # 4. 按频道统计
    print("\n" + "-" * 60)
    print("4. 频道分布")
    print("-" * 60)

    channels = {}
    for summary in all_summaries:
        channel_name = summary.get("channel_name", "未知频道")
        channels[channel_name] = channels.get(channel_name, 0) + 1

    for channel, count in sorted(channels.items(), key=lambda x: x[1], reverse=True):
        print(f"  📺 {channel}: {count} 条")

    # 5. 关键词检测（检查"卡池"相关）
    print("\n" + "-" * 60)
    print("5. 关键词检测")
    print("-" * 60)

    keywords_to_check = ["卡池", "pool", "gacha", "抽卡", "召唤"]

    for keyword in keywords_to_check:
        matching_summaries = []
        for summary in all_summaries:
            text = summary.get("summary_text", "").lower()
            if keyword.lower() in text:
                matching_summaries.append(summary)

        print(f"  🔍 关键词 '{keyword}': {len(matching_summaries)} 条")

        if matching_summaries:
            print("     示例:")
            for i, s in enumerate(matching_summaries[:3], 1):
                channel = s.get("channel_name", "未知")
                created_at = s.get("created_at")
                # 处理datetime对象和字符串
                if isinstance(created_at, datetime):
                    created = created_at.strftime("%Y-%m-%d")
                else:
                    created = str(created_at)[:10] if created_at else "未知"
                text_preview = s.get("summary_text", "")[:100]
                print(f"       {i}. {channel} ({created}): {text_preview}...")

    # 6. 最近5条总结详情
    print("\n" + "-" * 60)
    print("6. 最近5条总结详情")
    print("-" * 60)

    recent_summaries = await db.get_summaries(limit=5, start_date=now - timedelta(days=7))

    if not recent_summaries:
        print("  ⚠️  最近7天内没有总结记录")
    else:
        for i, s in enumerate(recent_summaries, 1):
            print(f"\n  [{i}] {s.get('channel_name', '未知频道')}")
            print(f"      时间: {s.get('created_at', '')}")
            print(f"      消息数: {s.get('message_count', 0)}")
            print(f"      内容: {s.get('summary_text', '')[:150]}...")

    # 7. 诊断结论
    print("\n" + "=" * 60)
    print("诊断结论")
    print("=" * 60)

    summaries_90days = await db.get_summaries(
        limit=10000, start_date=now - timedelta(days=90), end_date=now
    )

    if len(all_summaries) > 0 and len(summaries_90days) == 0:
        print("\n⚠️  问题确认：数据库中有总结记录，但90天内没有任何记录！")
        print("\n💡 这就是为什么关键词检索返回0条的原因：")
        print("   - memory_manager.search_summaries() 默认查询最近90天的记录")
        print("   - 你的所有总结都是90天之前的")
        print("   - 所以被时间过滤条件排除了")
        print("\n✅ 解决方案：")
        print("   1. 修改代码，当关键词检索返回0条时，放宽时间限制")
        print("   2. 或者调整默认的时间范围参数")
    elif len(summaries_90days) > 0:
        print("\n✅ 90天内有总结记录，问题可能出在其他地方")
        print(f"   - 90天内共有 {len(summaries_90days)} 条总结")
        print("   - 请检查关键词匹配逻辑")

    # 关闭数据库连接
    if hasattr(db, "close"):
        await db.close()

    print("\n" + "=" * 60)


if __name__ == "__main__":
    asyncio.run(main())
