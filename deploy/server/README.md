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
