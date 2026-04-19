# webhook-docker

基于 Go 的 WebHook 执行器，使用 Docker 部署。服务接收受签名保护的 WebHook 请求后，按配置执行白名单动作组，支持本地容器执行与 SSH 执行两种模式。

## 仓库结构

```text
webhook-docker/
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── configs/
├── scripts/
├── cmd/
├── internal/
└── dev/
```

## 准备配置

1. 复制环境变量模板为 `.env`。

```bash
cp .env.example .env
```

2. 编辑 `.env`，至少设置 WebHook secret。

```env
GITHUB_BLOG_WEBHOOK_SECRET=replace-with-real-secret
```

3. 如需调整配置，编辑 [configs/webhook.yaml](configs/webhook.yaml)。容器内默认配置路径为 `/app/config/webhook.yaml`。

## 构建镜像

在仓库根目录执行：

```bash
docker build -t webhook-docker:latest -f Dockerfile .
```

## 启动服务

推荐使用 Compose 在仓库根目录启动：

```bash
docker-compose up --build -d
```

如果你的环境使用新版命令，也可以执行：

```bash
docker compose up --build -d
```

默认会对外暴露 `8080` 端口。健康检查接口为 `GET /health`，就绪检查接口为 `GET /ready`。

## 发送 WebHook

下面示例以 GitHub 风格签名为例。先准备请求体，再计算 HMAC-SHA256：

```bash
BODY='{"ref":"refs/heads/main","after":"0123456789abcdef"}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$GITHUB_BLOG_WEBHOOK_SECRET" -hex | sed 's/^.* //')

curl -i -X POST "http://127.0.0.1:8080/hooks/blog-update" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -H "X-Hub-Signature-256: sha256=$SIG" \
  -d "$BODY"
```

成功时返回 `202 Accepted`。签名错误时返回 `401 Unauthorized`，未配置或未启用的 Hook 返回 `404 Not Found`，同一 Hook 忙时返回 `409 Conflict`。

## 查看日志

使用 Compose 启动时可直接查看容器日志：

```bash
docker-compose logs -f
```

## 停止服务

```bash
docker-compose down
```
