# Forgejo 手动发布与 GitHub 分发

切换验收后，私有 Forgejo 是唯一主仓库。`main` 托管经过审查的稳定代码和发布控制，`dev` 用于日常开发。GitHub `production`、历史发布和镜像保留用于公开分发。普通推送不触发发布、部署或同步。本文件沿用旧文件名以保持既有链接。

## 本机编译与 systemd

本机流程不经过 Forgejo。按[贡献指南](../CONTRIBUTING.md#按改动范围验证)先构建前端，再以 `go build -tags embed` 从本地源码编译候选二进制。仅在授权的目标环境使用 [`sub2api.service`](sub2api.service) 部署；该 unit 从 `/opt/sub2api/sub2api` 启动服务，配置和数据边界见[原生部署说明](README.md#binary-installation)。本仓库不恢复已撤下的常驻开发 unit；本地试用须用隔离的配置、PostgreSQL、Redis 与数据目录。构建、启动及数据库迁移不属于发版或同步流程。

## Forgejo 发布

目标实例 `https://git.134333.xyz` 已确认为 Forgejo `17.0.2+gitea-1.22.0`。本次仅适配本地代码，不代表已在实例启用、运行或验收新发布。

[手动发布工作流](../.forgejo/workflows/sub2api-manual-release.yml)仅接受 `source_branch`、`source_sha`、`request_id` 三个必填字符串，只能从可信 `main` 手动调度。控制代码固定到本次 `main` 的完整 SHA；来源分支只提供候选源码，不提供工作流或发布脚本。旧 Gitea 定义移入[非扫描归档](../.gitea/workflow-archive/sub2api-manual-release.yml)，旧 GitHub 工作流保留于[历史归档](../.github/workflow-archive/legacy-github-actions/)，不删除历史。

启用前，依次完成以下验收：

1. 审核控制代码后由维护者推进受保护的 `main`，限制能修改或触发工作流的主体。`main` 条件不能代替仓库权限。
2. 在仓库 Actions 关闭时核对工作流扫描路径和所有可触发引用，防止旧分支或历史 TAG 中的 `.github/workflows` / `.gitea/workflows` 被回退扫描。仅移动当前工作树不能证明历史引用安全；未验收前不启用 Actions，不擅改共享实例设置。
3. 只读盘点历史 Release、TAG、附件、Registry 版本及旧任务。GitHub 原有 `v*-custom.*` 发布保持原始格式、正文和原件，不伪造 `build.json` 或新完成标记，也不作为新格式的发布基线。旧 Generic Package 暂存、宿主目录和原件恢复点不是新流程的输入；旧方案残留需单独核实和授权处理。
4. 确认以下 Runner、双引擎、凭据及上传条件；再另行授权启用 Actions、真实排队验收及首次正式发布。

### 共享 Runner 与隔离构建

Job 使用账号可用的 `docker` 标签、`node:24-trixie` job 镜像和临时目录，不指定 Runner 名称、ID 或项目专属宿主挂载。已知 Runner 为 v13.2.2，描述为 dedicated TLS Docker-in-Docker；这并不证明下述项目所需的两个独立引擎已配置或可达。

- `SUB2API_BUILD_DOCKER_IP` 指向经批准的非生产 Linux amd64 构建引擎，Buildx 须能执行 `linux/arm64` 构建步骤。`SUB2API_TEST_DOCKER_IP` 指向另一个可回收的 Testcontainers 引擎。当前项目契约仍是两个不同的 RFC1918 IPv4、`tcp://<IP>:2375`；不回退共享 Runner 的默认 context 或宿主 socket。实际地址、访问控制和可达性尚待配置及验收。
- TLS DIND 不能直接填入上述无 TLS 接口。若只能提供 TLS 引擎，须先明确证书、端口及 Buildx context 契约并另行适配，不能猜测、关闭 TLS 或连接生产 Docker。
- 构建 API 仅允许可信控制任务接入。候选容器不得访问构建 API、宿主 socket、生产服务或发布凭据；仅能访问隔离测试 Docker API 和测试容器端口。私网地址不是隔离证明，不得将 `2375` 暴露到公共或生产网络，不得在测试引擎存放其他任务的数据。
- 固定候选 SHA 用 `git archive` 经管道传入候选容器，不挂载 runner 文件系统，也不把 `.git`、令牌或发布控制目录交给候选。导入失败即退出并清理本次临时容器；不重试付费模型请求。只有经过前后端检查、隔离集成测试、Linux amd64 冒烟的六平台包及由相同 Linux 二进制生成的 OCI 才能进入发布阶段。

保留独立 `python3 scripts/manual-release.py --verify-only` 供受控隔离环境构建验证，不另加工作流输入。它需要固定 `TRUSTED_SHA`、`RELEASE_REF=refs/heads/main`、`SOURCE_BRANCH`、完整 `SOURCE_SHA`、UUID `REQUEST_ID`、`RELEASE_RUN_ID=<run-id>-<attempt>` 及两个引擎变量；checkout 必须已有完整历史和来源分支的远端引用。此模式不访问发布 API，不需发布令牌，不创建 TAG、Release 或镜像，但会拉取依赖、创建临时容器并执行候选源码，不能在生产环境试跑。

### 正式发布、幂等与失败重建

使用 `./release.sh <远端分支>`。客户端优先使用非空 `FORGEJO_TOKEN` 环境变量；未设置或为空时，仅按调用用户的 `HOME` 自动读取 `~/.config/forgejo/token`（root 对应 `/root/.config/forgejo/token`），按 UTF-8 纯文本去掉首尾空白。文件置于仓库外并限制为本人读写（如 `chmod 600`）；缺失、为空、不可读或编码错误时在任何 API 请求前失败，不输出凭据，不扫描其他配置。此回退不适用于同步脚本，也不替代 Actions Secret。

客户端读取远端完整 SHA，按提交者时间的 UTC 日期生成 `YYYYMMDD-xxxxxxxx` 版本，只调度一次可信 `main`。dispatch 请求体使用 `return_run_info: true`，响应 `id` 用于 API 轮询、`run_number` 用于网页链接。网络中断、超时或响应不明时按打印的请求 ID 核查，不重复调度。脚本不部署、不同步 GitHub。

工作流仓库 secret 名为合法的 `RELEASE_TOKEN`，仅发布步骤映射成 `FORGEJO_RELEASE_TOKEN`。凭据需有目标私有仓库代码/Release/TAG 写入、用户身份读取和该命名空间包读写权限，并核实 CDN、附件及 Registry 限制；不硬编码全局凭据名，不需要暂存组织或 Generic Package 权限。候选构建不接收这些凭据。

发布六个 Linux/macOS/Windows × amd64/arm64 安装包、`SHA256SUMS`、`build.json` 和 Linux amd64/arm64 OCI 版本镜像，仅写版本 TAG，不写 `latest`。完成标记 `release-complete.json` 最后上传，绑定原始 `build.json` 的 SHA256、版本与源码 SHA。正式发布必须非草稿、非预发布，附件集合、全部包哈希、注解 TAG 的完整 SHA、镜像索引及两平台摘要均一致。新格式基线按同来源分支的已完成祖先 BFS 最近距离选择，再按完成时间降序和 TAG 排序；没有同分支记录才使用 `init`，已有记录却均不可达则停止。

- 仓库串行声明为固定 concurrency group、`cancel-in-progress: false`。真实 Forgejo 排队语义尚未验收，不能当作已生效的互斥保证。启用前须实际验收 A 运行时 B 等待、取消 A 后 B 无需重启即可继续；本地 mock 测试不能替代它。
- 完成版重跑只校验，不写入、不覆盖、不重建。损坏的完成标记、SHA 碰撞、归属不明或冲突状态立即停止。
- 不保存 Generic/staging 包、宿主持久目录、原件分片或 checkpoint。未完成版必须先确认原任务终止且未成功，验证注解 TAG 中的版本、源码 SHA、来源分支、请求 ID、可信控制 SHA 与原 Actions run 一致，并核对 Release/镜像的创建主体。清理前分页核对该仓库该工作流所有 run；除本次外任何非终止态、缺少本次记录、分页重复或身份不明都停止，不删除。
- 仅在上述证据充分且没有完成标记时，删除该版本精确 Release（含附件）和该容器镜像的确切版本，回读确认清理结果；同 SHA 注解 TAG 保留，不碰其他 TAG 或 `latest`。DELETE 响应不明即停，不自动重放；身份无法可靠确认时由维护者另行核实和授权精确清理。
- 清理后在新临时目录从原 SHA 全量构建，不恢复或补传上一轮原件。原件丢失不是成功证据。原 SHA 已不在来源分支可达历史时停止。若分支已推进，`release.sh <分支>` 会读取新顶端；恢复旧 SHA 应手动调度相同工作流，仍只填三个字符串并使用新请求 ID。

本地离线回归：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_manual_release.py -v
PYTHONDONTWRITEBYTECODE=1 python3 .github/release-tools/test_release_matrix.py
bash -n release.sh sysnc-github.sh
git diff --check
```

这些测试覆盖模拟 API、临时 Git 历史、全量失败重建及零写入保护，不启动 Forgejo、Docker 或真实 Actions，不证明实例权限、并发、TLS、网络隔离或真实构建已通过。

## GitHub 手动同步

`./sysnc-github.sh` 是另行授权的独立写入命令，需 `FORGEJO_TOKEN` 和 `GITHUB_TOKEN`，不会被发布脚本调用。先确认公开范围获批、GitHub Actions 已关闭、客户端兼容性已验证。仅同步允许的 `main` 及其可达的已验收正式 Forgejo TAG/Release/版本镜像，不公开 `dev`，不更新 GitHub `production`、`latest` 或 Forgejo 专用完成标记。已有目标内容完全一致时不重复写入；冲突时停止。

导入的旧 `v*-custom.*` 版本没有新六包证据和完成标记，必须保留原件，不能伪造新验收记录。同步器按严格新格式边界列为缺口并返回非零；本次历史导入的独立原件对等核对不改变此规则。现有 GitHub 安装、升级和 reauth 不能直接消费 Forgejo 新版格式，发布成功不代表生产已部署。
