# AgentDeck daemon 容器镜像：把界面 + PTY + 工作流编排跑在 Linux 服务器上，浏览器访问。
#
#   docker build -t agentdeckd:dev .
#   docker run -d --name agentdeckd -p 127.0.0.1:3210:3210 \
#     -e AGENTDECK_TOKEN=换成足够长的随机串 -v agentdeck-data:/data agentdeckd:dev
#
# 镜像里预装了 node/npm 和 git，方便在容器内安装各家 CLI（claude / codex / gemini 等）。

# —— 构建阶段：静态编译，界面资源随 exe 一起嵌入 ——
FROM golang:1.27 AS build
# go.mod 里的补丁版本比镜像新时让 Go 自动拉对应工具链（官方镜像默认是 local）。
ENV GOTOOLCHAIN=auto
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO 关掉才能静态链接，容器里不依赖 glibc 版本。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/agentdeckd .

# —— 运行阶段 ——
FROM node:22-bookworm-slim
ARG VERSION=dev
LABEL org.opencontainers.image.title="AgentDeck daemon" \
      org.opencontainers.image.version="${VERSION}"

# PTY 会话里要能跑 git、看日志、安装 CLI，这些是基础工具。
RUN DEBIAN_FRONTEND=noninteractive apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl git openssh-client tzdata ripgrep less procps \
 && rm -rf /var/lib/apt/lists/*

# 非 root 运行；基础镜像 node 已经占用 1000，这里让系统自动分配 UID。
RUN useradd -m -s /bin/bash agentdeck \
 && mkdir -p /data /workspace \
 && chown -R agentdeck:agentdeck /data /workspace

COPY --from=build /out/agentdeckd /usr/local/bin/agentdeckd
USER agentdeck
WORKDIR /workspace

# 容器内必须监听 0.0.0.0；访问令牌通过 AGENTDECK_TOKEN 或 -token 传入。
ENV AGENTDECK_TOKEN="" \
    AGENTDECK_REPO="yjjqiu/agentloop" \
    TERM="xterm-256color"
EXPOSE 3210
VOLUME ["/data"]

ENTRYPOINT ["/usr/local/bin/agentdeckd"]
CMD ["-addr", "0.0.0.0:3210", "-data", "/data"]