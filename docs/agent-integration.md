# Agent 接入 SSH Bridge

SSH Bridge 本地模式在 `/api/openapi.json` 提供随可执行文件发布的 OpenAPI 3.1 文档。Agent 请求使用：

```http
Authorization: Bearer <SSH_BRIDGE_AGENT_TOKEN>
```

## MCP 接入

MCP Streamable HTTP 地址为：

```text
http://127.0.0.1:7408/mcp
```

MCP 使用与 HTTP API 相同的 Bearer Token。服务采用无状态协议，不创建额外的 MCP 会话；SSH Bridge 的业务会话由 `execute_command` 返回的 `session_id` 表示，并由 Agent 在同一对话的后续调用中继续传递。

提供以下工具：

- `list_targets`：获取不含 SSH 凭据的可用目标主机。
- `execute_command`：异步发起命令并返回会话 ID、执行 ID 和 `pending` 状态。
- `get_execution`：在请求中断后查询状态和输出预览。
- `wait_execution`：长等待执行结果，最长 60 秒。
- `download_output`：以最多 1 MiB 的分块读取完整 NDJSON 输出，内容使用 Base64 编码。

`execute_command.files` 可携带文件，每项包含 `name`、`filename` 和 `content_base64`。命令仍只能使用 `{{file:name}}` 占位符，不能指定远端路径。单个文件最多 16 MiB，一次最多 8 个。

服务支持 MCP `2026-07-28` 的无状态请求形式，同时兼容仍会发送 `initialize` / `notifications/initialized` 的旧客户端。由于本地管理页面没有登录步骤，MCP 端点仍强制要求 Agent Token，并拒绝来自非 localhost 网页的 Origin。

## 推荐调用流程

1. 调用 `list_targets` 获取已启用的 `target_id`。响应不会包含本机私钥路径或 Host Key 等管理配置。
2. 调用 `execute_command`。一次 Agent 对话的第一次调用不传 `session_id`，保存响应中的 `session_id` 和 `execution_id`。
3. 调用 `wait_execution` 一次，通常会直接获得最终结果。若仍为 `pending` 或 `running`，可继续等待或稍后查询。
4. 请求中断后调用 `get_execution`，并同时提供之前保存的会话 ID 和执行 ID。

后续同一 Agent 对话的命令继续传入相同的 `session_id`。每次执行都会返回新的 `execution_id`。

## 文件输入

没有文件时使用 JSON 请求。有文件时改用 `multipart/form-data`：

- `request`：JSON 编码的执行请求。
- 其他字段：上传文件，字段名为占位符名称。
- 命令只能通过 `{{file:名称}}` 引用上传文件，不能提供远端路径。

例如文件字段名为 `migration_sql`，命令可写为：

```text
mysql app < {{file:migration_sql}}
```

不要把占位符放在引号中；SSH Bridge 会把整个占位符替换为经过 Shell 转义的内部路径。

## 结果处理

终态为 `succeeded`、`failed` 或 `timeout`。`output_preview` 最多保存 64 KiB；更大的结果使用 `downloadExecutionOutput` 获取完整 NDJSON。每行包含：

- `sequence`：输出块顺序。
- `offset_ms`：相对执行开始时间。
- `stream`：`stdout` 或 `stderr`。
- `data_base64`：原始输出字节。

不要仅根据输出文本判断成功；应同时检查 `status` 和 `exit_code`。
