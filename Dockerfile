# 使用Python 3.13 slim镜像作为基础
FROM python:3.13-slim

# 设置工作目录
WORKDIR /app

# 设置环境变量和时区
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    TZ=Asia/Shanghai

# 安装系统依赖并设置时区
RUN apt-get update && apt-get install -y \
    gcc \
    g++ \
    tzdata \
    && ln -sf /usr/share/zoneinfo/$TZ /etc/localtime \
    && echo $TZ > /etc/timezone \
    && rm -rf /var/lib/apt/lists/*

# 复制依赖文件
COPY requirements.txt .

# 安装Python依赖
RUN pip install --no-cache-dir -r requirements.txt

# 复制应用代码和启动脚本
COPY . .
COPY docker-entrypoint.sh .

# 设置启动脚本权限
RUN chmod +x docker-entrypoint.sh

# 创建非root用户
RUN useradd -m -u 1000 appuser && chown -R appuser:appuser /app
USER appuser

# 创建必要的目录和文件
RUN mkdir -p /app/data && \
    touch /app/data/config.json /app/data/prompt.txt /app/data/poll_prompt.txt /app/data/.last_summary_time.json /app/data/.poll_regenerations.json

# 设置数据卷
VOLUME ["/app/data"]

# 设置入口点
ENTRYPOINT ["./docker-entrypoint.sh"]

# 默认命令
CMD ["python", "main.py"]
