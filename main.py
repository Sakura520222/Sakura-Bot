# Copyright 2026 Sakura-Bot
#
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
#
# - 署名：必须提供本项目的原始来源链接
# - 相同方式共享：衍生作品必须采用相同的许可证
#
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
# 许可证全文：参见 LICENSE 文件

"""
Sakura-Bot 主入口文件

使用模块化的初始化器架构，从臃肿的单文件重构为清晰的模块化结构。
"""

import asyncio
import logging
import os
import signal
import sys

# 添加项目根目录到 Python 路径
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

from core.bootstrap.app_bootstrap import AppBootstrap
from core.config import logger, set_shutdown_event
from core.process_manager import start_qa_bot, stop_qa_bot
from core.settings import validate_required_settings

# 版本信息
__version__ = "1.7.1"


async def graceful_shutdown_resources():
    """优雅关闭所有资源（已废弃，请使用 core.shutdown_manager.ShutdownManager）

    为了向后兼容，保留此函数但重定向到新的关机管理器
    """
    from core.shutdown_manager import get_shutdown_manager
    from core.telegram_client import get_active_client

    shutdown_manager = get_shutdown_manager()
    client = get_active_client()
    await shutdown_manager.graceful_shutdown(client=client)


async def main():
    """主函数 - 使用应用引导程序启动所有组件

    重构后的main函数非常简洁，所有初始化逻辑都委托给AppBootstrap。
    """
    # 抑制第三方库的 INFO 日志输出
    logging.getLogger("telethon").setLevel(logging.WARNING)
    logging.getLogger("telethon.client.updates").setLevel(logging.WARNING)

    # 使用应用引导程序启动应用
    bootstrap = AppBootstrap(version=__version__)
    await bootstrap.run()


if __name__ == "__main__":
    logger.info(f"===== Sakura-Bot v{__version__} 启动 ======")

    # 验证必要配置
    is_valid, missing = validate_required_settings()

    if not is_valid:
        logger.error(
            f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing)}"
        )
        print(f"错误: 请确保 .env 文件中配置了所有必要的 API 凭证。缺少: {', '.join(missing)}")
    else:
        logger.info("所有必要的 API 凭证已配置完成")

        # 创建一个事件循环和用于优雅关闭的 asyncio.Event
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        shutdown_event = asyncio.Event()

        # 定义信号处理函数
        def signal_handler(signum, frame):
            """处理 SIGTERM 和 SIGINT 信号"""
            signal_name = signal.Signals(signum).name
            logger.info(f"收到信号 {signal_name} ({signum})，开始优雅关闭...")
            # 设置事件以通知主循环开始关闭
            loop.call_soon_threadsafe(shutdown_event.set)

        # 注册信号处理器（在 Windows 上只支持 SIGINT，SIGTERM 可能不可用）
        try:
            signal.signal(signal.SIGTERM, signal_handler)
            logger.info("已注册 SIGTERM 信号处理器")
        except (AttributeError, ValueError) as e:
            logger.warning(f"无法注册 SIGTERM 信号处理器: {e}")

        try:
            signal.signal(signal.SIGINT, signal_handler)
            logger.info("已注册 SIGINT 信号处理器")
        except (AttributeError, ValueError) as e:
            logger.warning(f"无法注册 SIGINT 信号处理器: {e}")

        # 设置全局关机事件
        set_shutdown_event(shutdown_event)
        logger.info("全局关机事件已设置到 config 模块")

        # 启动问答Bot
        start_qa_bot()

        # 启动主函数
        try:
            logger.info("开始启动主函数...")

            # 创建主任务和等待关闭信号的任务
            async def run_with_shutdown():
                """运行主任务，同时监听关闭信号"""
                main_task = asyncio.create_task(main())

                # 等待关闭信号或主任务完成
                await shutdown_event.wait()

                # 如果收到关闭信号，取消主任务并执行优雅关闭
                if not main_task.done():
                    logger.info("取消主任务...")
                    main_task.cancel()

                    try:
                        await main_task
                    except asyncio.CancelledError:
                        logger.info("主任务已取消")
                    except Exception as e:
                        logger.error(f"主任务取消时出错: {type(e).__name__}: {e}")

                # 执行优雅关闭
                logger.info("执行优雅关闭...")
                await graceful_shutdown_resources()

                # 退出程序（使用 os._exit 确保立即退出并关闭控制台）
                from core.shutdown_manager import get_shutdown_manager

                shutdown_manager = get_shutdown_manager()
                shutdown_manager.perform_exit(0)

                # 这行代码不会被执行到，因为 perform_exit 会立即终止进程
                loop.stop()

            loop.run_until_complete(run_with_shutdown())

        except KeyboardInterrupt:
            logger.info("机器人服务已通过键盘中断停止")
        except Exception as e:
            logger.critical(f"主函数执行失败: {type(e).__name__}: {e}", exc_info=True)
        finally:
            # 确保问答Bot被停止
            logger.info("正在停止问答Bot...")
            stop_qa_bot()
            logger.info("程序已退出")
            # 关闭事件循环
            try:
                loop.run_until_complete(loop.shutdown_asyncgens())
            except Exception as e:
                logger.error(f"关闭异步生成器时出错: {e}")
            loop.close()
