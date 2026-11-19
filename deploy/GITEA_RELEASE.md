# Gitea 手动发布与 GitHub 分发

切换验收后，私有 Gitea 是唯一主仓库。`main` 托管经过审查的稳定代码和发布控制，`dev` 用于日常开发。GitHub `production`、历史发布和镜像保留用于公开分发。普通推送不触发发布或同步。

## Gitea 发布

[手动发布工作流](../.gitea/workflows/sub2api-manual-release.yml)已随基础版本到达 `main` 和 `dev`；Gitea Actions 保持关闭。两条分支的旧 GitHub 工作流已移入[归档目录](../.github/workflow-archive/legacy-github-actions/)，不再从 `.github/workflows` 加载。新工作流使用独立文件名，默认 job token 仅能读代码。

启用前，按以下顺序验收：

1. 审核 `dev` 的控制代码后快进到受保护的 `main`。保证能够修改或触发工作流的权限仅授予可信维护者；工作流中的 `main` 条件不能替代仓库权限。
2. 验证所有可触发引用（包括历史 TAG）不会执行 `.github/workflows`。Gitea v28.2.0 默认在无 `.gitea/workflows` 的引用上回退查找该目录；原始导入时三条分支各有 114 个旧工作流 ID，19 个历史 TAG 另外带有 20 个。`main` 和 `dev` 的归档不影响 `production` 或历史 TAG。公开的工作流禁用接口只查找默认分支上的定义，不能直接禁用这些旧引用中的全部定义。若由实例管理员采用 `[actions] WORKFLOW_DIRS = .gitea/workflows` 阻断回退，须先评估并单独批准对其他仓库的影响；本项目不变更共享 Gitea 配置。未验证隔离时保持仓库 Actions 关闭。
3. 核对非生产 runner 和隔离测试引擎；仅在旧工作流隔离确认后启用仓库 Actions。先在 Gitea 的 `main` 手动运行工作流，填写来源分支、完整 SHA 和请求 ID，保持 `publish_release` 不勾选。试跑执行相同的构建和测试，不使用发布步骤的 `GITEA_RELEASE_TOKEN`，不写 Gitea TAG、Release、Registry 或正式检查点，临时产物随任务清理。首次正式发布另行授权。

发布必须使用隔离的非生产 Linux amd64 worker（标签 `sub2api-release-dedicated`），提供 checkout action 所需的 Node 20 运行时，并支持 Python 3、curl、Git、Docker/Buildx、Skopeo 和 arm64 构建执行。可以使用满足条件的非生产共享 runner，不要求新增物理机；不能使用当前连接生产 Docker 的共享 runner。管理员应检查：

- `/docker/gitea/mirrors/sub2api` 是可恢复的专用持久挂载。挂载内 `dedicated-worker-approved` 文件记录集成测试专用临时 Docker 主机的私网 IPv4。该主机不保存发布凭据、检查点或其他任务数据；只允许该发布环境访问 Docker 接口和测试容器动态端口，由管理员按任务初始化和回收。
- Actions secret `ALL_GITEA_TOKEN` 能写入该私有仓库的 TAG、Release 和镜像；实际仓库、附件、镜像权限和上传限制已核实。

条件未满足时不要调度。正式发布使用 `./release.sh <远端分支>`，由受限凭据机制提供 `GITEA_TOKEN`。客户端显式请求发布，以 Gitea 上的分支固定提交，只调度一次 `main` 的手动工作流，不部署或同步 GitHub。请求结果不明时按请求 ID 查询，不重复提交。

产物包括 Linux、macOS、Windows 的 amd64/arm64 六个安装包、`SHA256SUMS`、`build.json`，以及仅带本次版本标签的双架构 Gitea 镜像。版本为 `YYYYMMDD-<8 位 SHA>`，日期取提交者时间的 UTC 日期。只有附件、镜像和完成标记 `release-complete.json` 都核对通过才算完成。

第一次远端写入前会持久化原始产物及校验值。失败后，按版本比对 Gitea TAG、附件、镜像，以及专用目录中的 `records/<版本>.json` 和原始检查点。可恢复时重跑原 SHA，只补缺项；已有 TAG 或草稿但检查点缺失时停止。不要删除检查点、替换同名内容或重建已部分发布的版本。

本地可运行 `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_manual_release.py -v` 验证调度与恢复，不连接真实发布服务。当前已通过 Gitea v28.2.0 原生工作流静态解析及私有 API 只读认证；未验证真实 dispatch、附件上传、双架构 OCI 推送和远端完成标记。

## GitHub 手动同步

`./sysnc-github.sh` 是独立的写入命令。先确认公开范围获批、客户端兼容性已验证、GitHub Actions 已关闭。只同步允许的 `main` 及其可达的正式 Gitea 版本，不公开 `dev`，不更新 GitHub `production` 或 `latest`。现有 GitHub 安装、升级和 reauth 不能直接消费 Gitea 新版格式。
