# Forgejo 手动发布与 GitHub 分发

Forgejo 是唯一主仓库。受保护的 `main` 承载稳定源码与可信发布控制，`dev` 用于开发；GitHub 保留历史公开分发。普通 push 不触发构建、发布、部署或同步。本文件沿用旧文件名以保持既有链接。

## 入口和安装状态

| Forgejo Actions 页面 | 输入 | 职责 |
| --- | --- | --- |
| [Manual release](../.forgejo/workflows/release.yml) | `source_branch` 默认 `main`；`source_sha` 可选完整 40 位 SHA | 从固定来源执行检查、构建六平台包并发布 Forgejo Release/双架构镜像 |
| [Manual GitHub sync](../.forgejo/workflows/sync-github.yml) | 无自定义输入 | 将获准的 `main`、已验收正式 TAG、原件和镜像补齐到 GitHub/GHCR，不重新构建 |

两个入口都只能从可信 `main` 手动启动，控制代码 checkout 到本次完整 `forgejo.sha`。来源分支只提供候选源码，不能替换工作流或控制脚本；两个入口共用静态 `sub2api-delivery` concurrency group，`cancel-in-progress: false`。不提供根目录发布/同步 CLI，也不在工作流内再次 dispatch。没有新增 dev→main 自动提升入口。

网页双入口及历史承接规则已交付可信 main。本轮将固定来源升级为 `v2.24.1`；上一次实际验收的版本仍为 `v2.22.0-custom.3`。先前基于 v2.23.1 的 run56 已由操作者取消删除，没有生成 TAG/Release；其专属临时容器、匿名卷、网络及候选镜像已精确清理，未修改共享引擎。源码交付与实际发布验收分别记录于本地 `CONTEXT.md`、`MEMORY.md`。仅修改本地文件不会改变远端控制；发布前须回读获授权可信 main 的完整 SHA，不把源码交付或离线测试当作真实验收。

启用或更新前，核对仓库保护、手动触发主体、工作流扫描路径和所有可触发引用，避免历史 `.github/workflows` / `.gitea/workflows` 回退扫描。旧 Gitea 定义和 GitHub 工作流保留在非扫描归档；不恢复 push/PR/TAG 自动触发，不启用 GitHub Actions。切换、首次正式发布及真实同步各需相应授权，不修改共享实例或 Runner。

## 固定来源和版本编号

`scripts/resolve-release.py` 在任务开始执行时读取并冻结远端来源分支 HEAD，生成规范化输入、请求 UUID、候选/控制完整 SHA 和 `CI_RUN_ID`/`CI_RUN_ATTEMPT` 的 `RELEASE_CONTEXT`；发布控制器随后验证可选 SHA 从冻结的分支 HEAD 可达。网页不接收版本号、请求 ID 或控制 SHA；排队前看到的分支 HEAD 和预估 N 都不是最终结果。应填写已检查、已交付的完整 SHA，任务中再核对冻结结果。

本项目始终使用 `v<已集成 ranxi2001 上游正式版本>-custom.N`，不使用通用 Skill 的日期-SHA，不从本地 `VERSION` 或自有上一版猜测上游基础。当前本地 pin：

- 正式 TAG：`v2.24.1`；注解 TAG 对象：`396c53dea4c0fc31e4f14a207c84ee864c9e7428`。
- 剥离后的正式 commit：`0755d5ef037f64d203828d0edcc513764b69e1d2`；候选必须包含该祖先。
- 实际集成 production：`8dedf05db37c870265e04327defb9e96e7787603`，TAG 后额外 2 个提交（Astra 重发票据用例固定时钟及其合并），不等于恰好官方 TAG。

串行控制器按同基础版本的远端 TAG/Release **数字**最大 N 加一，无记录时从 1 开始；失败 TAG 和历史 TAG 保留并占号。解析器不预留编号，发布也不自动更新上游。未来明确同步新基础时，在同一轮更新 `scripts/manual-release.py` 的 pin、测试及文档，核实最终候选祖先关系。

新 `build.json` 使用 schema 5，绑定来源分支 HEAD、规范化输入、完整 source/control SHA、请求和 run/attempt；附加 TAG、完成标记与原始 build 字节哈希形成交付证明。已完成旧项目记录可能没有 schema 字段，按其真实 TAG/build/marker 证明严格读取，不改写成新 schema。缺少新六包/完成证据的历史导入不冒认、不伪造 marker、不重建原件。

## 显式认证及临时目录

| 入口 | 仓库 Secret | 仅该控制步骤使用的环境变量 |
| --- | --- | --- |
| Manual release | `RELEASE_TOKEN` | `FORGEJO_RELEASE_TOKEN` |
| Manual GitHub sync | `RELEASE_TOKEN`、`MIRROR_TOKEN` | `FORGEJO_TOKEN`、`GITHUB_TOKEN` |

Token 须分别具有确切目标仓库/API/包权限；Secret 名不能使用保留前缀。仅校验名称不证明权限。缺少显式 Secret 应在任何远端写入前失败。不要把发布 Token 配置为候选构建环境。

控制器在 0700 临时目录内生成 0600 的精确 HTTPS 仓库 Git 认证和 Registry 配置，Registry 登录经 stdin；Git 子进程不继承 API Token。隔离 HOME、Git 全局配置和 XDG，不读取宿主 `~/.config`、Git helper、Registry 登录文件，不使用本地文件锁作为 CI 串行替代。正常、失败及取消路径均清理本次认证和任务资源；日志不得输出 Token、认证文件或 TLS 私钥。认证目录不是跨 Runner 缓存、恢复点或持久凭据。

## Runner 与真实构建隔离

Job 使用账号可用的 `docker` 标签和临时目录，不绑定 Runner 名称/ID，不注册或改造共享 Runner。已核实平台为 Forgejo 17.0.2、Runner v13.2.2 和专用 Docker 29.10.0 TLS DIND；既有平台证据不能代替本轮新工作流验收。

- 可信控制任务沿用 Runner 的 `DOCKER_HOST=tcp://docker:2376`、`DOCKER_TLS_VERIFY=1`、`DOCKER_CERT_PATH=/certs/client`。要求原生 Linux amd64 引擎；不覆盖为明文 `2375`、本地 socket、默认 context 或 `DOCKER_CONTEXT`。Buildx 使用本任务具名 context/builder，精确清理，不改父证书或共享 daemon。
- 本项目保留真实 Testcontainers 集成检查：控制器在本次网络启动独立临时 privileged 测试 DIND，固定 `docker:29.10.0-dind@sha256:4bbc7223f81db790097c6dea42fb93e05b8fead4f0431fbe6ccaba6e8473201c`，仅用本次匿名数据/证书卷，独立生成 TLS 证书；无父 socket/证书、无宿主端口。privileged DIND 共享内核的风险已获接受，不宣称虚拟机沙箱。
- 只把测试 daemon 的三个临时客户端证书复制给候选，临时副本文件 0444、子目录 0755，外层 scratch 0700，父证书权限不变。候选连接任务网络别名 `docker:2376`、`/work/test-docker-certs` 和 `TESTCONTAINERS_HOST_OVERRIDE=docker`，不是 Runner 网络的父引擎。
- 固定源码用 `git archive` / `docker cp` 传入非 root `node` 候选，无 `.git`、发布凭据、父证书、生产 socket 或宿主 bind mount，保留 `--cap-drop=ALL`、`no-new-privileges`。数据库、Redis、网络均为隔离夹具。
- 保留前端 lint/typecheck/重点测试、后端单元/集成/lint、六包与双架构 OCI、Linux amd64 裸二进制版本/health/setup/login/内嵌 UI 检查。另将**最终 OCI 原件**的 Linux amd64 分支转换为本任务 Docker archive，导入既有非生产 TLS 引擎，以镜像默认 ENTRYPOINT、无网络及有限资源运行 `-version`，核对版本和完整源码 SHA；不覆盖入口或直接调用二进制，不重建、也不提前推 Registry。失败不得生成新完成元数据。该检查覆盖入口权限/降权路径，但不是最终镜像的数据库、health 或旧库升级自动验收；既有裸二进制集成门禁继续保留。另五包是交叉编译；ARM64 OCI 不等于 ARM64 原生运行验收。逐次记录实际 skip 和执行范围，不能声称全部测试均未跳过。
- 发布控制保持 `umask 077` 保护凭据和临时目录；只为公开镜像上下文保留源码文件 mode，Dockerfile 对入口脚本显式 `COPY --chmod=0755`。不能依赖 `chmod +x` 将0600修成0755，否则降权后 shell 无法读取0711脚本。回归在077下检查上下文权限、最终镜像默认入口以及失败阻断。
- 正常异常由控制器 finally 清理，工作流 `always()` 再按本次 run/attempt 精确兜底；只删除确切容器（含最终镜像检查容器）、匿名卷、网络、候选及检查镜像、builder/context 和临时目录。取消不保证进程 finally 执行，因此不能只依赖 Python 栈清理。禁止全局 prune、共享服务重启、Generic 暂存、原件分片、checkpoint 或跨任务缓存。

## 公开产物和完成门禁

Forgejo 正式版本包含 Linux/macOS/Windows × amd64/arm64 六个程序包、`SHA256SUMS`、`build.json`，以及最后上传的 `release-complete.json`，共 **9 个附件**。包集合、大小、哈希、TAG 完整 SHA、build 原始字节哈希和双架构二进制必须一致。

新 Forgejo 镜像按以下顺序发布：先 `<version>-amd64` / `<version>-arm64` 子镜像并保留摘要，再 index-only 发布 `<version>`。主索引必须恰好指向这两个平台及其原始字节摘要；Registry 内 `sha256:…` 子清单是正常内容身份，不因名称不易读而删除历史摘要条目。旧完成版本不补架构标签、不改写版本产物。

认证上传后，使用**独立空 authfile、临时 HOME/XDG** 再匿名验收顶层索引及两个架构子清单原始摘要。两种验收都成功才能转正式；完成标记最后写入并回读。仓库公开、软件包可见性字段、OCI source label、附件非空或 API 认证成功都不能代替匿名读取。失败保留草稿，明确“上传完成但公开分发受阻”，不写完成标记或推进 `latest`。

完成标记回读后，仅 main 请求且实际发布或复用记录也来自 main 时，依据已验收 `main` 版本源码祖先关系维护 `latest`；dev 请求或 dev 来源记录不维护此别名，历史重跑不能使其回退，main 分叉或未知历史别名停止核对，不自动认领；下节精确冻结的首次承接窗口是唯一旧摘要例外。Forgejo `latest` 使用已验证子清单的 index-only 更新；GitHub/GHCR `latest` 由独立同步入口处理。不创建 `latest` Git TAG，也不恢复旧的主/次版本滚动别名。

## 幂等、恢复和未知结果

- 同一完整 SHA 已有完成版时，只验证复用原版本，不新增 N、不改版本正文/分支/产物；完成标记损坏不降级为 unfinished。多个完成证据冲突立即停止。可按当前规则检查受控别名，但不是重写完成版本。
- 不完成旧流程残留前，先核对原注解 TAG、源码/控制 SHA、请求、原 run/attempt 及创建主体，旧 run 已终止且没有其他活跃写入。新旧事件严格匹配各自原工作流；不能把旧三输入事件当作新 schema 5 来源。
- **先构建并验收替代原件，再精确清理未完成版本。** 构建前后、删除前比较稳定快照：Release/附件稳定 ID、名称/类型/大小、正文/状态、TAG 对象及剥离 SHA、原始元数据哈希、包 ID 和镜像摘要。下载统计及附件顺序不作为稳定身份；真实变更或归属不明则零删除。
- 只删除已证明归属的确切 Release/镜像版本并回读；包含新流程部分架构标签时也要逐个核对，不自动删除未知标签。失败 TAG 保留，新 N 从原完整 SHA 全量重建，不恢复旧原件。仅剩失败 TAG 时只占号，其他源码的残留不擅自清理。
- DELETE 结果未知、仍有写入、已有完成证据时停止，不重放、不全包删除。网页/API 超时先按请求 UUID、全局 run ID/attempt、源码/控制 SHA 和产物核对唯一任务，不额外 dispatch 或自动重跑。

## 独立 Manual GitHub sync

同步控制器 `scripts/sync-github.py` 沿用 RawMark 的单文件结构，仅导入 Python 标准库，不依赖同目录 Python 模块；工作流可原样用 `runpy.run_path()` 读取 `USE_LFS`，不需要 `PYTHONPATH` 或 `sys.path` 补丁。同步所需的临时认证、Forgejo HTTP 传输及本项目历史/别名校验都在脚本内；仍保留 curl 作为 Forgejo 边缘传输工具，`scripts/delivery-history.json` 作为固定历史数据输入。`delivery_ci.py`、`gitea_http.py` 继续供发布控制使用，不因同步单文件化删除。修改安全边界时须同时核对两种入口的对应测试。

操作者另行启动无输入的同步工作流，发布任务不串联它。范围仅为获准公开的 `main` 及其可达、已验收正式 TAG/Release/镜像，不公开 `dev`，不更新 GitHub `production`，不强推、不改写既有分发。同步前检查 GitHub Actions 确实关闭、目标权限和 Git/Registry 传输认证，并在任何目标写入前只读预检 main 祖先选版与两端已有 `latest`；未知别名或 main 分叉立即停止，真正更新前再次核验。API Token 可用不代表 Git 推送权限可用。

GitHub 每版只接收 **8 个附件**：六程序包、`SHA256SUMS`、`build.json`，不复制内部 `release-complete.json`；GHCR 继续 `--all --preserve-digests` 全量复制双架构。目标已一致则复用，冲突停止。认证及独立匿名索引/子清单验收成功后才能将目标草稿转正式。全部新流程版本验证成功，且没有未核对的历史证据缺口后，才按 main 祖先规则推进 GHCR `latest`；有些版本已补齐但整体存在缺口时仍返回失败，逐版报告，不模拟总成功。

### 固定导入档案与首次 latest 承接

[`scripts/delivery-history.json`](../scripts/delivery-history.json) 是经操作者批准的固定历史边界，不是新完成证明，也不是自动扩展的忽略名单。清单绑定两个确切仓库、16 个历史 Release、128 个附件（共 6,224,072,508 字节）、原 TAG 对象/剥离 commit、两端 Release/附件 ID、正文哈希、Forgejo 附件 UUID 和原件 SHA256；来源是此前原样迁移的逐字节核对及本次只读元数据回查。旧五包/reauth 不重建，不补 marker，不删除或覆盖。

同步在**任何目标写入之前**检查这 16 版两端均存在且正式非预发布，正文、名称、TAG 身份和完整附件集合都精确一致。GitHub 的服务端入库 digest 必须匹配原件 SHA256；Forgejo 没有附件 digest 字段，须重新流式下载核对全部原件，不落盘保存 6GB。该检查会产生约 6.22GB 的 Forgejo 下载流量与相应耗时，不是缓存或模拟验收。任一缺失、增删、ID/字节或 TAG 变化即停止，不能边修复档案边同步新版本；统计和附件顺序不作为身份。全部通过后独立报告“历史档案核对通过”，不作为新六包/schema5/完成标记版本计数。清单之外的无证明版本仍是真正缺口，必须非零并阻止 latest。

仅首次受控承接允许两端**精确完整** `:latest` 引用处于批准的旧摘要 `sha256:754be0e7b4ddc86092d55b10cb4cc0fb35aa8b3f6de32da34a191f2886099857`。目标必须是完整验收过的 schema5 main 完成版，源码为冻结边界 `6d0b8849328a5be3acf187649fa87bb96709e846` 的严格后代，且这是边界之后唯一的已完成 schema5 main 版本；同步在可能维护 latest 的任何目标写入前，以及实际写 latest 前，均只读核对 scope 外完成版的最小证明；普通祖先、已等于选中摘要和 alias 缺失路径也不跳过检查。所有已验证 main（含无 schema/schema4）参与分叉阻断，只有边界后的 schema5 main 计入首次窗口，不扩大这些版本的同步权限。原 main 祖先/分叉、匿名双架构门禁及写前复查仍须通过。不适用于 dev、旧无 schema、错误仓库、其他摘要或第二个新主线完成版；普通已知祖先前进不受此窗口影响。

这是有限的首次迁移窗口，不是永久接受旧摘要或允许历史回退。应在第一个新 main 版本完成后分别验收 Forgejo 发布与独立 GHCR 同步；出现第二个新主线完成版就关闭旧摘要通道。首次真实承接验收后再移除清单中的 alias 授权并交付变更，保留历史档案清单。代码交付不立即改变 alias；实际切换仍在另行启动的发布/同步步骤发生，已有完成版本始终不改写。

GHCR 首次建包或整包删除后重建通常为 private，公开代码仓库不使包自动 Public。仅实际需要并获授权时，由操作者在**确切包**的 Settings / Danger Zone 设置 Public，再重新验收；没有可虚构的 REST 可见性参数，不自动改可见性或删除重建。2026-11-22 只读审计已确认现有 `eezd/sub2api` 包为 Public，该事实不代替后续每版匿名验收。

## 离线验证和真实验收边界

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p 'test_*.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 .github/release-tools/test_release_matrix.py
bash -n scripts/build-release.sh scripts/package-release.sh
git diff --check
```

测试使用临时 Git 历史、HOME、合成 Secret、模拟 API/Registry，不读取真实凭据或创建 Docker 服务。测试应覆盖来源冻结/旧证据、显式认证、匿名失败草稿、架构顺序、别名祖先、稳定指纹、先 build 后删及取消精确清理；本地通过不证明新的 Actions、实例权限、并发、Registry 和真实构建已验收。交付必须分别报告本地代码/测试、可信 main 安装、真实 Forgejo 发布、独立 GitHub 同步和人工可见性步骤。

## 本机编译与 systemd

本机流程独立于上述两个 Actions。按[贡献指南](../CONTRIBUTING.md#按改动范围验证)构建前端，再以 `go build -tags embed` 编译；仅在获授权目标环境使用 [`sub2api.service`](sub2api.service)，其程序路径为 `/opt/sub2api/sub2api`，配置/数据见[原生部署说明](README.md#binary-installation)。不恢复已撤下的常驻开发 unit；临时检查只能使用隔离 PostgreSQL/Redis/配置/数据。发布、同步不等于部署或授权启动与 SQL 迁移。现有 GitHub 安装器、升级器和 reauth 也不自动兼容新 Forgejo 六包格式。
