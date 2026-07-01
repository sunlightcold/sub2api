# Fork 版本修改与部署说明

本文档说明 `sunlightcold/sub2api` fork 相比上游 `Wei-Shaw/sub2api` 的主要改动、使用方式、生产部署方式和回滚方式。

## 适用场景

当 sub2api 作为另一层 sub2api 的上游时，如果入站是 `/v1/chat/completions`，旧逻辑在自动模式下可能会把请求转换为 `/v1/responses` 再发给上游。上游链路如果要求 `instructions` 或对 Responses API 兼容不完整，就可能出现：

```text
Instructions are required
```

本 fork 增加了 OpenAI Responses 路由模式，用于避免 Chat Completions 请求被自动转换到 Responses，同时保留必要的 Responses 兼容处理。

## 修改内容

### 1. 新增 OpenAI Responses 模式

新增推荐配置值：

```text
openai_responses_mode = preserve_chat_endpoint
```

页面显示为：

```text
保持 Chat 端点
```

语义：

| 入站端点 | preserve_chat_endpoint 下发给上游 |
| --- | --- |
| `/v1/chat/completions` | `/v1/chat/completions` |
| `/v1/responses` | 跟随 auto 的 Responses 兼容处理 |

也就是说，该模式只阻止 Chat Completions 自动转换为 Responses。客户端本身发 `/v1/responses` 时，仍保留 auto 模式下的兼容处理，例如在缺少 `instructions` 时补默认值。

### 2. instructions 处理差异

在 `preserve_chat_endpoint` 推荐模式下，`/v1/responses` 仍走 auto 兼容处理，因此会保留原有的默认 `instructions` 补齐行为；`/v1/chat/completions` 则不会被转换为 `/v1/responses`。

### 3. 其他模式保持原行为

已有模式仍保持原语义：

| 模式 | 行为 |
| --- | --- |
| `auto` | 保持上游原有自动探测/自动转换逻辑 |
| `force_responses` | 强制走 `/v1/responses` |
| `force_chat_completions` | 强制走 `/v1/chat/completions` |
| `preserve_chat_endpoint` | Chat 入站保持 `/v1/chat/completions`；Responses 入站保持 auto 兼容处理 |

### 4. 构建与 CI 修复

为保证 GitHub Actions 能构建 Docker 镜像，fork 中还调整了前端 pnpm 工作区配置：

- 新增 `frontend/pnpm-workspace.yaml`
- Docker 构建中复制 `frontend/pnpm-workspace.yaml`
- Docker 构建中固定 pnpm 版本
- 新增 GHCR Docker 镜像构建 workflow

当前 GitHub Actions 成功后会推送镜像：

```text
ghcr.io/sunlightcold/sub2api:main
```

也会同时推送当前 commit sha 对应的镜像 tag。

## 重要边界

`preserve_chat_endpoint` 不是字节级透明代理。

`preserve_chat_endpoint` 保证 `/v1/chat/completions` 不被转换为 `/v1/responses`，但 `/v1/responses` 仍保留 auto 兼容处理。请求仍会经过 sub2api 的正常网关逻辑，例如：

- 使用账号配置的上游 API key 发往上游
- 按账号模型映射改写 `model`，如果配置了模型映射
- 应用 `service_tier` 相关策略
- 对流式 Chat Completions 请求可能补 `stream_options.include_usage=true`
- 重新设置部分上游请求 header

如果你要排查“直连上游 sub2api”和“经过本 fork 中转后”参数是否完全一致，需要抓取上游 sub2api 实际收到的请求 body 进行对比，尤其关注流式请求。

## 生产部署方式

推荐使用 GitHub Actions 构建好的 GHCR 镜像，不建议在低配置服务器上执行 `docker build --no-cache`，容易占满 CPU/内存导致服务器卡死。

### 可选：主节点暴露数据库/Redis 给远程节点

主 `docker-compose.yml` 和 `docker-compose.local.yml` 默认保持上游行为：PostgreSQL/Redis 不暴露到宿主机，只允许容器内网访问。

如果你需要让另一台生图节点或远程应用节点连接主服务器的 PostgreSQL/Redis，必须显式叠加 override 文件：

```bash
docker compose -f docker-compose.yml -f docker-compose.remote-bindings.yml up -d
```

然后在 `.env` 中按需设置：

```env
POSTGRES_BIND_HOST=主服务器内网IP或VPN IP
POSTGRES_BIND_PORT=5432
REDIS_BIND_HOST=主服务器内网IP或VPN IP
REDIS_BIND_PORT=6379
```

不要把数据库或 Redis 暴露到公网。Redis 暴露给远程节点时必须设置强 `REDIS_PASSWORD`，并用防火墙只允许可信节点 IP 访问。

### 方式一：替换一键部署的镜像

如果生产环境是官方一键脚本部署的目录，例如：

```bash
mkdir -p sub2api-deploy && cd sub2api-deploy
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh | bash
docker compose up -d
```

进入该部署目录：

```bash
cd sub2api-deploy
```

备份 compose 文件：

```bash
cp docker-compose.yml docker-compose.yml.bak
```

编辑 `docker-compose.yml`，把 sub2api 服务的镜像从：

```yaml
image: weishaw/sub2api:latest
```

改成：

```yaml
image: ghcr.io/sunlightcold/sub2api:main
```

然后拉取并重启：

```bash
docker compose pull sub2api
docker compose up -d sub2api
docker compose logs -f sub2api
```

如果出现 `denied` 或 `unauthorized`，检查 GitHub Packages 中该镜像是否设为 Public；如果保持 Private，需要先在服务器执行 `docker login ghcr.io`。

### 方式二：使用固定 commit 镜像

`main` 会随着每次 push 更新。如果生产环境希望固定版本，可以在 GitHub Actions 的 Docker Image workflow 输出中找到 commit sha tag，然后把镜像写成：

```yaml
image: ghcr.io/sunlightcold/sub2api:<commit-sha>
```

再执行：

```bash
docker compose pull sub2api
docker compose up -d sub2api
```

### 方式三：服务器本地构建

仅在服务器资源足够时使用：

```bash
git clone https://github.com/sunlightcold/sub2api.git
cd sub2api
docker build -f Dockerfile -t sub2api:preserve-endpoint .
```

然后把部署目录里的 `docker-compose.yml` 改成：

```yaml
image: sub2api:preserve-endpoint
```

再重启：

```bash
docker compose up -d sub2api
```

低配置服务器不推荐这种方式。

## 使用 preserve_chat_endpoint

部署新镜像后，在管理后台编辑对应的 OpenAI APIKey 账号：

1. 找到 Responses API 相关模式配置。
2. 推荐选择 `保持 Chat 端点`。
3. 保存账号。
4. 用 `/v1/chat/completions` 测试中转链路。

保存后，该账号的 extra 中会包含：

```json
{
  "openai_responses_mode": "preserve_chat_endpoint"
}
```

## 验证方式

### 1. 确认容器使用 fork 镜像

```bash
docker compose ps
docker inspect sub2api --format '{{.Config.Image}}'
```

应看到：

```text
ghcr.io/sunlightcold/sub2api:main
```

或你指定的 commit sha tag。

### 2. 确认服务日志正常

```bash
docker compose logs -f sub2api
```

启动后不应出现数据库迁移失败、配置加载失败或镜像拉取失败。

### 3. 确认链路行为

使用 `/v1/chat/completions` 请求本机 sub2api，再观察上游 sub2api 日志或抓包：

- 上游应收到 `/v1/chat/completions`
- 不应因为本机这一层变成 `/v1/responses`
- 不应由本机这一层补默认 `instructions`

如果是流式请求，需要额外注意 `stream_options.include_usage` 是否影响检测结果。

## 回滚方式

如果需要回到官方镜像，把 `docker-compose.yml` 中镜像改回：

```yaml
image: weishaw/sub2api:latest
```

然后执行：

```bash
docker compose pull sub2api
docker compose up -d sub2api
docker compose logs -f sub2api
```

如果之前备份过 compose 文件，也可以直接恢复：

```bash
cp docker-compose.yml.bak docker-compose.yml
docker compose up -d sub2api
```

## 后续同步上游

本地仓库保留两个 remote：

```bash
git remote -v
```

一般应为：

```text
origin    https://github.com/sunlightcold/sub2api.git
upstream  https://github.com/Wei-Shaw/sub2api.git
```

同步官方更新时：

```bash
git fetch upstream
git checkout main
git merge upstream/main
```

解决冲突并测试后推送到 fork：

```bash
git push origin main
```

push 成功后，GitHub Actions 会重新构建并推送：

```text
ghcr.io/sunlightcold/sub2api:main
```
