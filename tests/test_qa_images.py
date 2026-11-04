import io
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from PIL import Image

from core.ai.qa_images import (
    MAX_IMAGE_BYTES,
    ImageContext,
    ImageInputError,
    ImageRef,
    download_image,
    image_data_url,
    telegram_image_ref,
)


def test_pending_consumption_followups_replacement_and_scope_isolation():
    store = ImageContext()
    a, b = (1, 10), (2, 10)
    for index in (3, 1, 2):
        assert store.add(a, ImageRef(str(index), index))[0]
    assert not store.add(a, ImageRef("2", 2))[0]
    store.add(b, ImageRef("other", 1))
    first = store.begin(a, "session", "这是什么")
    assert [r.message_id for r in first.refs] == [1, 2, 3]
    with pytest.raises(ImageInputError, match="仍在处理"):
        store.begin(a, "session", "重复问题")
    store.finish(first, True)
    assert store.begin(a, "session", "讲个笑话") is None
    followup = store.begin(a, "session", "它是什么颜色？")
    assert len(followup.refs) == 3
    store.finish(followup, True)
    store.add(a, ImageRef("new", 4))
    current = store.begin(a, "session", "比较一下")
    assert [r.file_id for r in current.refs] == ["new"]
    store.finish(current, True)
    assert [r.file_id for r in store.begin(b, "other-session", "这是什么").refs] == ["other"]
    assert store.begin((1, 11), "session", "这是什么") is None
    assert store.begin(a, "new-session", "它是什么颜色？") is None


def test_expiry_limits_failure_retry_clear_and_atomic_lease():
    now = [1000.0]
    store = ImageContext(ttl=300, now=lambda: now[0])
    scope = (1, 1)
    for index in range(4):
        store.add(scope, ImageRef(str(index), index, now[0]))
    with pytest.raises(ImageInputError, match="最多 4"):
        store.add(scope, ImageRef("5", 5, now[0]))

    def begin(_):
        try:
            return store.begin(scope, "s", "这是什么")
        except ImageInputError:
            return None

    with ThreadPoolExecutor(max_workers=8) as pool:
        leases = [lease for lease in pool.map(begin, range(8)) if lease]
    assert len(leases) == 1
    store.finish(leases[0], False)
    retry = store.begin(scope, "s", "再试一次")
    assert len(retry.refs) == 4
    store.clear_user(1)
    store.finish(retry, False)
    assert not store.has_context(scope)
    store.add(scope, ImageRef("expires", 6, now[0]))
    now[0] += 301
    store.cleanup()
    assert not store.states


@pytest.mark.parametrize("fmt,mime", [("PNG", "image/png"), ("JPEG", "image/jpeg")])
def test_actual_format_and_corruption(fmt, mime):
    image = Image.new("RGB", (3, 2), "red")
    output = io.BytesIO()
    image.save(output, format=fmt)
    assert image_data_url(output.getvalue()).startswith(f"data:{mime};base64,")
    with pytest.raises(ImageInputError):
        image_data_url(output.getvalue()[:25])


def test_format_size_pixel_limits_and_telegram_documents():
    for content in (b"not an image", b"GIF89a" + b"x" * 20, b"x" * (MAX_IMAGE_BYTES + 1)):
        with pytest.raises(ImageInputError):
            image_data_url(content)
    huge = io.BytesIO()
    Image.new("1", (9000, 2)).save(huge, format="PNG")
    with pytest.raises(ImageInputError, match="像素"):
        image_data_url(huge.getvalue())
    doc = SimpleNamespace(
        mime_type="image/jpeg", file_name="image.jpg", file_id="telegram", file_size=100
    )
    message = SimpleNamespace(photo=[], document=doc, message_id=10)
    assert telegram_image_ref(message).file_id == "telegram"
    doc.mime_type = "image/gif"
    with pytest.raises(ImageInputError):
        telegram_image_ref(message)


async def test_download_failures_never_expose_telegram_token():
    bot = SimpleNamespace(
        get_file=AsyncMock(side_effect=ValueError("https://api.telegram.org/file/botSECRET"))
    )
    with pytest.raises(ImageInputError) as error:
        await download_image(bot, ImageRef("file", 1))
    assert "SECRET" not in str(error.value)
    bot.get_file = AsyncMock(
        return_value=SimpleNamespace(file_size=10, file_path="https://attacker.example/image")
    )
    with pytest.raises(ImageInputError, match="地址无效"):
        await download_image(bot, ImageRef("file", 1))
