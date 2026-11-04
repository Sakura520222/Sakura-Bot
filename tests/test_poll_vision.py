import json
from dataclasses import replace
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import pytest

from core.ai import ai_client, poll_vision
from core.config.ai_runtime import AIConnectionSettings
from core.media import image_descriptions

IMAGE = "data:image/png;base64,PRIVATE_IMAGE_BODY"


@pytest.fixture
def runtime(monkeypatch):
    configs = {
        "poll": AIConnectionSettings(
            "poll-secret", "https://poll.example/v1", "poll-model", supports_vision=False
        ),
        "vision": AIConnectionSettings(
            "vision-secret",
            "https://vision.example/v1",
            "vision-model",
            supports_vision=True,
            vision_enabled=False,
            vision_ocr=False,
            vision_description=False,
        ),
    }
    clients = {}
    for purpose in configs:
        client = MagicMock()
        client.with_options.return_value = client
        client.chat.completions.create = AsyncMock()
        clients[purpose] = client
    clients["poll"].chat.completions.create.return_value = SimpleNamespace(
        choices=[
            SimpleNamespace(
                message=SimpleNamespace(
                    content=json.dumps(
                        {"question": "这只戴墨镜的猫想去哪度假？", "options": ["海边", "沙发"]}
                    )
                )
            )
        ]
    )
    clients["vision"].chat.completions.create.return_value = SimpleNamespace(
        choices=[SimpleNamespace(message=SimpleNamespace(content="图1：一只戴着墨镜的橘猫。"))]
    )
    selected = []

    def get_runtime(purpose="summary", *, config=None):
        selected.append((purpose, config))
        return clients[purpose], config.model if config is not None else configs[purpose].model

    monkeypatch.setattr(ai_client, "get_async_ai_runtime", get_runtime)
    monkeypatch.setattr(
        ai_client, "load_poll_prompt", lambda: "根据以下素材生成双语投票：\n{summary_text}"
    )
    monkeypatch.setattr(poll_vision, "effective_ai_config", lambda purpose: configs[purpose])
    saved = AsyncMock(return_value=["图1：一只戴着墨镜的橘猫。"])
    monkeypatch.setattr(image_descriptions, "saved_poll_descriptions", saved)
    return configs, clients, selected, saved


async def test_visual_poll_model_receives_images_directly_without_external_call(runtime):
    configs, clients, selected, saved = runtime
    configs["poll"] = replace(configs["poll"], supports_vision=True)
    result = await ai_client.generate_poll_from_summary("", images=[IMAGE])
    assert result["question"].startswith("这只")
    request = clients["poll"].chat.completions.create.call_args.kwargs
    assert request["model"] == "poll-model"
    assert request["messages"][-1]["content"][1] == {
        "type": "image_url",
        "image_url": {"url": IMAGE},
    }
    clients["vision"].chat.completions.create.assert_not_awaited()
    assert selected == [("poll", configs["poll"])]
    saved.assert_not_awaited()
    clients["poll"].with_options.assert_not_called()


async def test_text_poll_model_uses_external_description_with_channel_ocr_disabled(runtime):
    configs, clients, selected, saved = runtime
    result = await ai_client.generate_poll_from_summary("猫", images=[IMAGE])
    assert result["options"] == ["海边", "沙发"]
    poll_request = clients["poll"].chat.completions.create.call_args.kwargs
    text = poll_request["messages"][-1]["content"]
    assert isinstance(text, str) and "戴着墨镜的橘猫" in text and "猫" in text
    assert "PRIVATE_IMAGE_BODY" not in text and "base64" not in text
    assert selected == [("poll", configs["poll"])]
    saved.assert_awaited_once_with([IMAGE], configs["vision"])
    clients["poll"].with_options.assert_not_called()


async def test_both_purpose_parameters_are_pinned_before_vision_await(runtime):
    configs, clients, selected, saved = runtime
    original_poll, original_vision = configs["poll"], configs["vision"]

    async def describe(images, config):
        configs["poll"] = replace(original_poll, model="new-poll", api_key="new-secret")
        configs["vision"] = replace(original_vision, model="new-vision")
        assert config is original_vision
        return ["戴墨镜的猫"]

    saved.side_effect = describe
    await ai_client.generate_poll_from_summary("原帖图片", images=[IMAGE])
    assert selected == [("poll", original_poll)]
    assert clients["poll"].chat.completions.create.call_args.kwargs["model"] == "poll-model"
    saved.assert_awaited_once_with([IMAGE], original_vision)


@pytest.mark.parametrize("failure", ["capability", "empty", "upstream", "invalid_poll"])
async def test_image_failures_do_not_generate_a_fake_text_only_poll(runtime, caplog, failure):
    configs, clients, _, saved = runtime
    if failure == "capability":
        configs["vision"] = replace(configs["vision"], supports_vision=False)
        saved.side_effect = ValueError("vision_disabled")
    elif failure == "empty":
        saved.side_effect = ValueError("empty_saved_image_description")
    elif failure == "upstream":
        saved.side_effect = ValueError("vision-secret " + IMAGE)
    else:
        clients["poll"].chat.completions.create.return_value.choices[0].message.content = "not JSON"
    assert await ai_client.generate_poll_from_summary("有图片的帖子", images=[IMAGE]) is None
    if failure != "invalid_poll":
        clients["poll"].chat.completions.create.assert_not_awaited()
    assert "PRIVATE_IMAGE_BODY" not in caplog.text and "vision-secret" not in caplog.text


async def test_plain_text_poll_keeps_its_existing_single_model_request(runtime):
    _, clients, selected, saved = runtime
    assert await ai_client.generate_poll_from_summary("足够长的普通文本帖子，没有任何图片素材。")
    assert [purpose for purpose, _ in selected] == ["poll"]
    assert isinstance(
        clients["poll"].chat.completions.create.call_args.kwargs["messages"][-1]["content"], str
    )
    clients["vision"].chat.completions.create.assert_not_awaited()
    saved.assert_not_awaited()
