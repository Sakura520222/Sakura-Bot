# Copyright 2026 Sakura-Bot
# 本项目采用 AGPL-3.0 许可
# 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

"""Static image validation shared by chat and Wiki uploads."""

import io

from PIL import Image

MAX_IMAGE_BYTES = 8 * 1024 * 1024
MAX_PIXELS = 12_000_000
MAX_SIDE = 8192


class ImageInputError(ValueError):
    """User-facing image failure without file contents or upstream errors."""


def validate_image(data):
    if not data or len(data) > MAX_IMAGE_BYTES:
        raise ImageInputError("单张图片不能超过 8 MiB，且不能为空。")
    try:
        with Image.open(io.BytesIO(data)) as img:
            fmt = img.format
            if fmt not in {"PNG", "JPEG"}:
                raise ImageInputError("目前只支持 PNG / JPEG 图片。")
            if (
                img.width * img.height > MAX_PIXELS
                or max(img.size) > MAX_SIDE
                or getattr(img, "n_frames", 1) != 1
            ):
                raise ImageInputError("图片像素过大或为动画，请发送静态 PNG / JPEG。")
            img.verify()
        with Image.open(io.BytesIO(data)) as img:
            img.load()
    except ImageInputError:
        raise
    except Exception:
        raise ImageInputError("图片已损坏或格式不受支持，请重新发送 PNG / JPEG。") from None
    return "image/png" if fmt == "PNG" else "image/jpeg"
