# SSH Bridge

English · [中文](README.md)

SSH Bridge is a lightweight SSH execution gateway for AI Agents, designed primarily for after-the-fact auditing and troubleshooting. Local mode runs as a single executable with SQLite; Server mode uses PostgreSQL or MySQL. Full command output is archived in local files in both modes. The web UI supports English and Chinese, with Chinese as the default.

## Features

- Local mode has no administrator sign-in. Server mode uses a fixed `admin` account and in-memory login sessions.
- Local mode generates one Agent token on first start. Server mode supports multiple named Agent tokens, per-host permissions, and audit attribution.
- Manage SSH targets using a private key or username and password. Host key verification is optional.
- Test SSH login before or after saving a target, without running a remote command.
- Soft-delete targets without losing their historical execution records.
- Authenticate Agents with bearer tokens through the HTTP API or built-in stateless MCP Streamable HTTP endpoint.
- Generate a session ID on the first execution and an execution ID for each command.
- Run SSH commands asynchronously; wait for or query results by session and execution ID after an interruption.
- Upload input files referenced only through `{{file:alias}}` placeholders. Bridge creates and cleans up remote temporary paths.
- View sessions, commands, status, output previews, and archived stdout/stderr in the web UI.
- Store metadata in SQLite, PostgreSQL, or MySQL and full output in JSONL files. A single execution is limited to 50 MiB of output.
- Automatically mark unfinished executions as failed after a service restart.

The first version intentionally does not include approval workflows, record-retention cleanup, SSO, or S3 storage. The calling Agent remains responsible for asking the user whether a command should run.

## Build and run

```bash
cd web
npm install
npm run build
cd ..
go build -o ssh-bridge ./cmd/ssh-bridge

./ssh-bridge --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

Open `http://127.0.0.1:7408`. Local mode signs in automatically and may listen only on a loopback address. On first start, it saves an Agent token with `0600` permissions in `ssh-bridge-data/agent-token`. The full token is displayed once on the Agent Access page; regeneration immediately invalidates the old token. `SSH_BRIDGE_AGENT_TOKEN` can provide the initial value before the data directory is initialized.

### Server mode

Server mode requires `SSH_BRIDGE_ADMIN_PASSWORD` and `SSH_BRIDGE_DATABASE_URL`. The database type is inferred from the URL scheme.

PostgreSQL:

```bash
export SSH_BRIDGE_DATABASE_URL='postgres://ssh_bridge:password@127.0.0.1:5432/ssh_bridge?sslmode=disable'
export SSH_BRIDGE_ADMIN_PASSWORD='replace-with-a-strong-password'
export SSH_BRIDGE_COOKIE_SECURE=false # Only for local HTTP testing
./ssh-bridge --mode server --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

MySQL:

```bash
export SSH_BRIDGE_DATABASE_URL='mysql://ssh_bridge:password@127.0.0.1:3306/ssh_bridge?charset=utf8mb4'
export SSH_BRIDGE_ADMIN_PASSWORD='replace-with-a-strong-password'
export SSH_BRIDGE_COOKIE_SECURE=false # Only for local HTTP testing
./ssh-bridge --mode server --listen 127.0.0.1:7408 --data-dir ./ssh-bridge-data
```

Migrations run automatically at startup. `GET /healthz` checks whether the process responds; `GET /readyz` also checks the database. Outputs and uploads still live under `--data-dir`, not in the external database.

The administrator signs in as `admin`. Login sessions are kept only in process memory, expire after 12 hours, and become invalid after a restart. Use an HTTPS reverse proxy in production, preserve the original `Host` header, leave secure cookies enabled, and protect public access and rate limits at the proxy. Do not expose the login page through plain HTTP.

Start with this workflow:

1. Sign in and add a target host; test its SSH login.
2. Create a named credential on the Agent Access page and assign target permissions. Save the one-time token.
3. Configure an Agent to use the MCP endpoint and call `list_targets` before a simple read-only command.

The administrator credential API provides `GET/POST /api/v1/agent-credentials`, `PUT /api/v1/agent-credentials/{id}`, and `POST /api/v1/agent-credentials/{id}/regenerate`. Create or update with `name`, `target_ids`, and `enabled`. Only a SHA-256 token hash and identifying prefix are stored. Revocation or rotation takes effect immediately. Removing host permission blocks new executions but preserves access to that credential's historical results.

For container deployment, see the [Server Docker guide](deploy/server/README.md). The root `Dockerfile` builds the frontend and Go binary; the sample Compose setup includes PostgreSQL, persistent volumes, and a read-only SSH key mount.

## Agent integration

The running service exposes OpenAPI 3.1 at `/api/openapi.json`. See the [Agent integration guide](docs/agent-integration.md) for the full protocol. Agents can list permitted hosts through `GET /api/v1/agent/targets` without receiving private key paths or other management details. MCP clients connect to `/mcp` with the same bearer token; MCP uses the same execution and audit implementation as the HTTP API.

For the first command, omit `session_id`:

```http
POST /api/v1/executions
Authorization: Bearer <agent-token>
Content-Type: application/json

{
  "target_id": "target_...",
  "session_title": "Investigate the order service",
  "title": "Check service status",
  "command": "systemctl status order-api --no-pager"
}
```

The server immediately returns HTTP 202 with `session_id`, `execution_id`, and `status: "pending"`. Reuse the session ID for later commands in the same Agent conversation. Usually, one long wait returns the final state and output preview:

```http
GET /api/v1/sessions/{session_id}/executions/{execution_id}/wait?timeout=55
Authorization: Bearer <agent-token>
```

After a network interruption, query the same URL without `/wait` before retrying so a command is not run twice. Status values are `pending`, `running`, `succeeded`, `failed`, and `timeout`. Download full ordered stdout/stderr as Base64-encoded JSONL events when the preview is insufficient:

```http
GET /api/v1/sessions/{session_id}/executions/{execution_id}/output
Authorization: Bearer <agent-token>
```

### Input files

Send SQL, scripts, or other inputs as `multipart/form-data`. The `request` part contains execution JSON; each file field name matches a placeholder:

```bash
curl -X POST http://127.0.0.1:7408/api/v1/executions \
  -H 'Authorization: Bearer <agent-token>' \
  -F 'request={"target_id":"target_...","title":"Apply SQL","command":"mysql app < {{file:migration_sql}}"};type=application/json' \
  -F 'migration_sql=@./migration.sql'
```

The caller cannot choose a remote path. Bridge substitutes an internally generated temporary path, removes the remote file after execution, and keeps the original input under the local data directory for auditing. Up to eight files are allowed per execution, each at most 16 MiB, and every file must be referenced in the command.

## Data, upgrades, and backups

The default `./ssh-bridge-data` directory contains `ssh-bridge.db` in Local mode, `target-password.key` for encrypted SSH passwords, `outputs/YYYY/MM/DD/{execution_id}/output.jsonl`, and `artifacts/YYYY/MM/DD/{execution_id}/`. Private keys are read from administrator-configured paths and are not stored in the database or output archive. An empty host key setting skips verification; configure one when available.

Stop Local mode before backing up the entire data directory; copying only the SQLite database may omit its WAL data. Treat backups as sensitive because they include connection settings, tokens, commands, outputs, and uploaded files. Keep `target-password.key` with the database, or stored SSH passwords cannot be decrypted. After replacing the executable, restart with the same `--data-dir`, check `/readyz`, and confirm historical records.

For Server mode, stop writes and back up both the external database and `/data` at the same point in time. The [Server Docker guide](deploy/server/README.md) covers upgrades, migrations, and backups.

## License

SSH Bridge is released under the [Apache License 2.0](LICENSE). Third-party dependencies remain subject to their respective licenses.
