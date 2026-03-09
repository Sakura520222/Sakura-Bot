# core/config/file_watcher.py
import asyncio
import logging
from pathlib import Path

from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

logger = logging.getLogger(__name__)


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
        # Early exit if stopped (thread-safe check)
        if self._stopped:
            return

        if event.is_directory:
            return

        # 只监控配置文件
        if not event.src_path.endswith(("config.json", ".env", "qa_persona.txt")):
            return

        logger.debug(f"检测到文件变更: {event.src_path}")

        # 使用 call_soon_threadsafe 确保跨线程安全
        self._loop.call_soon_threadsafe(self._handle_debounce)

    def _handle_debounce(self):
        """处理防抖逻辑（在事件循环线程中执行）"""
        # Don't schedule new operations if stopped
        if self._stopped:
            return

        # 取消之前的定时器
        if self._debounce_timer:
            self._debounce_timer.cancel()

        # 设置新的防抖定时器
        self._debounce_timer = self._loop.call_later(self._debounce_delay, self._trigger_reload)

    def _trigger_reload(self):
        """触发配置重载"""
        # Don't trigger if stopped (could happen during debounce delay)
        if self._stopped:
            logger.debug("FileWatcher已停止，跳过重载")
            return

        logger.info("防抖延迟结束，触发配置重载")

        # Store task reference for cleanup
        self._reload_task = asyncio.create_task(self._config_manager.reload_config())
