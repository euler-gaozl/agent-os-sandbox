# Agent OS Sandbox

独立的隔离执行 HTTP 服务，只拥有 `public.sandbox_jobs`。每次调用通过 Runtime HTTP 检查授权、操作身份与 fence，不读取执行表。缺少隔离配置时拒绝执行，没有宿主机 Shell 回退。

## 执行边界

只有 Runtime 身份可以 `POST /execute`。作业容器使用只读根目录、禁网、内存/CPU/进程/输出限额和固定截止时间，守护程序降低工作负载权限。恢复根据持久化作业与容器身份核对，不能因未收到回复就认定从未执行。

隔离执行需要专用或受限的 **Linux Docker daemon** 和由摘要固定的 `SANDBOX_IMAGE`。使用 `deploy/sandbox-runtime.Dockerfile` 构建包含守护程序与 Node 24 的工作镜像。代理镜像含 Docker CLI、以非 root 运行，需显式配置最小必要 daemon 访问。Docker daemon 访问仍属于高权限基础设施能力，不应与不可信工作负载共享通用宿主 daemon。

Windows 当前可运行代理并在未配置时拒绝执行，**尚未提供本机隔离执行后端**。

## 构建、迁移与部署

需要 Go 1.27.2、PostgreSQL 18；代理本身不需要 Node。

```powershell
$env:GOWORK='off'; $env:GOPROXY='off'
./scripts/Build.ps1
go test -mod=vendor -count=1 ./...
go vet -mod=vendor ./...
```

Linux 也可 `go build -mod=vendor ./cmd/sandbox`。固定 SDK 与 `vendor` 支持单仓构建。根据 [配置示例](deploy/service.example.json) 提供本域数据库账号、独立服务令牌/对端哈希（含自身）和 `RUNTIME_URL`，以 `--config <文件>` 启动，显式配置多机地址和私网边界。

应用角色拥有 public USAGE、本域 DML，无 CREATE 且不是 schema 所有者。DDL 连接只通过 `MIGRATION_DATABASE_URL` 提供给 `bin/migrate --role <账号>`，事务只管理 `sandbox_jobs`。正常启动先验结构再清理后台作业，不执行 DDL。

按 [发布契约](deploy/release-contract.json) 独立升级/回滚并保留数据库、作业记录和配置；不要删除作业记录来强迫重试。

## 验证范围

`TEST_DATABASE_URL` 启用真实隔离 PostgreSQL 迁移/权限测试，未提供时明确跳过。普通边界测试的 Runtime 是 HTTP 夹具。

`sandbox/docker_integration_test.go` 保留真实 Docker 隔离、重放、代理 SIGKILL、独立守护截止和孤儿回执恢复场景。执行需 `TEST_DOCKER_SANDBOX=1`、专用 `DOCKER_HOST`、摘要固定的 `SANDBOX_IMAGE` 和隔离测试库。手动 `isolation.yml` 没有提交触发；拆仓后这套 Linux Docker 工作流等待最终统一验收，不能把旧集成仓通过记录算成新仓通过。

本地三个授权/受限登录检查及 Linux 测试程序交叉编译已通过；这不证明 Windows Docker 执行可用。Actions 日常关闭，[历史证据](docs/history/service-notes-20261014.en.md) 保留失败修复与范围说明。
