#!/usr/bin/env bash
# 远端发布客户端：不读取本地 Git 状态，不推送或切换分支。
set -euo pipefail
if (( $# != 1 )) || [[ -z $1 ]]; then
    printf '用法：bash %s <远端分支>\n' "$0" >&2
    exit 2
fi
command -v python3 >/dev/null || { printf '缺少命令：python3\n' >&2; exit 1; }
command -v curl >/dev/null || { printf '缺少命令：curl\n' >&2; exit 1; }
project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
exec python3 - "$1" "$project_dir" <<'PY'
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.parse
import uuid

sys.path.insert(0, str(Path(sys.argv[2]) / 'scripts'))
from gitea_http import request as http_request

# 初始化时渲染；凭据不得写在这里。
API_URL = 'https://git.134333.xyz/api/v1'  # 例如 https://git.example.com/api/v1
OWNER = 'eezd'
REPO = 'sub2api'
WORKFLOW = 'sub2api-manual-release.yml'
FORGEJO_DOWNLOAD_HOSTS = ()  # 如有对象存储，填入已核实的 HTTPS authority；不发送令牌
WAIT_SECONDS = 7200
POLL_SECONDS = 5


def fail(message):
    raise RuntimeError(message)


def load_token():
    token = os.environ.get('FORGEJO_TOKEN')
    if token:
        return token
    path = Path.home() / '.config/forgejo/token'
    try:
        token = path.read_text(encoding='utf-8').strip()
    except FileNotFoundError:
        token = ''
    except (OSError, UnicodeError):
        fail(f'读取 Forgejo 凭据文件失败：{path}；请检查文件、编码及读取权限，或设置 FORGEJO_TOKEN')
    if not token:
        fail(f'缺少 FORGEJO_TOKEN；请设置该环境变量或将非空 Token 写入 {path}')
    return token


def quote(value):
    return urllib.parse.quote(value, safe='')


def request(url, token, payload=None, *, phase, download_hosts=()):
    headers = {'Accept': 'application/octet-stream' if download_hosts else 'application/json'}
    data = None
    method = 'GET'
    if payload is not None:
        method = 'POST'
        data = json.dumps(payload).encode()
        headers['Content-Type'] = 'application/json'
    auth = 'token ' + token
    for _ in range(4):
        try:
            with http_request(url, auth, method=method, data=data, headers=headers, limit=1_048_576) as response:
                code, response_headers, stream = response
                body = stream.read(1_048_577)
            if len(body) > 1_048_576:
                fail(phase + '失败：响应超过 1 MiB')
            if 200 <= code < 300:
                return body if download_hosts else json.loads(body) if body else None
            location = response_headers.get('Location')
            if download_hosts and code in (301, 302, 303, 307, 308) and location:
                url = urllib.parse.urljoin(url, location)
                target = urllib.parse.urlsplit(url)
                if (target.scheme != 'https' or target.netloc not in download_hosts or
                        target.username is not None or target.password is not None):
                    fail(phase + '失败：拒绝未批准的下载跳转')
                auth = None
                continue
            reason = {
                401: '认证未通过，请核对凭据',
                403: '请求被拒绝，请核对所需权限及边缘层访问策略',
                404: '资源可能不可见、不存在或接口不支持，请核对目标与接口能力',
            }.get(code, '请核对目标服务状态')
            if 300 <= code < 400:
                reason = '拒绝认证请求重定向'
            fail(f'{phase}失败：HTTP {code}；{reason}；未自动重试发布请求')
        except OSError:
            fail(f'{phase}失败：网络或超时错误；如已提交发布，请按请求 ID 核查，未自动重试')
        except (json.JSONDecodeError, UnicodeDecodeError):
            fail(f'{phase}失败：响应不是有效 JSON；未自动重试')
    fail(phase + '失败：下载跳转过多')


def release_asset(release, name, origin, token):
    matches = [asset for asset in release.get('assets', []) if asset.get('name') == name]
    if len(matches) != 1:
        fail('正式 Release 缺少唯一 ' + name)
    url = urllib.parse.urljoin(API_URL, matches[0]['browser_download_url'])
    target = urllib.parse.urlsplit(url)
    if (target.scheme, target.netloc) != (origin.scheme, origin.netloc):
        fail(name + ' 下载地址不属于已配置 Forgejo；拒绝向其他地址发送凭据')
    return request(url, token, phase='下载 ' + name, download_hosts=(origin.netloc, *FORGEJO_DOWNLOAD_HOSTS))


def main():
    if any('@@' in value for value in (API_URL, OWNER, REPO, WORKFLOW)):
        fail('发布模板尚未配置仓库和工作流')
    parsed = urllib.parse.urlsplit(API_URL)
    if parsed.scheme != 'https' or not parsed.netloc or parsed.username or parsed.password:
        fail('Forgejo API 地址必须为不含凭据的 HTTPS 地址')
    token = load_token()
    branch = sys.argv[1]
    if (branch.startswith(('-', '/')) or branch.endswith('/') or
            any(ord(c) < 32 for c in branch)):
        fail('远端分支名无效')
    base = API_URL.rstrip('/') + '/repos/' + quote(OWNER) + '/' + quote(REPO)
    branch_info = request(base + '/branches/' + quote(branch), token, phase='读取来源分支')
    sha = branch_info['commit']['id']
    if not re.fullmatch(r'[0-9a-f]{40}', sha):
        fail('远端未返回有效的完整提交 SHA')
    commit = request(base + '/git/commits/' + sha, token, phase='读取提交')
    date = datetime.datetime.fromisoformat(commit['commit']['committer']['date'].replace('Z', '+00:00'))
    if date.tzinfo is None:
        fail('远端提交时间缺少时区')
    version = date.astimezone(datetime.timezone.utc).strftime('%Y%m%d') + '-' + sha[:8]
    request_id = str(uuid.uuid4())
    print(f'来源分支：{branch}\n完整 SHA：{sha}\n版本：{version}\n请求 ID：{request_id}', flush=True)
    details = request(base + '/actions/workflows/' + quote(WORKFLOW) +
                      '/dispatches', token,
                      {'ref': 'refs/heads/main', 'return_run_info': True, 'inputs': {
                          'source_branch': branch, 'source_sha': sha, 'request_id': request_id}},
                      phase='调度发布工作流')
    if (not isinstance(details, dict) or type(details.get('id')) is not int or details['id'] <= 0 or
            type(details.get('run_number')) is not int or details['run_number'] <= 0):
        fail('未取得唯一 run ID；任务可能已启动，请按请求 ID 核查，不要直接重复触发')
    run_id = details['id']
    run_api = base + '/actions/runs/' + str(run_id)
    web_base = API_URL.rstrip('/').removesuffix('/api/v1')
    run_url = web_base + '/' + quote(OWNER) + '/' + quote(REPO) + '/actions/runs/' + str(details['run_number'])
    print('工作流：' + run_url, flush=True)
    deadline = time.monotonic() + WAIT_SECONDS
    while True:
        run = request(run_api, token, phase='查询发布任务')
        status = run.get('conclusion') or run.get('status')
        if status == 'success':
            break
        if status in ('failure', 'cancelled', 'canceled', 'skipped', 'timed_out', 'action_required'):
            fail(f'发布未成功：{status}；查看 {run_url}')
        if status not in ('queued', 'waiting', 'pending', 'running', 'in_progress', 'completed'):
            fail(f'工作流返回无法识别的状态，请查看 {run_url}')
        if time.monotonic() >= deadline:
            fail('等待超时，未取消远端发布；请查询已有任务：' + run_url)
        time.sleep(POLL_SECONDS)
    release = request(base + '/releases/tags/' + quote(version), token, phase='核对正式 Release')
    if (release.get('tag_name') != version or release.get('draft') is not False or
            release.get('prerelease') is not False):
        fail('工作流结束，但未找到匹配的正式 Release')
    build_bytes = release_asset(release, 'build.json', parsed, token)
    completion_bytes = release_asset(release, 'release-complete.json', parsed, token)
    build, completion = map(json.loads, (build_bytes, completion_bytes))
    for metadata in (build, completion):
        if not isinstance(metadata, dict) or metadata.get('source_sha') != sha or metadata.get('version') != version:
            fail('发布元数据的版本或 SHA 不匹配')
    if completion.get('build_sha256') != hashlib.sha256(build_bytes).hexdigest():
        fail('Forgejo 发布完成记录中的 build.json SHA256 不匹配')
    print('Forgejo Release：' + web_base + '/' + quote(OWNER) + '/' + quote(REPO) + '/releases/tag/' + version)
    print('Forgejo 发布成功；GitHub 尚未由本命令同步；本地 systemd 服务未更新')


try:
    main()
except KeyboardInterrupt:
    print('已停止本地等待；远端任务未取消，请查看已输出的工作流或请求 ID', file=sys.stderr)
    sys.exit(130)
except RuntimeError as exc:
    print('发布失败：' + str(exc), file=sys.stderr)
    sys.exit(1)
except (KeyError, TypeError, ValueError, AttributeError):
    print('发布失败：配置或响应数据不符合接口约定；请按已输出的请求 ID 核查', file=sys.stderr)
    sys.exit(1)
PY
