# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""跨进程读取运行时配置，文件替换后下一次请求即使用新版本。"""

import logging
import threading
from copy import deepcopy
from pathlib import Path

from .persistence import ConfigPersistenceError, ConfigStore

logger = logging.getLogger(__name__)


class RuntimeConfigReader:
    def __init__(self):
        self._lock = threading.RLock()
        self._path = None
        self._version = None
        self._config = {}
        self._loaded = False

    def read(self) -> dict:
        import core.config as config_module

        path = Path(config_module._old_config.CONFIG_FILE).resolve()
        with self._lock:
            if path != self._path:
                self._path, self._version, self._config, self._loaded = path, None, {}, False
            try:
                stat = path.stat()
                version = (stat.st_ino, stat.st_mtime_ns, stat.st_size)
            except FileNotFoundError:
                if not self._loaded:
                    return {}
                # 临时缺失不能把已生效的限额或凭据退回环境变量。
                return deepcopy(self._config)
            if version != self._version:
                try:
                    config = ConfigStore(path).read()
                except ConfigPersistenceError:
                    if not self._loaded:
                        raise
                    logger.warning("运行时配置暂时无效，继续使用最近一次有效配置")
                else:
                    self._config, self._loaded = dict(config), True
                self._version = version
            return deepcopy(self._config)


runtime_config = RuntimeConfigReader()
