# Copyright 2026 Sakura-Bot
# 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""完整配置的事务存储：严格读取、增量修改、原子替换及有效历史。"""

import json
import os
import tempfile
import threading
import time
from contextlib import contextmanager
from copy import deepcopy
from datetime import UTC, datetime
from pathlib import Path
from sys import version_info
from uuid import uuid4

from .validator import ConfigValidator

_THREAD_LOCK = threading.RLock()
_MISSING = object()
BACKUP_LIMIT = 30


def _copy_windows_dacl(source: Path, target: Path, *, protect: bool = False) -> None:
    """复制 DACL 及继承保护位，不能用 os.chmod 代替 Windows 权限保护。

    https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-getfilesecurityw
    """
    import ctypes
    from ctypes import wintypes

    advapi = ctypes.WinDLL("advapi32", use_last_error=True)
    get_security = advapi.GetFileSecurityW
    get_security.argtypes = [
        wintypes.LPCWSTR,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.DWORD),
    ]
    get_security.restype = wintypes.BOOL
    get_control = advapi.GetSecurityDescriptorControl
    get_control.argtypes = [
        wintypes.LPVOID,
        ctypes.POINTER(ctypes.c_ushort),
        ctypes.POINTER(wintypes.DWORD),
    ]
    get_control.restype = wintypes.BOOL
    set_security = advapi.SetFileSecurityW
    set_security.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.LPVOID]
    set_security.restype = wintypes.BOOL

    size = wintypes.DWORD()
    get_security(str(source), 0x4, None, 0, ctypes.byref(size))
    if not size.value:
        raise OSError("读取配置文件权限失败")
    descriptor = ctypes.create_string_buffer(size.value)
    if not get_security(str(source), 0x4, descriptor, size, ctypes.byref(size)):
        raise OSError("读取配置文件权限失败")
    control, revision = ctypes.c_ushort(), wintypes.DWORD()
    if not get_control(descriptor, ctypes.byref(control), ctypes.byref(revision)):
        raise OSError("读取配置文件继承权限失败")
    # SE_DACL_PROTECTED / PROTECTED_DACL_SECURITY_INFORMATION。
    flags = 0x4 | (0x80000000 if protect or control.value & 0x1000 else 0x20000000)
    if not set_security(str(target), flags, descriptor):
        raise OSError("保留配置文件权限失败")


class ConfigPersistenceError(RuntimeError):
    """可安全展示的配置读写错误，不包含配置值或底层异常原文。"""


class ConfigConflictError(ConfigPersistenceError):
    """同一字段已被另一次操作修改，调用方需要重新读取。"""


class ConfigSnapshot(dict):
    """兼容 dict 的读取结果，保留独立基线以精确提交修改及删除。"""

    def __init__(self, config: dict, path: Path, *, valid: bool = True):
        super().__init__(deepcopy(config))
        self._baseline = deepcopy(config)
        self.path = path.resolve()
        self.valid = valid


def configs_equal(left, right) -> bool:
    """按 JSON 类型和值比较，不能把 true、1、1.0 混为同一个值。"""
    if isinstance(left, dict) and isinstance(right, dict):
        return left.keys() == right.keys() and all(
            configs_equal(value, right[key]) for key, value in left.items()
        )
    if type(left) is not type(right):
        return False
    if isinstance(left, list):
        return len(left) == len(right) and all(
            configs_equal(first, second) for first, second in zip(left, right, strict=True)
        )
    return left == right


def _apply_changes(before: dict, after: dict, latest: dict) -> dict:
    """将有意修改的路径应用到最新配置；数组整体比较，同路径冲突拒绝保存。"""
    result = deepcopy(latest)
    for key in before.keys() | after.keys():
        old = before.get(key, _MISSING)
        new = after.get(key, _MISSING)
        current = latest.get(key, _MISSING)
        if configs_equal(old, new):
            continue
        if (
            isinstance(new, dict)
            and isinstance(current, dict)
            and (isinstance(old, dict) or old is _MISSING)
        ):
            result[key] = _apply_changes({} if old is _MISSING else old, new, current)
        elif not configs_equal(current, old) and not configs_equal(current, new):
            raise ConfigConflictError("配置字段已被其他操作修改，请重新读取后重试")
        elif new is _MISSING:
            result.pop(key, None)
        else:
            result[key] = deepcopy(new)
    return result


class ConfigStore:
    """所有 config.json 写入口共享的同步持久化边界。"""

    def __init__(self, path: str | Path):
        self.path = Path(path).resolve()
        self.backup_path = self.path.with_name(self.path.name + ".last_valid")
        self.history_dir = self.path.with_name(self.path.name + ".backups")

    @contextmanager
    def _locked(self):
        """线程锁加操作系统文件锁，覆盖读取、校验、备份和提交整个事务。"""
        with _THREAD_LOCK:
            lock_path = self.path.with_name(self.path.name + ".lock")
            try:
                fd = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
            except OSError:
                raise ConfigPersistenceError("无法锁定配置文件，配置未修改") from None
            with os.fdopen(fd, "r+b") as lock_file:
                if os.name == "nt":
                    import msvcrt

                    # Windows 允许锁定 EOF 后的区域。不要在加锁前初始化字节：
                    # 另一进程可能已经锁住该字节，初始化写入会产生共享冲突。
                    deadline = time.monotonic() + 10
                    while True:
                        try:
                            lock_file.seek(0)
                            msvcrt.locking(lock_file.fileno(), msvcrt.LK_NBLCK, 1)
                            break
                        except OSError:
                            if time.monotonic() >= deadline:
                                raise ConfigPersistenceError("等待配置文件锁超时") from None
                            time.sleep(0.02)
                else:
                    import fcntl

                    fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
                try:
                    yield
                finally:
                    if os.name == "nt":
                        lock_file.seek(0)
                        msvcrt.locking(lock_file.fileno(), msvcrt.LK_UNLCK, 1)
                    else:
                        fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)

    def _validate(self, config: dict, previous: dict | None = None) -> None:
        error = ConfigValidator.validate_config(config, self.path)
        if error is None and previous is not None:
            error = ConfigValidator.validate_transition(previous, config, self.path)
        if error is not None:
            raise ConfigPersistenceError(error.error)

    def _read_file(self, path: Path) -> dict:
        success, error, config = ConfigValidator.validate_config_file(path)
        if not success:
            raise ConfigPersistenceError(error.error)
        return config

    def _history_files(self) -> list[Path]:
        return sorted(self.history_dir.glob("*.json"), reverse=True)

    def _recovery_config(self) -> dict | None:
        # 历史文件采用 UTC 时间排序；启动不会用当前磁盘覆盖已有的 last_valid。
        for path in [*self._history_files(), self.backup_path]:
            try:
                return self._read_file(path)
            except ConfigPersistenceError:
                continue
        return None

    def _read_current(self, fallback: dict | None = None) -> dict:
        config = self._read_file(self.path)
        recovery = self._recovery_config()
        self._validate(config, recovery if recovery is not None else fallback)
        return config

    @staticmethod
    def _encode(config: dict) -> bytes:
        try:
            return (
                json.dumps(config, ensure_ascii=False, indent=2, allow_nan=False) + "\n"
            ).encode("utf-8")
        except (TypeError, ValueError, UnicodeError):
            raise ConfigPersistenceError("配置必须是可序列化的 JSON 对象") from None

    @staticmethod
    def _atomic_write(path: Path, content: bytes, *, preserve_permissions: bool = True) -> None:
        temp_path = None
        staging_dir = None
        try:
            temp_parent = path.parent
            if os.name == "nt":
                if version_info < (3, 13):
                    raise ConfigPersistenceError("Windows 安全配置写入需要 Python 3.13 或更高版本")
                # Python 3.13+ 在 Windows 上以私有 DACL 创建 0o700 目录。
                # 先在同卷私有目录写完，再复制目标 DACL，避免临时文件继承
                # data/ 的宽松权限，使凭据在写入期间可被其他用户读取。
                staging_dir = Path(tempfile.mkdtemp(prefix=".config-write-", dir=path.parent))
                temp_parent = staging_dir
            prefix = ".write-" if os.name == "nt" else f".{path.name}."
            fd, name = tempfile.mkstemp(prefix=prefix, suffix=".tmp", dir=temp_parent)
            temp_path = Path(name)
            with os.fdopen(fd, "wb") as stream:
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
            if os.name == "nt":
                if preserve_permissions and path.exists():
                    _copy_windows_dacl(path, temp_path)
                else:
                    _copy_windows_dacl(temp_path, temp_path, protect=True)
            elif preserve_permissions and path.exists():
                os.chmod(temp_path, path.stat().st_mode & 0o777)
            os.replace(temp_path, path)
        except OSError:
            raise ConfigPersistenceError("原子写入配置失败，原文件未替换") from None
        finally:
            if temp_path is not None:
                try:
                    temp_path.unlink(missing_ok=True)
                except OSError:
                    pass
            if staging_dir is not None:
                try:
                    staging_dir.rmdir()
                except OSError:
                    pass

    def _archive(self, config: dict) -> None:
        histories = self._history_files()
        if histories:
            try:
                if configs_equal(self._read_file(histories[0]), config):
                    return
            except ConfigPersistenceError:
                pass
        self.history_dir.mkdir(mode=0o700, exist_ok=True)
        name = datetime.now(UTC).strftime("%Y%m%dT%H%M%S.%fZ") + f"-{uuid4().hex[:8]}.json"
        path = self.history_dir / name
        # 独占创建，历史内容永不就地覆盖；只有完整同步的快照才保留。
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            if os.name == "nt":
                # 独占预留一个空文件，然后替换为私有文件。即使历史目录原先
                # 权限宽松，凭据也不会写入继承宽松 DACL 的文件句柄。
                os.close(fd)
                self._atomic_write(path, self._encode(config), preserve_permissions=False)
            else:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(self._encode(config))
                    stream.flush()
                    os.fsync(stream.fileno())
        except Exception:
            path.unlink(missing_ok=True)
            raise

    def _backup(self, config: dict, *, replace_last_valid: bool = True) -> None:
        try:
            self._validate(config)
            self._archive(config)
            if not replace_last_valid:
                try:
                    self._read_file(self.backup_path)
                    return
                except ConfigPersistenceError:
                    pass
            self._atomic_write(self.backup_path, self._encode(config), preserve_permissions=False)
        except (OSError, ConfigPersistenceError):
            raise ConfigPersistenceError("保存有效配置备份失败，已拒绝更新") from None

    def _prune_history(self) -> None:
        # 清理失败不改变已经成功提交的结果，也不会删除保留范围内的恢复版本。
        try:
            expired = self._history_files()[BACKUP_LIMIT:]
        except OSError:
            return
        for path in expired:
            try:
                path.unlink()
            except OSError:
                pass

    def read(self) -> ConfigSnapshot:
        with self._locked():
            return ConfigSnapshot(self._read_current(), self.path)

    def initialize(self) -> dict:
        """严格验证启动配置，保留已有恢复点，只补充独立有效历史。"""
        with self._locked():
            config = self._read_current()
            self._backup(config, replace_last_valid=False)
            self._prune_history()
            return deepcopy(config)

    def _commit(self, current: dict, candidate: dict) -> ConfigSnapshot:
        self._validate(candidate, current)
        encoded = self._encode(candidate)
        if not configs_equal(candidate, current):
            self._backup(current)
            self._atomic_write(self.path, encoded)
            self._prune_history()
        return ConfigSnapshot(candidate, self.path)

    def replace(self, config: dict) -> ConfigSnapshot:
        """完整替换，明确保留字段删除语义；不接受缺失或已损坏的现有文件。"""
        self._validate(config)
        with self._locked():
            return self._commit(self._read_current(), deepcopy(dict(config)))

    def update_fields(self, fields: dict) -> ConfigSnapshot:
        """从最新完整配置开始，仅替换明确提供的顶层字段。"""
        with self._locked():
            current = self._read_current()
            candidate = deepcopy(current)
            candidate.update(deepcopy(fields))
            return self._commit(current, candidate)

    def save_changes(self, snapshot: ConfigSnapshot) -> ConfigSnapshot:
        """提交读取基线后的差异，合并不同路径的并发更新，保留显式删除。"""
        if (
            not isinstance(snapshot, ConfigSnapshot)
            or not snapshot.valid
            or snapshot.path != self.path
        ):
            raise ConfigPersistenceError("缺少有效的配置读取基线，已拒绝保存")
        with self._locked():
            current = self._read_current()
            candidate = _apply_changes(snapshot._baseline, dict(snapshot), current)
            return self._commit(current, candidate)

    def reload(self, previous: dict | None) -> tuple[dict | None, str, bool]:
        """在同一文件锁内校验和回滚，避免回滚覆盖另一入口的新提交。"""
        with self._locked():
            try:
                # 历史可能比 watchdog 尚未处理的内存快照更新。以最近的有效
                # 恢复版本校验，不能用旧快照累计的差异撤销已成功提交的修改。
                config = self._read_current(previous)
            except ConfigPersistenceError as error:
                recovery = self._recovery_config()
                if recovery is None and previous is not None:
                    recovery = deepcopy(previous)
                if recovery is None:
                    return None, str(error), False
                try:
                    self._validate(recovery)
                    self._atomic_write(self.path, self._encode(recovery))
                except ConfigPersistenceError:
                    return None, str(error), False
                return recovery, str(error), True

            if not configs_equal(config, previous):
                # 只有内存快照仍是最近的恢复版本时才更新 last_valid，避免
                # 多次后台写入后，将旧内存值重新当作最新备份。
                if previous is not None and configs_equal(previous, self._recovery_config()):
                    self._backup(previous)
                # 校验成功后才将磁盘版本纳入历史，坏输入不会污染恢复版本。
                self._backup(config, replace_last_valid=False)
                self._prune_history()
            return config, "", False
