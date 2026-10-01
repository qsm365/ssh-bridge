# Server 模式 Docker 开发部署

在仓库根目录执行：

```bash
cp deploy/server/.env.example deploy/server/.env
mkdir -p deploy/server/ssh-keys
# 编辑 deploy/server/.env，填写独立、随机的数据库密码和管理员密码。
docker compose -f deploy/server/compose.yaml up --build -d
docker compose -f deploy/server/compose.yaml ps
```

默认只在宿主机 `127.0.0.1:7408` 开放管理页面。开发验收使用 HTTP 时，`.env` 中可设 `SSH_BRIDGE_COOKIE_SECURE=false`；生产环境应改为 `true`，并由反向代理提供 HTTPS、转发原始 `Host`，同时处理公网接入保护和限流。不要把登录入口直接暴露为公网明文 HTTP。

Compose 会把 `SSH_BRIDGE_POSTGRES_PASSWORD` 拼入数据库 URL，请使用字母、数字、`-`、`_` 等 URL 安全字符组成密码；若必须使用其他字符，需要自行调整 Compose 中数据库 URL 的编码方式。受限网络可以在 `.env` 中设置 `SSH_BRIDGE_NPM_REGISTRY`、`SSH_BRIDGE_GOPROXY` 作为构建镜像源，默认仍使用官方源。

容器以固定 UID/GID `10001` 运行。`bridge_data` 卷挂载为 `/data`，保存完整执行输出、上传归档及用于加密 SSH 密码的 `target-password.key`；`postgres_data` 卷保存元数据。首次启动自动执行数据库迁移。若改用宿主机目录挂载 `/data`，请预先授予 UID 10001 写入权限；目录不可写时服务启动会报错。

私钥从 `SSH_BRIDGE_SSH_KEYS_DIR` 指向的宿主机目录只读挂载到容器 `/run/ssh-keys`。在主机表单中填写容器内的绝对路径，例如 `/run/ssh-keys/id_ed25519`。容器 UID 10001 必须能遍历目录并读取对应私钥；建议目录归属 UID 10001 且模式 `0500`、私钥归属 UID 10001 且模式 `0400`。权限错误会在 SSH 测试连接时显示读取私钥失败。不要把真实私钥放进镜像或 Git。

两个持久化卷应配套备份和恢复，尤其不要丢失 `/data/target-password.key`，否则数据库里已加密的 SSH 密码无法解密。重建容器不会删除具名卷。`.env` 和 `ssh-keys/` 已加入 Git 忽略规则，仍需避免把凭据写入命令行、构建参数、镜像层或共享日志。

停止开发服务（保留数据）：

```bash
docker compose -f deploy/server/compose.yaml down
```

## 生产接入与升级

Compose 是单实例开发验收配置，默认仅绑定宿主机回环地址。生产环境应由 HTTPS 反向代理接入，将原始 `Host` 头转发给 Bridge，并设置 `SSH_BRIDGE_COOKIE_SECURE=true`；只允许受信网络访问数据库和 Bridge 的内部端口。`/healthz` 表示进程可响应，`/readyz` 会检查数据库连接。若采用 MySQL，使用 `SSH_BRIDGE_DATABASE_URL` 的 MySQL DSN 启动独立 Server 实例；示例 Compose 仅提供 PostgreSQL。

每次升级先安排短暂停机，停止 Bridge 写入，再对数据库做一致性备份，并完整备份 `/data`（尤其是 `target-password.key`、执行输出和上传归档）。数据库与 `/data` 是同一份审计数据的两部分，恢复时应使用同一备份时间点；不要只备份数据库或只保存容器镜像。SSH 私钥也应通过独立的安全渠道备份，勿放进数据卷快照或 Git。备份中包含敏感命令、输出和连接信息，应限制访问并验证可恢复性。

PostgreSQL Compose 环境可按以下顺序操作；请将备份文件写入受控目录，不要把密码或备份提交到仓库：

```bash
# 在仓库根目录；先停止 Bridge，保留 PostgreSQL 和具名卷。
docker compose -f deploy/server/compose.yaml stop bridge
# 将输出重定向到已创建、仅管理员可读的备份目录。
docker compose -f deploy/server/compose.yaml exec -T postgres \
  pg_dump -U ssh_bridge -Fc ssh_bridge > /secure/backup/ssh-bridge.pgdump
docker compose -f deploy/server/compose.yaml cp \
  bridge:/data/. /secure/backup/data/
# 不要执行 docker compose down -v。
# 更新代码或镜像后：
docker compose -f deploy/server/compose.yaml up --build -d
docker compose -f deploy/server/compose.yaml ps
```

服务启动时会按编号执行未应用的数据库迁移，并记录到 `schema_migrations`；不提供自动降级迁移。升级前检查目标版本和备份，升级后确认 `/readyz`、管理员登录、授权目标列表和一条历史执行输出；异常时先停服务，再恢复匹配的数据库与 `/data` 备份，然后运行原版本。MySQL 部署同样遵循此顺序，数据库备份工具改用对应的 MySQL 客户端。不要在运行中的服务上单独恢复数据库或删除卷。
