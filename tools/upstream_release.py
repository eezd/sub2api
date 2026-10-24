#!/usr/bin/env python3
"""同步 sub2api 上游并复用现有 Actions 发布流程。"""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


UPSTREAM = 'ranxi2001/sub2api'
FORK = 'eezd/sub2api'
BRANCH = 'production'
VERSION = 'backend/cmd/server/VERSION'
STABLE = re.compile(r'v(\d+)\.(\d+)\.(\d+)')
SHA = re.compile(r'[0-9a-f]{40}')
RELEASE_CHECKS = {
    'ci': ('backend-ci.yml', {'shell', 'test', 'frontend', 'golangci-lint', 'release-helpers', 'prism-adapter'}),
    'security': ('security-scan.yml', {'backend-security', 'frontend-security'}),
    'caddy': ('multihost-caddy.yml', {'real-proxy-responses'}),
    'fullapp': ('multihost-fullapp.yml', {'isolated-real-applications'}),
}
RELEASE_JOBS = {'prepare', 'build-frontend', 'release', 'sync-version-file'} | {
    f'{caller} / {job}' for caller, (_, jobs) in RELEASE_CHECKS.items() for job in jobs
}


class ReleaseError(Exception):
    pass


def command(args, cwd=None, check=True):
    result = subprocess.run(args, cwd=cwd, text=True, capture_output=True)
    if check and result.returncode:
        # 不回显参数，避免未来调用方传入凭据后被错误信息泄露。
        raise ReleaseError(f'{args[0]} 执行失败：{result.stderr.strip() or result.stdout.strip()}')
    return result


def git(root, *args):
    return command(['git', *args], cwd=root).stdout.strip()


class GitHub:
    def __init__(self):
        self.token = os.environ.get('GH_TOKEN') or os.environ.get('GITHUB_TOKEN')
        if not self.token and shutil.which('gh'):
            result = command(['gh', 'auth', 'token'], check=False)
            if not result.returncode:
                self.token = result.stdout.strip()

    def get(self, path, optional=False):
        headers = {'Accept': 'application/vnd.github+json', 'User-Agent': 'sub2api-upstream-release'}
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'
        request = urllib.request.Request('https://api.github.com/' + path, headers=headers)
        for attempt in range(3):
            try:
                with urllib.request.urlopen(request, timeout=30) as response:
                    return json.load(response)
            except urllib.error.HTTPError as error:
                if optional and error.code == 404:
                    return None
                if error.code not in (429, 500, 502, 503, 504) or attempt == 2:
                    raise ReleaseError(f'GitHub 查询失败（HTTP {error.code}）：{path}') from None
            except (TimeoutError, urllib.error.URLError):
                if attempt == 2:
                    raise ReleaseError(f'GitHub 查询超时或连接失败：{path}') from None
            time.sleep(2 ** attempt)

    def pages(self, path, key=None):
        separator = '&' if '?' in path else '?'
        result = []
        for page in range(1, 101):
            data = self.get(f'{path}{separator}per_page=100&page={page}')
            items = data[key] if key else data
            result.extend(items)
            if len(items) < 100:
                return result
        raise ReleaseError('GitHub 分页超过上限，停止以避免使用不完整结果')


def next_tag(upstream_tag, existing):
    if not STABLE.fullmatch(upstream_tag):
        raise ReleaseError('上游必须是 vX.Y.Z 正式版本')
    pattern = re.compile(re.escape(upstream_tag) + r'-custom\.(\d+)')
    numbers = [int(match[1]) for tag in existing if (match := pattern.fullmatch(tag))]
    return f'{upstream_tag}-custom.{max(numbers, default=0) + 1}'


def repository(path):
    root = Path(git(path, 'rev-parse', '--show-toplevel'))
    if git(root, 'branch', '--show-current') != BRANCH:
        raise ReleaseError('必须在 production 分支运行')
    allowed = {f'git@github.com:{FORK}.git', f'https://github.com/{FORK}.git',
               f'https://github.com/{FORK}', f'ssh://git@github.com/{FORK}.git'}
    for mode in ([], ['--push']):
        urls = git(root, 'remote', 'get-url', *mode, '--all', 'origin').splitlines()
        if len(urls) != 1 or urls[0] not in allowed:
            raise ReleaseError('origin 必须且只能指向 eezd/sub2api')
    return root


def clean(root):
    if git(root, 'status', '--porcelain', '--untracked-files=no'):
        raise ReleaseError('存在已跟踪文件的未提交修改；保留现场，请先处理再继续')
    if Path(git(root, 'rev-parse', '--path-format=absolute', '--git-path', 'MERGE_HEAD')).exists():
        raise ReleaseError('存在未完成的合并，请先解决冲突并提交')


def state_path(root):
    return Path(git(root, 'rev-parse', '--absolute-git-dir')) / 'upstream-release.json'


def save(root, state):
    path = state_path(root)
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n')
    temporary.replace(path)


def load(root):
    path = state_path(root)
    if not path.exists():
        raise ReleaseError('没有发布进度，请先运行 sync')
    return json.loads(path.read_text())


@contextmanager
def locked(root):
    path = state_path(root).with_suffix('.lock')
    with path.open('w') as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ReleaseError('已有同步或发布命令在运行') from None
        yield


def remote_sha(root, ref):
    lines = git(root, 'ls-remote', 'origin', ref).splitlines()
    return lines[0].split()[0] if lines else None


def plan(root, api):
    release = api.get(f'repos/{UPSTREAM}/releases/latest')
    tag = release['tag_name']
    if release['draft'] or release['prerelease'] or not STABLE.fullmatch(tag):
        raise ReleaseError('上游 latest 不是正式版本')
    head = api.get(f'repos/{UPSTREAM}/branches/{BRANCH}')['commit']['sha']
    comparison = api.get(f'repos/{UPSTREAM}/compare/{tag}...{head}')
    if comparison['status'] not in ('ahead', 'identical'):
        raise ReleaseError('上游 production 不是最新正式版 tag 的后代')
    tag_sha = comparison['base_commit']['sha']
    tags = [row['name'] for row in api.pages(f'repos/{FORK}/tags')]
    tags.extend(row['tag_name'] for row in api.pages(f'repos/{FORK}/releases'))
    tags.extend(line.split()[1].removeprefix('refs/tags/') for line in
                git(root, 'ls-remote', '--tags', '--refs', 'origin').splitlines())
    return {
        'upstream_tag': tag, 'upstream_sha': head, 'upstream_tag_sha': tag_sha,
        'fork_sha': remote_sha(root, f'refs/heads/{BRANCH}'), 'tag': next_tag(tag, tags),
        'extra_commits': comparison['ahead_by'], 'upstream_notes': release.get('body') or '',
        'stage': 'planned',
    }


def ancestor(root, older, newer):
    result = command(['git', 'merge-base', '--is-ancestor', older, newer], cwd=root, check=False)
    if result.returncode not in (0, 1):
        raise ReleaseError('无法检查提交祖先关系')
    return result.returncode == 0


def fork_guards(root):
    required = {
        '.github/workflows/release.yml': ["'v*-custom.*'", 'secrets.GHCR_TOKEN', 'sync-version-file:'],
        '.github/release-tools/release_matrix.py': ['CUSTOM_VERSION_RE', 'def is_prerelease('],
        '.github/release-tools/release-images.sh': ['-custom', 'amd64 arm64'],
        'backend/internal/service/update_service.go': ['"eezd/sub2api"', 'customVersionMarker'],
        'backend/internal/reauthruntime/runtime.go': ['"eezd/sub2api"'],
        'deploy/install.sh': ['eezd/sub2api'],
        'deploy/upgrade-k3s.sh': ['eezd/sub2api', '-custom'],
        'deploy/docker-compose.yml': ['ghcr.io/eezd/sub2api'],
    }
    for filename, markers in required.items():
        path = root / filename
        if not path.is_file() or any(marker not in path.read_text() for marker in markers):
            raise ReleaseError(f'自有修改检查失败：{filename}；请审查合并结果')
    # 独立 push 检查会让一次发版启动多个 Actions；旧试验流程已并入 CI。
    for path in (root / '.github/workflows').glob('*.yml'):
        if path.name != 'release.yml' and re.search(r'^  push:', path.read_text(), re.MULTILINE):
            raise ReleaseError(f'违反单次发布 Actions 约束：{path.name} 仍有独立 push 触发器')
    workflow = (root / '.github/workflows/release.yml').read_text()
    for _, (filename, _) in RELEASE_CHECKS.items():
        check = root / '.github/workflows' / filename
        if (not check.is_file() or '  workflow_call:' not in check.read_text() or
                f'uses: ./.github/workflows/{filename}' not in workflow):
            raise ReleaseError(f'发布检查未接入单次 Release：{filename}')


def release_settings(api):
    if not api.token:
        raise ReleaseError('仓库变量检查需要 GH_TOKEN / GITHUB_TOKEN 或 gh 登录，不能将权限不足误判为变量不存在')
    variables = api.pages(f'repos/{FORK}/actions/variables', key='variables')
    if any(row['name'] == 'SIMPLE_RELEASE' and row['value'].lower() == 'true' for row in variables):
        raise ReleaseError('SIMPLE_RELEASE=true 会覆盖全量发布要求，请先修改仓库变量')


def sync(root, api, args):
    clean(root)
    path = state_path(root)
    if path.exists():
        state = load(root)
        if state['stage'] == 'verified':
            state = None
        else:
            if args.upstream_sha and args.upstream_sha != state['upstream_sha']:
                raise ReleaseError('已有未完成的固定目标，不能切换上游 SHA')
            if state['stage'] == 'tagged':
                print('已有发布进度，请继续 publish / verify', flush=True)
                return state
            if state['stage'] == 'pushed':
                head = git(root, 'rev-parse', 'HEAD')
                if head == state['sha']:
                    print(f'已推送 {head}，请继续 publish / verify', flush=True)
                    return state
                if not ancestor(root, state['sha'], head):
                    raise ReleaseError('本地偏离已推送提交，请先分析分叉')
                if remote_sha(root, f'refs/tags/{state["tag"]}'):
                    raise ReleaseError('已有同名远端 tag，不能修改本次发布提交')
    else:
        state = None
    if state is None:
        state = plan(root, api)
        if not args.upstream_sha or args.upstream_sha != state['upstream_sha']:
            raise ReleaseError('请先运行 plan，并用 --upstream-sha 指定本次固定目标；上游变化后需重新检查')
        if git(root, 'rev-parse', 'HEAD') != state['fork_sha']:
            raise ReleaseError('本地与远端 production 不一致，请先核对并同步')
        release_settings(api)
        git(root, 'fetch', '--no-tags', f'https://github.com/{UPSTREAM}.git', state['upstream_sha'])
        git(root, 'fetch', '--no-tags', 'origin', BRANCH)
        if not ancestor(root, state['upstream_tag_sha'], state['upstream_sha']):
            raise ReleaseError('上游 tag 与固定 SHA 的祖先关系检查失败')
        if ancestor(root, state['upstream_sha'], 'HEAD'):
            raise ReleaseError('该上游提交已合入；如需单独发布本地改动，应使用现有发布流程')
        save(root, state)
    if not ancestor(root, state['upstream_sha'], 'HEAD'):
        result = command(['git', 'merge', '--no-ff', '--no-commit', state['upstream_sha']], cwd=root, check=False)
        if result.returncode:
            state['stage'] = 'conflicts'
            save(root, state)
            raise ReleaseError('合并发生冲突，已保留现场；解决冲突并提交后再次运行 sync')
        # VERSION 随上游合并，正式版本由发布流水线写入。
        fork_guards(root)
        git(root, 'diff', '--cached', '--check')
        git(root, 'commit', '-m', f'merge: sync upstream {state["upstream_tag"]}')
    if not ancestor(root, state['fork_sha'], 'HEAD'):
        raise ReleaseError('合并结果丢失原 production 历史，停止推送')
    fork_guards(root)
    previous_sha = state.get('pushed_sha', state['fork_sha'])
    state['sha'] = git(root, 'rev-parse', 'HEAD')
    state['stage'] = 'merged'
    save(root, state)
    if remote_sha(root, f'refs/heads/{BRANCH}') not in (state['fork_sha'], previous_sha, state['sha']):
        raise ReleaseError('远端 production 已变化，停止推送，请先分析并发修改')
    git(root, 'push', 'origin', f'{state["sha"]}:refs/heads/{BRANCH}')
    state['stage'] = 'pushed'
    state['pushed_sha'] = state['sha']
    save(root, state)
    print(f'已推送 {state["sha"]}，目标版本 {state["tag"]}', flush=True)
    return state


def successful_run(api, sha, tag):
    query = urllib.parse.urlencode({'head_sha': sha, 'event': 'push', 'branch': tag})
    runs = api.get(f'repos/{FORK}/actions/workflows/release.yml/runs?{query}&per_page=100')['workflow_runs']
    runs = [run for run in runs if run['head_sha'] == sha and run['event'] == 'push' and run['head_branch'] == tag]
    if not runs:
        return None
    run = max(runs, key=lambda row: row['id'])
    if run['status'] != 'completed':
        return None
    if run['conclusion'] != 'success':
        raise ReleaseError(f'Release 未通过：{run["html_url"]}')
    jobs = api.pages(f'repos/{FORK}/actions/runs/{run["id"]}/jobs', key='jobs')
    names = {job['name'] for job in jobs if job['conclusion'] == 'success'}
    if not RELEASE_JOBS <= names or any(job['conclusion'] != 'success' for job in jobs):
        raise ReleaseError(f'Release 有缺失、跳过或失败的任务：{run["html_url"]}')
    return run


def wait_for(check, timeout):
    deadline = time.monotonic() + timeout
    while True:
        result = check()
        if result:
            return result
        if time.monotonic() >= deadline:
            raise ReleaseError('等待超时，进度已保留；检查远端任务后可重新运行同一命令')
        print('等待 GitHub Actions…', flush=True)
        time.sleep(min(20, max(0, deadline - time.monotonic())))


def tag_notes(state):
    # 移除上游安装段，避免原文附带的安装命令把用户导向上游。
    notes = re.split(r'(?m)^##\s+(?:📥\s*)?Installation\s*$', state['upstream_notes'])[0].rstrip()
    extra = (f'\n\n同步上游 {state["upstream_tag"]}，production 固定提交 '
             f'{state["upstream_sha"]}（tag 后额外 {state["extra_commits"]} 个提交）；'
             '保留 eezd 自有发布、安装和更新来源。')
    # 上游正文通常已含平台简介；GoReleaser 会统一添加，避免重复。
    notes = re.sub(r'^> AI API Gateway Platform[^\n]*\n\s*', '', notes)
    notes = re.sub(r'\n\s*---\s*$', '', notes).rstrip()
    return notes + extra + '\n'


def publish(root, api, args):
    clean(root)
    state = load(root)
    if state['stage'] not in ('pushed', 'tagged', 'verified'):
        raise ReleaseError('合并尚未推送，请先继续 sync')
    if args.sha != state['sha']:
        raise ReleaseError('--sha 必须与本次已推送提交完全一致')
    if state['stage'] in ('tagged', 'verified'):
        print(f'已推送 {state["tag"]}，请运行 verify；不会自动重发或覆盖 tag', flush=True)
        return state
    if not api.token:
        raise ReleaseError('发布检查需要 GH_TOKEN / GITHUB_TOKEN 或 gh 登录，避免将权限不足误判为变量不存在')
    release_settings(api)
    tag_ref = f'refs/tags/{state["tag"]}'
    existing = git(root, 'ls-remote', 'origin', tag_ref, tag_ref + '^{}')
    notes = tag_notes(state)
    message = f'Sub2API {state["tag"].removeprefix("v")}\n\n{notes}'
    if existing:
        tagged = git(root, 'ls-remote', 'origin', tag_ref + '^{}')
        if not tagged or tagged.split()[0] != state['sha']:
            raise ReleaseError('远端 tag 已被占用，不能覆盖')
        state['stage'] = 'tagged'
        save(root, state)
        return state
    if git(root, 'rev-parse', 'HEAD') != state['sha']:
        raise ReleaseError('本地 HEAD 已变化，请审查后重新执行 sync')
    if remote_sha(root, f'refs/heads/{BRANCH}') != state['sha']:
        raise ReleaseError('远端 production 已变化，不能发布旧的合并提交')
    fork_guards(root)
    if api.get(f'repos/{FORK}/releases/tags/{state["tag"]}', optional=True):
        raise ReleaseError('目标版本已有 Release，停止重复发布')
    local = command(['git', 'rev-parse', '--verify', tag_ref], cwd=root, check=False)
    if local.returncode == 0:
        if (git(root, 'rev-parse', f'{tag_ref}^{{commit}}') != state['sha'] or
                git(root, 'cat-file', '-t', tag_ref) != 'tag' or
                git(root, 'for-each-ref', '--format=%(contents)', tag_ref).strip() != message.strip()):
            raise ReleaseError('本地同名 tag 内容不同，停止以保留现场')
    else:
        with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8') as stream:
            # GoReleaser 读取 tag 的 contents:body，首段作为独立标题。
            stream.write(message)
            stream.flush()
            git(root, 'tag', '-a', state['tag'], state['sha'], '-F', stream.name)
    git(root, 'push', 'origin', f'{tag_ref}:{tag_ref}')
    state['stage'] = 'tagged'
    save(root, state)
    print(f'已触发全量发布：{state["tag"]}', flush=True)
    return state


def expected_assets(tag):
    version = tag.removeprefix('v')
    names = {f'sub2api_{version}_{system}_{arch}.{suffix}' for system, arch, suffix in (
        ('linux', 'amd64', 'tar.gz'), ('linux', 'arm64', 'tar.gz'),
        ('darwin', 'amd64', 'tar.gz'), ('darwin', 'arm64', 'tar.gz'), ('windows', 'amd64', 'zip'))}
    runtimes = {f'sub2api-reauth_{version}_linux_{arch}.tar.gz' for arch in ('amd64', 'arm64')}
    return names, runtimes


def download(url, destination):
    # 资产使用公开下载地址，不向跨域重定向附带 GitHub API 凭据。
    if not url.startswith(f'https://github.com/{FORK}/releases/download/'):
        raise ReleaseError('资产下载地址不属于自有仓库')
    with urllib.request.urlopen(url, timeout=60) as response, destination.open('wb') as stream:
        shutil.copyfileobj(response, stream)


def verify_checksums(directory, names):
    checksums = {}
    for line in (directory / 'checksums.txt').read_text().splitlines():
        fields = line.split()
        if len(fields) != 2 or not re.fullmatch(r'[0-9a-fA-F]{64}', fields[0]):
            raise ReleaseError('checksums.txt 格式无效')
        name = fields[1].lstrip('*')
        if name in checksums:
            raise ReleaseError('checksums.txt 包含重复文件')
        checksums[name] = fields[0].lower()
    for name in names:
        with (directory / name).open('rb') as stream:
            actual = hashlib.file_digest(stream, 'sha256').hexdigest()
        if checksums.get(name) != actual:
            raise ReleaseError(f'资产校验失败：{name}')


def verify_images(tag):
    if not shutil.which('docker'):
        raise ReleaseError('镜像验收需要 docker buildx，请安装后重跑 verify')
    version = tag.removeprefix('v')
    major, minor, _ = version.split('-')[0].split('.')
    digest = None
    for alias in (version, 'latest', f'{major}.{minor}', major):
        raw = command(['docker', 'buildx', 'imagetools', 'inspect', '--raw', f'ghcr.io/{FORK}:{alias}']).stdout
        manifest = json.loads(raw)
        platforms = {(row.get('platform', {}).get('os'), row.get('platform', {}).get('architecture'))
                     for row in manifest.get('manifests', [])}
        if not {('linux', 'amd64'), ('linux', 'arm64')} <= platforms:
            raise ReleaseError(f'镜像 {alias} 缺少 amd64 或 arm64')
        current = hashlib.sha256(raw.encode()).hexdigest()
        if digest and current != digest:
            raise ReleaseError(f'镜像别名 {alias} 未指向本次版本')
        digest = current


def verify_artifacts(api, tag):
    release = api.get(f'repos/{FORK}/releases/tags/{tag}')
    if release['draft'] or release['prerelease']:
        raise ReleaseError('自有版本必须是正式 Release')
    binaries, runtimes = expected_assets(tag)
    assets = {asset['name']: asset for asset in release['assets']}
    required = binaries | runtimes | {'checksums.txt'}
    if not required <= assets.keys() or any(
            assets[name]['size'] <= 0 or assets[name]['state'] != 'uploaded' for name in required):
        raise ReleaseError('Release 附件缺失或为空')
    with tempfile.TemporaryDirectory(prefix='sub2api-release-') as directory:
        directory = Path(directory)
        for name in binaries | {'checksums.txt'}:
            download(assets[name]['browser_download_url'], directory / name)
        verify_checksums(directory, binaries)
    verify_images(tag)
    return release


def verify(root, api, args):
    state = load(root)
    if state['stage'] not in ('tagged', 'verified'):
        raise ReleaseError('尚未推送 tag，请先运行 publish')
    tagged = git(root, 'ls-remote', 'origin', f'refs/tags/{state["tag"]}^{{}}')
    if not tagged or tagged.split()[0] != state['sha']:
        raise ReleaseError('远端 tag 与固定发布 SHA 不一致，停止验收')
    run = wait_for(lambda: successful_run(api, state['sha'], state['tag']), args.timeout)
    release = verify_artifacts(api, state['tag'])
    clean(root)
    git(root, 'fetch', '--no-tags', 'origin', BRANCH)
    remote = git(root, 'rev-parse', 'FETCH_HEAD')
    current = git(root, 'rev-parse', 'HEAD')
    if not ancestor(root, current, remote):
        raise ReleaseError('发布已完成，但本地存在分叉，不能快进同步 VERSION')
    git(root, 'merge', '--ff-only', remote)
    if (root / VERSION).read_text().strip() != state['tag'].removeprefix('v'):
        raise ReleaseError('发布机器人尚未正确同步 VERSION')
    state.update(stage='verified', release_url=release['html_url'], run_url=run['html_url'])
    save(root, state)
    print(json.dumps(state, ensure_ascii=False, indent=2), flush=True)
    return state


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', default='.', help='本地仓库路径')
    subparsers = parser.add_subparsers(dest='action', required=True)
    subparsers.add_parser('plan', help='只读检查上游、固定目标与下一个版本号')
    prepare = subparsers.add_parser('sync', help='固定目标、合并并推送 production；冲突时保留现场')
    prepare.add_argument('--upstream-sha', help='plan 输出的固定上游 SHA；首次同步必填')
    release = subparsers.add_parser('publish', help='推送 tag，在一次 Release 内检查、构建并发布')
    release.add_argument('--sha', required=True, help='已审查的本次合并提交完整 SHA')
    inspect = subparsers.add_parser('verify', help='验收 Release、镜像并快进同步 VERSION')
    inspect.add_argument('--timeout', type=int, default=7200, help='发布等待上限，单位秒')
    args = parser.parse_args()
    try:
        if hasattr(args, 'timeout') and args.timeout < 0:
            raise ReleaseError('等待上限不能为负数')
        if getattr(args, 'upstream_sha', None) and not SHA.fullmatch(args.upstream_sha):
            raise ReleaseError('上游 SHA 必须是完整的 40 位十六进制提交 ID')
        root = repository(args.repo)
        api = GitHub()
        if args.action == 'plan':
            print(json.dumps(plan(root, api), ensure_ascii=False, indent=2))
        else:
            with locked(root):
                {'sync': sync, 'publish': publish, 'verify': verify}[args.action](root, api, args)
    except (ReleaseError, OSError, ValueError, KeyError) as error:
        print(f'错误：{error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
