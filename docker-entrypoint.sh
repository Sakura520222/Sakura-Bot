#!/bin/bash
set -e

echo "========================================"
echo "Sakura-Bot Docker 容器启动"
echo "========================================"

# 检查必要的环境变量
echo "检查环境变量配置..."
required_vars=("TELEGRAM_API_ID" "TELEGRAM_API_HASH" "TELEGRAM_BOT_TOKEN")
missing_vars=()

for var in "${required_vars[@]}"; do
    if [ -z "${!var}" ]; then
        missing_vars+=("$var")
    fi
done

if [ ${#missing_vars[@]} -gt 0 ]; then
    echo "错误: 以下必要的环境变量未设置:"
    for var in "${missing_vars[@]}"; do
        echo "  - $var"
    done
    echo "请确保在 .env 文件中正确配置这些变量"
    exit 1
fi

# 检查AI API密钥
if [ -z "$LLM_API_KEY" ] && [ -z "$DEEPSEEK_API_KEY" ]; then
    echo "警告: 未设置AI API密钥 (LLM_API_KEY 或 DEEPSEEK_API_KEY)"
    echo "机器人可能无法正常工作"
fi

echo "环境变量检查完成"

# /app 可由宿主机源码覆盖；重建容器后同步新增依赖并恢复前端产物。
# 自定义诊断命令（例如 python --version）无需安装依赖或构建前端。
# prepare 脚本优先用挂载的 /app 副本：镜像内置副本不会随源码更新，
# 否则 prepare 的行为（如前端产物指纹核对）会永远停留在构建镜像时的版本。
if [[ "${1:-}" == "python" || "${1:-}" == "python3" ]] && \
   [[ "${2:-}" == "main.py" || "${2:-}" == "qa_bot.py" ]]; then
    RUNTIME_SCRIPT=/usr/local/bin/sakura-docker-runtime.py
    if [ -f /app/core/docker_runtime.py ]; then
        RUNTIME_SCRIPT=/app/core/docker_runtime.py
    fi
    python "$RUNTIME_SCRIPT" prepare
fi

# 检查并创建必要的文件
mkdir -p /app/data
if [ ! -f /app/data/config.json ]; then
    echo "创建默认 config.json 文件"
    echo '{}' > /app/data/config.json
fi

if [ ! -f /app/data/prompt.txt ]; then
    echo "创建默认 prompt.txt 文件"
    echo "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：" > /app/data/prompt.txt
fi

if [ ! -f /app/data/poll_prompt.txt ]; then
    echo "创建默认 poll_prompt.txt 文件"
    cat > /app/data/poll_prompt.txt << 'EOF'
根据以下内容生成一个有趣的单选投票。
1. 趣味性：题目和选项要幽默、有梗，具有互动性，避免平铺直叙。
2. 双语要求：整体内容中文在上，英文在下。在 JSON 字段内部，中文与英文之间使用 " / " 分隔。
3. 输出格式：仅输出标准的 JSON 格式，严禁包含任何前言、解释或 Markdown 代码块标识符。
4. JSON 结构：
{
  "question": "中文题目 / English Question",
  "options": [
    "中文选项1 / English Option 1",
    "中文选项2 / English Option 2",
    "中文选项3 / English Option 3",
    "中文选项4 / English Option 4"
  ]
}

# Input Content
{summary_text}
EOF
fi

if [ ! -f /app/data/.last_summary_time.json ]; then
    echo "创建默认 .last_summary_time.json 文件"
    echo '{}' > /app/data/.last_summary_time.json
fi

if [ ! -f /app/data/.poll_regenerations.json ]; then
    echo "创建默认 .poll_regenerations.json 文件"
    echo '{}' > /app/data/.poll_regenerations.json
fi

# 确保会话目录存在并设置权限
mkdir -p /app/data/sessions && chmod 755 /app/data/sessions

# 检查会话文件
if [ ! -f /app/data/sessions/bot_session.session ]; then
    echo "注意: 未找到会话文件 bot_session.session"
    echo "首次运行需要Telegram登录授权"
    echo "请按照提示完成登录流程"
fi

echo "========================================"
echo "启动参数: $@"
echo "========================================"

# 执行主程序
exec "$@"
