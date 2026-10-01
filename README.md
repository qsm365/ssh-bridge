# SSH Bridge

面向 AI Agent 的轻量 SSH 执行代理与事后审计工具。默认的 Local 模式以单文件运行，元数据保存到 SQLite；Server 模式的基础框架可连接 PostgreSQL 或 MySQL。两种模式的完整命令输出均保存为本地文件。

## 当前已实现

- localhost 管理页面默认以本机管理员身份打开，无需登录
- 首次启动自动生成 Agent Token，并在“Agent 接入”页面提供一次性展示和重新生成功能
- 目标主机管理（私钥或用户名 + 密码认证、可选的 Host Key 指纹校验）
- 目标主机可逻辑删除；删除后不再允许新执行，历史执行记录仍保留原主机关联
- 保存前及保存后的 SSH 登录联通性测试（不执行远程命令）
- Agent Bearer Token 鉴权
- 内置 OpenAPI 3.1 文档，以及不暴露私钥路径等管理信息的 Agent 目标主机列表
- 同一进程内置无状态 MCP Streamable HTTP 端点
- 首次执行自动生成会话 ID，每次执行自动生成执行 ID
- 异步 SSH 执行，以及会话 ID + 执行 ID 查询和长等待接口
- 通过占位符引用上传文件，自动上传到远端临时目录并在执行后清理
- 会话聚合、异常标记、命令与结果预览
- SQLite 元数据和本地 JSONL 完整输出；单次输出上限 50 MiB
- 服务重启时，将未完成记录标记为失败，避免一直停留在执行中
- `local` / `server` 双模式，以及 SQLite / PostgreSQL / MySQL 编号数据库迁移
- `/readyz` 数据库就绪检查和动态运行模式标识

Server 模式目前仍处于分阶段开发中，管理员登录和具名 Agent 权限将在后续阶段加入。在此之前 Server 模式同样只允许监听回环地址。初版暂不包含审批、记录清理、SSO 和 S3。

## 构建与运行

```bash
cd web
npm install
npm run build
cd ..
go build -o ssh-bridge ./cmd/ssh-bridge

./ssh-bridge --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

访问 `http://127.0.0.1:7408` 后直接进入管理页面。为了避免无登录管理台暴露到网络，本地模式只允许监听 localhost 或其他回环地址。

首次启动会生成 Agent Token 并以 `0600` 权限保存到数据目录的 `agent-token` 文件中。完整 Token 只会在“Agent 接入”页面首次读取时展示一次；重新生成后旧 Token 立即失效。也可以在数据目录尚未初始化时用 `SSH_BRIDGE_AGENT_TOKEN` 提供初始值，之后以本地 Token 文件为准。

### Server 模式（阶段一）

阶段一可使用 PostgreSQL 或 MySQL 保存元数据，但仍沿用现有的单 Agent Token，并且管理页面尚未增加登录保护，因此只能监听 localhost。数据库类型从连接地址协议自动识别。

PostgreSQL：

```bash
export SSH_BRIDGE_DATABASE_URL='postgres://ssh_bridge:password@127.0.0.1:5432/ssh_bridge?sslmode=disable'
./ssh-bridge --mode server --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

MySQL：

```bash
export SSH_BRIDGE_DATABASE_URL='mysql://ssh_bridge:password@127.0.0.1:3306/ssh_bridge?charset=utf8mb4'
./ssh-bridge --mode server --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

服务启动时会自动执行尚未应用的编号迁移。`GET /healthz` 表示进程存活，`GET /readyz` 会实际检查数据库连接。执行输出和上传文件仍写入 `--data-dir`，不会写入外部数据库。当前阶段不要把管理端口暴露到其他机器或公网。

首次运行建议按以下顺序操作：

1. 打开 `http://127.0.0.1:7408/agent-access`，复制首次生成的 Token 和 Agent 配置。
2. 在“目标主机”中添加主机并测试 SSH 登录。
3. 在 Agent 中添加 MCP Server 后，先调用 `list_targets` 确认连接，再执行简单只读命令。

## Agent 调用流程

运行中的服务会在 `http://127.0.0.1:7408/api/openapi.json` 提供 OpenAPI 3.1 文档。完整接入约定见 [Agent 接入说明](docs/agent-integration.md)。Agent 可先调用 `GET /api/v1/agent/targets` 获取不含私钥路径等管理信息的目标主机列表。

支持 MCP 的 Agent 也可以直接连接 `http://127.0.0.1:7408/mcp`，并使用相同的 Bearer Token。MCP 与普通 HTTP API 复用执行、审计和文件归档逻辑，不是独立服务。

首次调用不传 `session_id`：

```http
POST /api/v1/executions
Authorization: Bearer <agent-token>
Content-Type: application/json

{
  "target_id": "target_...",
  "session_title": "排查订单服务异常",
  "title": "查看服务状态",
  "command": "systemctl status order-api --no-pager"
}
```

服务立即返回 `202`：

```json
{
  "session_id": "session_...",
  "execution_id": "execution_...",
  "status": "pending"
}
```

同一 Agent 对话后续执行携带返回的 `session_id`。正常情况下调用下面的长等待接口一次即可获得最终状态和输出预览：

```http
GET /api/v1/sessions/{session_id}/executions/{execution_id}/wait?timeout=55
Authorization: Bearer <agent-token>
```

如果请求中断，使用不带 `/wait` 的同一路径主动恢复查询。状态包括 `pending`、`running`、`succeeded`、`failed`、`timeout`。

最终响应包含数据库内的短输出预览。需要完整输出时，调用下面的接口下载按顺序记录 stdout/stderr 的 JSONL 文件；每一行的数据使用 Base64 编码，避免二进制输出破坏记录格式。

```http
GET /api/v1/sessions/{session_id}/executions/{execution_id}/output
Authorization: Bearer <agent-token>
```

### 携带输入文件执行

需要上传 SQL、脚本或其他输入文件时，使用 `multipart/form-data`。`request` 字段保存执行请求 JSON，其余文件字段名就是占位符名称：

```bash
curl -X POST http://127.0.0.1:7408/api/v1/executions \
  -H 'Authorization: Bearer <agent-token>' \
  -F 'request={"target_id":"target_...","title":"执行 SQL","command":"mysql app < {{file:migration_sql}}"};type=application/json' \
  -F 'migration_sql=@./migration.sql'
```

调用方不能提供远端路径。系统会把 `{{file:migration_sql}}` 替换为内部生成的临时路径，执行结束后删除远端临时文件；原始输入文件保存在本地数据目录供审计。每次最多上传 8 个文件，单个文件最大 16 MiB，所有上传文件都必须在命令中被引用。

## 本地数据

默认数据目录为 `./ssh-bridge-data`：

- `ssh-bridge.db`：目标主机、会话、执行元数据和短输出预览
- `target-password.key`：加密 SSH 密码的本地密钥，仅服务进程可读；Server 模式也保存在此目录
- `outputs/YYYY/MM/DD/{execution_id}/output.jsonl`：按时间顺序归档的 stdout/stderr 输出块
- `artifacts/YYYY/MM/DD/{execution_id}/`：执行时上传的输入文件

私钥只读取管理员配置的服务端路径，不写入数据库内容或输出文件。SSH 密码在数据库中使用 AES-GCM 加密，管理接口不会返回明文；编辑密码主机时留空表示保留原密码。`target-password.key` 必须与数据库一起备份，丢失后已保存的密码无法恢复。Host Key 留空时仍不校验，建议在可用时配置。

## 升级与备份

升级前先停止 SSH Bridge，并备份整个数据目录。数据目录中可能存在 SQLite WAL 文件，只复制 `ssh-bridge.db` 不能保证得到一致的备份。

```bash
cp -R ./ssh-bridge-data ./ssh-bridge-data.backup
```

备份包含 Agent Token、主机连接配置、执行记录、完整输出和上传文件，应按照敏感数据保存。升级时用新版可执行文件替换旧文件，然后继续指定原来的 `--data-dir` 启动；启动后检查 `/healthz`，并通过管理页面确认目标主机和历史会话可以正常读取。
