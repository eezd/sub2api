# Forgejo 手动发布与 GitHub 分发

切换验收后，Forgejo 是唯一主仓库。`main` 托管经过审查的稳定代码和发布控制，`dev` 用于日常开发。GitHub `production`、历史发布和镜像保留用于公开分发。普通推送不触发发布、部署或同步。本文件沿用旧文件名以保持既有链接。

## 本机编译与 systemd

本机流程不经过 Forgejo。按[贡献指南](../CONTRIBUTING.md#按改动范围验证)先构建前端，再以 `go build -tags embed` 从本地源码编译候选二进制。仅在授权的目标环境使用 [`sub2api.service`](sub2api.service) 部署；该 unit 从 `/opt/sub2api/sub2api` 启动服务，配置和数据边界见[原生部署说明](README.md#binary-installation)。本仓库不恢复已撤下的常驻开发 unit；本地试用须用隔离的配置、PostgreSQL、Redis 与数据目录。构建、启动及数据库迁移不属于发版或同步流程。

## Forgejo 发布

目标实例 `https://git.134333.xyz` 已确认为 Forgejo `17.0.2+gitea-1.22.0`。本次仅适配本地代码，不代表已在实例启用、运行或验收新发布。

[手动发布工作流](../.forgejo/workflows/sub2api-manual-release.yml)仅接受 `source_branch`、`source_sha`、`request_id` 三个必填字符串，只能从可信 `main` 手动调度。控制代码固定到本次 `main` 的完整 SHA；来源分支只提供候选源码，不提供工作流或发布脚本。旧 Gitea 定义移入[非扫描归档](../.gitea/workflow-archive/sub2api-manual-release.yml)，旧 GitHub 工作流保留于[历史归档](../.github/workflow-archive/legacy-github-actions/)，不删除历史。

启用前，依次完成以下验收：

1. 审核控制代码后由维护者推进受保护的 `main`，限制能修改或触发工作流的主体。`main` 条件不能代替仓库权限。
2. 在仓库 Actions 关闭时核对工作流扫描路径和所有可触发引用，防止旧分支或历史 TAG 中的 `.github/workflows` / `.gitea/workflows` 被回退扫描。仅移动当前工作树不能证明历史引用安全；未验收前不启用 Actions，不擅改共享实例设置。
3. 只读盘点历史 Release、TAG、附件、Registry 版本及旧任务。GitHub 原有 `v*-custom.*` 发布保持原始格式、正文和原件，不伪造 `build.json` 或新完成标记，也不作为新格式的发布基线。旧 Generic Package 暂存、宿主目录和原件恢复点不是新流程的输入；旧方案残留需单独核实和授权处理。
4. 确认以下 Runner、TLS 连接、任务级隔离、凭据及上传条件；启用 Actions 及首次正式发布仍须授权。已有适用的平台并发证据可复用，不重复调度平台探针。

### 共享 Runner 与隔离构建

Job 使用账号可用的 `docker` 标签、`node:24-trixie` job 镜像和临时目录，不指定 Runner 名称、ID 或项目专属宿主挂载。已核实平台为 Runner v13.2.2、Forgejo 17.0.2 和专用 Docker 29.10.0 TLS DIND；Runner 不挂宿主 Docker socket，不修改或注册共享 Runner。

- 可信控制任务沿用 Runner 注入的 `DOCKER_HOST=tcp://docker:2376`、`DOCKER_TLS_VERIFY=1`、`DOCKER_CERT_PATH=/certs/client`。控制器要求显式远程 TLS 连接、证书路径和原生 Linux amd64 引擎；拒绝覆盖连接的 `DOCKER_CONTEXT`，不回退本地 socket、默认 context 或明文 `2375`。不再使用 `SUB2API_BUILD_DOCKER_IP` / `SUB2API_TEST_DOCKER_IP`。Buildx 从该连接创建本次具名 Docker context，再将独立 builder 绑定到它；构建结束或异常均精确清理 builder 和 context，不改父证书或共享 daemon。
- 控制器在本次专属网络中启动临时 privileged 测试服务，固定官方 `docker:29.10.0-dind@sha256:4bbc7223f81db790097c6dea42fb93e05b8fead4f0431fbe6ccaba6e8473201c`。它仅使用本次匿名数据卷和证书卷、自行生成独立 TLS 证书，不挂父 daemon 的 socket 或证书、不映射宿主端口、不关闭 TLS。privileged DIND 共享宿主内核的风险已获接受，不声称这是虚拟机隔离。
- 先通过 `docker exec <测试服务> docker --host unix:///var/run/docker.sock info` 等待测试 daemon 就绪，再将其 `/certs/client` 中的 `ca.pem`、`cert.pem`、`key.pem` 复制到 0700 临时 scratch 内，不输出或解析证书内容。仅对本次临时副本设置文件 0444、子目录 0755，使候选的非 root `node` 用户可读；父证书权限不变。候选收到的路径为 `/work/test-docker-certs`，以及 `DOCKER_HOST=tcp://docker:2376`、`DOCKER_TLS_VERIFY=1`、`DOCKER_CERT_PATH=/work/test-docker-certs`、`TESTCONTAINERS_HOST_OVERRIDE=docker`。此处 `docker` 网络别名只解析本次测试引擎，与 Runner 所在网络的同名父构建引擎分离，候选拿不到父证书。
- 固定候选 SHA 用 `git archive` 经管道和 `docker cp` 传入候选容器，无宿主 bind mount、`.git`、令牌或发布控制目录。候选仍使用 `--cap-drop=ALL`、`no-new-privileges`，只接入本次测试网络及 PostgreSQL/Redis 夹具。保留前后端检查、集成测试和 Linux amd64 冒烟；只有通过这些检查的六平台包及由相同 Linux 二进制生成的 OCI 才进入发布阶段。源码或证书复制失败立即退出；所有路径均按本次精确名称清理容器及匿名卷、网络、构建镜像和临时目录，不使用全局 prune、跨任务缓存或 Generic 暂存。
- 原生运行验收仅覆盖 Linux amd64；其余五个包是交叉编译，不能声称已原生运行。双架构 OCI 构建能力仍按实际 Buildx 执行结果记录，不把交叉编译当作 ARM64 运行验收。

`/docker/git/runner/README.md` 已记录同平台 AlphaPulse 的 TLS DIND、六包/双架构发布及并发恢复证据。Forgejo 17.0.2 的并发回执已核对：A13 占用、B14 等待，取消 A13 后 B14 约 3.42 秒继续成功，无 Runner 重启或额外唤醒任务；可复用这些适用的平台证据。本次 Sub2API 适配只做离线 mock 回归，尚未真实运行 Sub2API 新发布、候选测试 DIND 或网络隔离验收；不能将 AlphaPulse 的结果当作本项目发布成功。

保留独立 `python3 scripts/manual-release.py --verify-only` 供受控隔离环境构建验证，不另加工作流输入。它需要固定 `TRUSTED_SHA`、`RELEASE_REF=refs/heads/main`、`SOURCE_BRANCH`、完整 `SOURCE_SHA`、UUID `REQUEST_ID`、`RELEASE_RUN_ID=<run-id>-<attempt>` 及上述 Runner TLS Docker 环境；checkout 必须已有完整历史和来源分支的远端引用。此模式只用 `v2.22.0-custom.1` 作为临时检查包名，不读取或占用远端编号，不表示已分配正式版本；同样检查固定上游祖先。它不访问发布 API，不需发布令牌，不创建 TAG、Release 或镜像，但会拉取依赖、创建临时容器并执行候选源码，不能在生产环境试跑。

### 正式发布、幂等与失败重建

使用 `./release.sh <远端分支>`。客户端优先使用非空 `FORGEJO_TOKEN` 环境变量；未设置或为空时，仅按调用用户的 `HOME` 自动读取 `~/.config/forgejo/token`（root 对应 `/root/.config/forgejo/token`），按 UTF-8 纯文本去掉首尾空白。文件置于仓库外并限制为本人读写（如 `chmod 600`）；缺失、为空、不可读或编码错误时在任何 API 请求前失败，不输出凭据，不扫描其他配置。此本地回退不替代 Actions Secret；同步客户端对两个 Token 分别按下述规则取值。

客户端读取远端完整 SHA，只调度一次可信 `main`，不预先分配版本号，也不增加 version 输入。dispatch 请求体使用 `return_run_info: true`，响应全局 `id` 用于 API 轮询、`run_number` 用于网页链接。排队时其他任务可能先占用编号；本次成功后客户端分页查找该完整 SHA 唯一对应的已完成正式 custom Release，核对 TAG 的完整 SHA、build/完成标记的版本与完整 SHA、原始 build 字节哈希，输出实际最终版本。网络中断、超时或响应不明时按打印的请求 ID 核查，不重复调度。脚本不部署、不同步 GitHub。

本项目版本固定采用 `v<已集成 ranxi2001 正式版本>-custom.N`，不使用 git-init 通用的日期-SHA 名称，也不从本地 `VERSION` 文件推断基础。当前控制器明确固定官方 `v2.22.0`，其注解 TAG 对象为 `7089c11c106eb00726a6d4a36a3d04eb069f92c8`、剥离后的 commit 为 `4ba33f8713cbf6c6406e0f2639ffcc2ef1e31c87`。固定来源 SHA 必须包含该 commit 祖先；已核实本次集成还包含 tag 后 production `701612f6e9f7b697eb36397b9fb49bf7324e56a1`（54 个额外提交），不能把标签之后的集成说成恰好官方 tag。下次明确同步上游时，核实正式 tag 和集成祖先后再更新 `scripts/manual-release.py` 的 pin；发布命令本身不获取或合并新上游。

串行控制器读取远端 TAG 和 Release，在同一基础版本内按 custom 后缀**数字**最大值加一（无记录时为 1），其他基础不参与。失败 TAG 保留并消耗编号，不复用失败 N、不强推。历史导入的 custom TAG/Release 同样占号，但没有新 `build.json`/完成标记，不会因此被认领为新流程已验收版本或发布基线。

工作流仓库 secret 名为合法的 `RELEASE_TOKEN`，仅发布步骤映射成 `FORGEJO_RELEASE_TOKEN`。凭据需有目标已确认公开仓库的代码/Release/TAG 写入、用户身份读取和该命名空间包读写权限，并核实 CDN、附件及 Registry 限制；不硬编码全局凭据名，不需要暂存组织或 Generic Package 权限。候选构建不接收这些凭据。正式发布仍显式核对仓库为已确认的公开 `eezd/sub2api` 且当前令牌有写权限；仓库公开不证明 Registry 包可匿名拉取，后者须独立验收。

发布六个 Linux/macOS/Windows × amd64/arm64 安装包、`SHA256SUMS`、`build.json` 和 Linux amd64/arm64 OCI 版本镜像，仅写版本 TAG，不写 `latest`。完成标记 `release-complete.json` 最后上传，绑定原始 `build.json` 的 SHA256、版本与源码 SHA。正式发布必须非草稿、非预发布，附件集合、全部包哈希、注解 TAG 的完整 SHA、镜像索引及两平台摘要均一致。新格式基线按同来源分支的已完成祖先 BFS 最近距离选择，再按完成时间降序和 TAG 排序；没有同分支记录才使用 `init`，已有记录却均不可达则停止。

- 仓库串行声明为固定 concurrency group、`cancel-in-progress: false`；上述同平台实际并发恢复证据可复用。平台或并发配置变化后须重新核实，不能只以 YAML 或本地 mock 声称互斥已验证。
- 同一完整 SHA 已有新流程完成版时，验证后复用原版本，即使来源分支改变也不新增 N、不改正文或分支，不写入、不覆盖、不重建；不能只比 SHA 前缀。损坏的完成标记不按 unfinished 降级；完成证据冲突、归属不明或冲突状态立即停止。
- 不保存 Generic/staging 包、宿主持久目录、原件分片或 checkpoint。同一完整 SHA 的未完成版仍有 Release/镜像残留时，必须先确认原任务终止且未成功，验证注解 TAG 中的版本、源码 SHA、来源分支、请求 ID、可信控制 SHA 与原 Actions run 一致，并核对 Release/镜像的创建主体。清理前分页核对该仓库该工作流所有 run；除本次外任何非终止态、缺少本次记录、分页重复或身份不明都停止，不删除。
- 仅在上述证据充分且没有完成标记时，删除该版本精确 Release（含附件）和该容器镜像的确切版本，回读确认清理结果；同 SHA 注解 TAG 保留，不碰其他 TAG 或 `latest`。DELETE 响应不明即停，不自动重放；身份无法可靠确认时由维护者另行核实和授权精确清理。
- 清理后保留旧 TAG，使用**新的 N** 在新临时目录从原完整 SHA 全量构建，不恢复或补传上一轮原件。仅剩失败 TAG、已无 Release/镜像残留时只占号，不重复删除；其他源码的未完成版本不清理、不阻塞修复后的新 SHA。陌生身份、仍活跃的原任务或完成证据冲突不能盲删。原件丢失不是成功证据。原 SHA 已不在来源分支可达历史时停止。若分支已推进，`release.sh <分支>` 会读取新顶端；恢复旧 SHA 应手动调度相同工作流，仍只填三个字符串并使用新请求 ID。

本地离线回归：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 .github/release-tools/test_release_matrix.py
bash -n release.sh sysnc-github.sh scripts/build-release.sh scripts/package-release.sh
git diff --check
```

这些测试覆盖模拟 API、临时 Git 历史、同基础数字编号与失败占号、完整 SHA 复用/冲突、排队后分页解析实际版本、全量失败重建、零写入保护，TLS 环境保留、任务 daemon/证书边界、具名 context/builder 与失败清理，以及同步双 Token 的独立环境优先/文件回退、读取失败前零 API/锁/同步调用和固定 User-Agent 透传、Registry 默认文件/显式无效不回退、临时 Git 认证白名单继承（含空 helper、多值顺序）与新 custom 产物复制；不启动 Forgejo、Docker 或真实 Actions，不证明 Sub2API 实例权限、TLS、网络隔离或真实构建已通过。

## GitHub 手动同步

`./sysnc-github.sh` 是另行授权的独立写入命令，不会被发布脚本调用。`FORGEJO_TOKEN` 与 `GITHUB_TOKEN` 分别优先使用各自非空环境变量；未设置或为空时，仅读取调用用户 `HOME` 下各自的 `~/.config/forgejo/token`、`~/.config/github/token`，按 UTF-8 纯文本去掉首尾空白。任一文件缺失、为空、不可读或编码错误，在任何 API 请求、锁或同步前失败，不输出内容、不扫描其他配置或旧凭据。文件应在仓库外且仅本人可读写（如 0600）。API 凭据不代替 Git 认证，不进入 Git 子进程。临时裸仓库只继承本项目本地 `credential.*` 的 helper、username、useHttpPath（包括空 helper 重置和多值顺序），不复制 remote、hooks 或全部 `.git/config`；没有本地 Git 配置时仍可使用已有全局/SSH 认证。认证核对须包含临时快照的 Git 传输预检，不能只验证原项目。

镜像认证独立于两个 API Token。非空 `REGISTRY_AUTH_FILE` 优先；未设置或为空时使用调用用户的 `~/.config/containers/auth.json`，登录后可直接运行同步命令。所选路径必须是普通可读文件；显式无效路径不回退。缺失或不可读时只报告所选路径及 `skopeo login --authfile <所选路径> <源或目标 Registry>`/指定 `REGISTRY_AUTH_FILE` 的指引，不打印内容、不自动登录，也不从 Token 文件生成 Registry 认证。Forgejo API 请求透传固定 `User-Agent: forgejo-init-manual-sync`，这属于接口契约适配，不表示已确认存在无 UA 的 403 故障。先确认公开范围获批、GitHub Actions 已关闭、客户端兼容性已验证。仅同步允许的 `main` 及其可达的已验收正式 Forgejo TAG/Release/版本镜像，不公开 `dev`，不更新 GitHub `production`、`latest` 或 Forgejo 专用完成标记。已有目标内容完全一致时不重复写入；冲突时停止。

新流程 `v<已集成上游正式版本>-custom.N` 只有满足 TAG、完整 SHA、六包哈希和完成记录契约才可复制。导入的旧 `v*-custom.*` 虽然名称相同，但没有新六包证据和完成标记，必须保留原件，不能伪造新验收记录、重建或添加兼容认领层。同步器明确列为缺少证据的历史缺口并返回非零；本次历史导入的独立原件对等核对不改变此规则。现有 GitHub 安装、升级和 reauth 不能直接消费 Forgejo 新版格式，发布成功不代表生产已部署。
