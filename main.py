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
from pathlib import Path

# 添加项目根目录到 Python 路径
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

from core.bootstrap.app_bootstrap import AppBootstrap
from core.config import (
    AsyncIOEventBus,
    ConfigErrorNotifier,
    ConfigManager,
    FileWatcher,
    logger,
    set_shutdown_event,
)
from core.settings import get_admin_list, get_bot_token, validate_required_settings
from core.system.process_manager import start_qa_bot, stop_qa_bot

# 版本信息
__version__ = "1.7.3"


async def graceful_shutdown_resources():
    """优雅关闭所有资源（已废弃，请使用 core.system.shutdown_manager.ShutdownManager）

    为了向后兼容，保留此函数但重定向到新的关机管理器
    """
    from core.system.shutdown_manager import get_shutdown_manager
    from core.telegram.client import get_active_client

    shutdown_manager = get_shutdown_manager()
    client = get_active_client()
    await shutdown_manager.graceful_shutdown(client=client)


async def main(config_manager: ConfigManager = None):
    """主函数 - 使用应用引导程序启动所有组件

    重构后的main函数非常简洁，所有初始化逻辑都委托给AppBootstrap。

    Args:
        config_manager: 可选的配置管理器实例，用于配置热重载支持
    """
    # 抑制第三方库的 INFO 日志输出
    logging.getLogger("telethon").setLevel(logging.WARNING)
    logging.getLogger("telethon.client.updates").setLevel(logging.WARNING)

    # 使用应用引导程序启动应用
    bootstrap = AppBootstrap(version=__version__, config_manager=config_manager)
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

        # 初始化配置管理器（同步）
        config_manager = ConfigManager(loop=None)
        success, error = config_manager.initialize_sync(Path("data/config.json"))

        if not success:
            logger.warning(f"配置初始化失败: {error}")
            logger.info("将在没有配置热重载支持的情况下继续运行")
            config_manager = None
        else:
            logger.info("✅ 配置已同步加载，热重载已启用")

        # 创建一个事件循环和用于优雅关闭的 asyncio.Event
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)

        # 如果配置管理器初始化成功，设置事件循环
        if config_manager:
            config_manager._loop = loop

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
                # 初始化事件总线和文件监控（如果配置管理器可用）
                if config_manager:
                    event_bus = AsyncIOEventBus(sequential_mode=True)
                    config_manager.event_bus = event_bus

                    # 订阅配置错误通知器
                    bot_token = get_bot_token()
                    admin_list = get_admin_list()
                    # 获取第一个管理员ID作为通知接收者
                    admin_chat_id = str(admin_list[0]) if admin_list else ""
                    config_error_notifier = ConfigErrorNotifier(bot_token, admin_chat_id)

                    # 订阅配置验证失败事件
                    from core.config.events import ConfigValidationErrorEvent

                    await event_bus.subscribe(
                        ConfigValidationErrorEvent,
                        config_error_notifier.on_config_validation_error,
                        priority=event_bus.PRIORITY_HIGH,
                    )
                    logger.info("✅ 配置错误通知器已订阅")

                    file_watcher = FileWatcher(config_manager, loop)
                    file_watcher.start(str(Path("data")))
                    logger.info("✅ 文件监控已启动")

                main_task = asyncio.create_task(main(config_manager))

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

                # 停止文件监控和事件总线
                if config_manager:
                    logger.info("正在关闭配置热重载系统...")
                    if "file_watcher" in locals():
                        file_watcher.stop()
                    if "event_bus" in locals():
                        await event_bus.shutdown()
                    logger.info("配置热重载系统已关闭")

                # 退出程序（使用 os._exit 确保立即退出并关闭控制台）
                from core.system.shutdown_manager import get_shutdown_manager

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
