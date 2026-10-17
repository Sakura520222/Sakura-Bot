# core/config/file_watcher.py
import asyncio
import logging
from pathlib import Path

from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

logger = logging.getLogger(__name__)

# 提示词文件列表
PROMPT_FILES = ["prompt.txt", "poll_prompt.txt", "qa_persona.txt"]


class FileWatcher(FileSystemEventHandler):
    """配置文件监控器（带500ms防抖）"""

    def __init__(self, config_manager, event_loop: asyncio.AbstractEventLoop):
        """
        Args:
            config_manager: ConfigManager实例
            event_loop: asyncio事件循环
        """
        self._config_manager = config_manager
        self._loop = event_loop
        self._debounce_timer: asyncio.TimerHandle | None = None
        self._debounce_delay = 0.5  # 500ms
        self._observer = Observer()
        self._stopped = False  # Thread-safe stop flag
        self._reload_task: asyncio.Task | None = None  # Track reload task for cleanup

    def start(self, path: str):
        """启动文件监控"""
        watch_path = Path(path)
        if not watch_path.exists():
            logger.error(f"监控路径不存在: {path}")
            return

        self._stopped = False  # Reset stop flag on start
        self._observer.schedule(self, path, recursive=False)
        self._observer.start()
        logger.info(f"✅ 文件监控已启动: {path}")

    def stop(self):
        """停止文件监控（线程安全）"""
        # Set stop flag first to prevent new operations
        self._stopped = True

        # Stop observing files
        if self._observer.is_alive():
            self._observer.stop()
            self._observer.join()

        # Cancel debounce timer thread-safely
        if self._debounce_timer:
            # Use call_soon_threadsafe to ensure timer is cancelled in event loop thread
            self._loop.call_soon_threadsafe(self._debounce_timer.cancel)
            self._debounce_timer = None

        # Cancel active reload task if exists
        if self._reload_task and not self._reload_task.done():
            self._loop.call_soon_threadsafe(self._reload_task.cancel)
            self._reload_task = None

        logger.info("文件监控已停止")

    def on_modified(self, event):
        """文件修改回调（在独立线程中运行）"""
        self._on_path_changed(event.src_path, event.is_directory)

    def on_created(self, event):
        """部分平台将原子替换报告为新建事件。"""
        self._on_path_changed(event.src_path, event.is_directory)

    def on_moved(self, event):
        """同目录临时文件的 os.replace 必须以目标路径触发重载。"""
        self._on_path_changed(event.dest_path, event.is_directory)

    def _on_path_changed(self, path, is_directory):
        """统一过滤文件事件，忽略锁、历史备份和临时文件。"""
        # Early exit if stopped (thread-safe check)
        if self._stopped:
            return

        if is_directory:
            return

        # 只监控配置文件和提示词文件
        if not path.endswith(
            ("config.json", ".env", "qa_persona.txt", "poll_prompt.txt", "prompt.txt")
        ):
            return

        logger.debug(f"检测到文件变更: {path}")

        # 使用 call_soon_threadsafe 确保跨线程安全，传递文件路径
        self._loop.call_soon_threadsafe(lambda: self._handle_debounce_with_path(path))

    def _handle_debounce(self):
        """处理防抖逻辑（在事件循环线程中执行）"""
        # Don't schedule new operations if stopped
        if self._stopped:
            return

        # 取消之前的定时器
        if self._debounce_timer:
            self._debounce_timer.cancel()

        # 设置新的防抖定时器（不带文件路径，用于兼容旧调用）
        self._debounce_timer = self._loop.call_later(self._debounce_delay, self._trigger_reload)

    def _handle_debounce_with_path(self, file_path: str):
        """处理防抖逻辑（带文件路径）"""
        # Don't schedule new operations if stopped
        if self._stopped:
            return

        # 取消之前的定时器
        if self._debounce_timer:
            self._debounce_timer.cancel()

        # 设置新的防抖定时器（带文件路径）
        self._debounce_timer = self._loop.call_later(
            self._debounce_delay, lambda: self._trigger_reload(file_path)
        )

    def _trigger_reload(self, file_path: str = None):
        """触发配置或提示词重载"""
        # Don't trigger if stopped (could happen during debounce delay)
        if self._stopped:
            logger.debug("FileWatcher已停止，跳过重载")
            return

        logger.info("防抖延迟结束，触发重载")

        # 判断文件类型并触发相应的重载逻辑
        if file_path and file_path.endswith("config.json"):
            # 配置文件变更
            logger.info("检测到配置文件变更，触发配置重载")
            self._reload_task = asyncio.create_task(self._config_manager.reload_config())
        elif self._is_prompt_file(file_path):
            # 提示词文件变更
            prompt_type = self._get_prompt_type(file_path)
            logger.info(f"检测到提示词文件变更: {prompt_type}")
            self._reload_task = asyncio.create_task(
                self._handle_prompt_changed(file_path, prompt_type)
            )
        else:
            # 其他文件（.env等）也走配置重载
            logger.info(f"检测到其他文件变更，触发配置重载: {file_path}")
            self._reload_task = asyncio.create_task(self._config_manager.reload_config())

    def _is_prompt_file(self, file_path: str) -> bool:
        """判断是否为提示词文件"""
        if not file_path:
            return False
        return any(file_path.endswith(f) for f in PROMPT_FILES)

    def _get_prompt_type(self, file_path: str) -> str:
        """获取提示词类型"""
        if file_path.endswith("prompt.txt"):
            return "summary"
        elif file_path.endswith("poll_prompt.txt"):
            return "poll"
        elif file_path.endswith("qa_persona.txt"):
            return "qa_persona"
        return "unknown"

    async def _handle_prompt_changed(self, file_path: str, prompt_type: str):
        """处理提示词变更"""
        try:
            # 加载新内容
            content = await asyncio.to_thread(self._load_prompt_content, prompt_type)

            # 发布事件
            if self._config_manager.event_bus:
                from core.config.events import PromptChangedEvent

                await self._config_manager.event_bus.publish(
                    PromptChangedEvent(
                        prompt_type=prompt_type, file_path=file_path, content=content
                    )
                )

            logger.info(f"✅ 提示词已更新: {prompt_type} ({len(content)}字符)")
        except Exception as e:
            logger.error(f"处理提示词变更失败: {e}", exc_info=True)

    def _load_prompt_content(self, prompt_type: str) -> str:
        """同步加载提示词内容（在线程池中执行）"""
        if prompt_type == "summary":
            from core.infrastructure.config.prompt_manager import load_prompt

            return load_prompt()
        elif prompt_type == "poll":
            from core.infrastructure.config.poll_prompt_manager import load_poll_prompt

            return load_poll_prompt()
        elif prompt_type == "qa_persona":
            from core.config import get_qa_bot_persona

            return get_qa_bot_persona()
        else:
            raise ValueError(f"Unknown prompt type: {prompt_type}")
