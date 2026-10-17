# ====== 阶段 1: 构建前端 ======
FROM node:20-slim AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --include=dev --registry=https://registry.npmmirror.com
COPY web/ ./
RUN npm run build

# ====== 阶段 2: 构建后端 ======
FROM python:3.13-slim

WORKDIR /app

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    TZ=Asia/Shanghai \
    SAKURA_DOCKER=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    g++ \
    git \
    ca-certificates \
    tzdata \
    && ln -sf /usr/share/zoneinfo/$TZ /etc/localtime \
    && echo $TZ > /etc/timezone \
    && git config --system --add safe.directory /app \
    && git config --system --add url.https://github.com/.insteadOf git@github.com: \
    && git config --system --add url.https://github.com/.insteadOf ssh://git@github.com/ \
    && rm -rf /var/lib/apt/lists/*

# /update 在容器内安装前端依赖并构建，不依赖宿主机的 Node.js。
COPY --from=web-builder /usr/local/bin/node /usr/local/bin/node
COPY --from=web-builder /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# 复制依赖文件
COPY requirements.txt .

# 安装Python依赖
RUN pip install --no-cache-dir -r requirements.txt

# 复制应用代码和启动脚本
COPY . .
COPY docker-entrypoint.sh .

# 从前端构建阶段复制构建产物
COPY --from=web-builder /web/dist /app/web/dist
# 挂载 /app 后仍可使用镜像中的前端缓存和入口脚本。
COPY --from=web-builder /web/dist /opt/sakura/web-dist
COPY core/docker_runtime.py /usr/local/bin/sakura-docker-runtime.py
COPY docker-entrypoint.sh /usr/local/bin/sakura-entrypoint

# 设置启动脚本权限
RUN sed -i 's/\r$//' /usr/local/bin/sakura-entrypoint \
    && chmod +x /usr/local/bin/sakura-entrypoint \
    && python /usr/local/bin/sakura-docker-runtime.py record-image

# 创建必要的目录和文件，并设置权限
RUN mkdir -p /app/data/sessions && \
    chmod 755 /app/data /app/data/sessions && \
    touch /app/data/config.json /app/data/prompt.txt /app/data/poll_prompt.txt /app/data/.last_summary_time.json /app/data/.poll_regenerations.json

# 设置数据卷
VOLUME ["/app/data"]

# WebUI 端口
EXPOSE 8080

# 设置入口点
ENTRYPOINT ["/usr/local/bin/sakura-entrypoint"]

# 默认命令
CMD ["python", "main.py"]
