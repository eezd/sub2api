"""Standalone manual sync controller, following RawMark's stdlib-only script layout."""
from contextlib import ExitStack, contextmanager
from email.parser import BytesHeaderParser
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid

FORGEJO_API = 'https://git.134333.xyz/api/v1'
FORGEJO_OWNER = 'eezd'
FORGEJO_REPO = 'sub2api'
FORGEJO_GIT = 'https://git.134333.xyz/eezd/sub2api.git'
FORGEJO_IMAGE = 'git.134333.xyz/eezd/sub2api'  # 仓库路径，无 docker://、版本或摘要
GITHUB_OWNER = 'eezd'
GITHUB_REPO = 'sub2api'
GITHUB_GIT = 'https://github.com/eezd/sub2api.git'
GITHUB_PRIVATE = 'false'  # true / false
BRANCHES_JSON = r'''["main"]'''
BRANCHES = None  # 在 main 中解析已确认的分支范围
USE_LFS = 'false'  # true / false，接入时检查整个纳入同步的历史
FORGEJO_DOWNLOAD_HOSTS = ()  # 如有对象存储，填入已核实的 HTTPS authority；不发送令牌
GITHUB_DOWNLOAD_HOSTS = ('release-assets.githubusercontent.com', 'objects.githubusercontent.com')

VERSION = re.compile(r'v[0-9]+\.[0-9]+\.[0-9]+-custom\.[0-9]+')
SHA = re.compile(r'[0-9a-f]{40}')
HASH = re.compile(r'[0-9a-f]{64}')
PLATFORMS = ('linux/amd64', 'linux/arm64')
BUILD = 'build.json'
COMPLETE = 'release-complete.json'
SUMS = 'SHA256SUMS'
TOKENS = ('FORGEJO_RELEASE_TOKEN', 'FORGEJO_TOKEN', 'GITHUB_TOKEN', 'GITEA_TOKEN',
          'GITEA_RELEASE_TOKEN', 'GH_TOKEN', 'RELEASE_TOKEN', 'MIRROR_TOKEN')


# Keep sync self-contained like RawMark; release controllers still use delivery_ci/gitea_http.
# These run-scoped helpers retain Sub2API's explicit credentials and Forgejo edge transport.
def identity():
    values = [os.environ.get(key, '') for key in ('CI_RUN_ID', 'CI_RUN_ATTEMPT')]
    if not all(re.fullmatch(r'[1-9][0-9]*', value) for value in values):
        raise ValueError('Invalid CI run/attempt')
    return '-'.join(values)


def path_for(role):
    if role != 'sync':
        raise ValueError('Invalid delivery role')
    parent = Path(os.environ['RUNNER_TEMP']).resolve()
    return parent / f'sub2api-{role}-{identity()}'


def cleanup_auth(role):
    path = path_for(role)
    if path.is_symlink():
        raise ValueError('Refusing symlink auth cleanup')
    if path.exists():
        shutil.rmtree(path)


def child_env():
    environment = {key: value for key, value in os.environ.items() if key not in TOKENS and not key.startswith(
        ('GIT_CONFIG_KEY_', 'GIT_CONFIG_VALUE_')) and key not in (
        'GIT_DIR', 'GIT_WORK_TREE', 'GIT_COMMON_DIR', 'GIT_OBJECT_DIRECTORY',
        'GIT_ALTERNATE_OBJECT_DIRECTORIES', 'GIT_NAMESPACE', 'GIT_CONFIG_PARAMETERS', 'GIT_CONFIG_COUNT')}
    if os.environ.get('CI_GIT_HELPER'):
        environment.update(GIT_CONFIG_COUNT='3', GIT_CONFIG_KEY_0='credential.helper', GIT_CONFIG_VALUE_0='',
                           GIT_CONFIG_KEY_1='credential.helper', GIT_CONFIG_VALUE_1=os.environ['CI_GIT_HELPER'],
                           GIT_CONFIG_KEY_2='credential.useHttpPath', GIT_CONFIG_VALUE_2='true')
    return environment


@contextmanager
def credentials(role, tokens):
    if not tokens or not all(isinstance(value, str) and value.strip() for value in tokens.values()):
        raise ValueError('Missing Actions Secrets: RELEASE_TOKEN / MIRROR_TOKEN')
    path = path_for(role)
    path.mkdir(mode=0o700)  # Existing state is never adopted.
    old = dict(os.environ)
    try:
        home, xdg = path / 'home', path / 'xdg'
        home.mkdir(mode=0o700)
        xdg.mkdir(mode=0o700)
        credentials_file = path / 'credentials.json'
        credentials_file.write_text(json.dumps(tokens))
        credentials_file.chmod(0o600)
        helper = path / 'git-credentials.py'
        helper.write_text('''import json, sys
from pathlib import Path
if sys.argv[-1] != 'get':
    sys.exit(0)
fields = dict(line.rstrip('\\n').split('=', 1) for line in sys.stdin if '=' in line)
url = fields.get('protocol', '')+'://'+fields.get('host', '')+'/'+fields.get('path', '')
token = json.loads(Path(sys.argv[1]).read_text()).get(url)
if token:
    print('username=x-access-token\\npassword='+token)
''')
        config = path / 'gitconfig'
        config.write_text('[credential]\n\thelper =\n\tuseHttpPath = true\n\thelper = !' +
                          shlex.join([sys.executable, str(helper), str(credentials_file)]) + '\n' +
                          '[core]\n\thooksPath = /dev/null\n')
        config.chmod(0o600)
        os.environ.update(HOME=str(home), XDG_CONFIG_HOME=str(xdg), XDG_RUNTIME_DIR=str(xdg),
                          GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(config), GIT_TERMINAL_PROMPT='0',
                          CI_GIT_HELPER='!' + shlex.join([sys.executable, str(helper), str(credentials_file)]),
                          DOCKER_CONFIG=str(path / 'docker'), CI_AUTH_ROOT=str(path),
                          GIT_ASKPASS='/bin/false', SSH_ASKPASS='/bin/false')
        for key in list(os.environ):
            if key.startswith(('GIT_CONFIG_KEY_', 'GIT_CONFIG_VALUE_')) or key in (
                    'GIT_CONFIG_COUNT', 'GIT_CONFIG_PARAMETERS', 'REGISTRY_AUTH_FILE'):
                os.environ.pop(key)
        auth = path / 'registry.json'
        auth.write_text('{"auths":{}}\n')
        auth.chmod(0o600)
        yield str(auth)
    finally:
        os.environ.clear()
        os.environ.update(old)
        cleanup_auth(role)


def login(authfile, registry, username, token):
    subprocess.run(['skopeo', 'login', '--authfile', authfile, '--username', username, '--password-stdin', registry],
                   input=token, text=True, check=True, env=child_env())


@contextmanager
def anonymous():
    with tempfile.TemporaryDirectory(prefix='sub2api-anonymous-', dir=os.environ.get('CI_AUTH_ROOT') or os.environ.get('RUNNER_TEMP')) as temp:
        path = Path(temp)
        auth = path / 'auth.json'
        auth.write_text('{"auths":{}}\n')
        auth.chmod(0o600)
        old = dict(os.environ)
        try:
            os.environ.update(HOME=temp, XDG_CONFIG_HOME=temp, XDG_RUNTIME_DIR=temp,
                              DOCKER_CONFIG=temp, REGISTRY_AUTH_FILE=str(auth))
            for key in (*TOKENS, 'CI_GIT_HELPER'):
                os.environ.pop(key, None)
            yield str(auth)
        finally:
            os.environ.clear()
            os.environ.update(old)


def newest_main(records, head, ancestor):
    main = [row for row in records if row['source_branch'] == 'main']
    if any(not ancestor(row['source_sha'], head) for row in main):
        raise ValueError('Completed main history diverges from current main')
    if not main:
        return None
    for row in main:
        if all(ancestor(other['source_sha'], row['source_sha']) for other in main):
            return max((other for other in main if other['source_sha'] == row['source_sha']),
                       key=lambda other: tuple(map(int, re.findall(r'\d+', other['version']))))
    raise ValueError('Completed main releases diverge; latest is unchanged')


def delivery_history():
    value = json.loads(Path(__file__).with_name('delivery-history.json').read_bytes())
    if value['schema'] != 1:
        raise ValueError('Unknown delivery history schema')
    return value


def check_alias(raw, selected, records, ancestor, *, endpoint=None):
    if raw is None or raw == selected['image']['digest']:
        return
    known = [row for row in records if row.get('schema') == 5 and
             row['source_branch'] == 'main' and row['image']['digest'] == raw]
    if known and all(ancestor(row['source_sha'], selected['source_sha']) for row in known):
        return
    # Preserve the approved finite legacy-alias transition, not arbitrary adoption.
    if endpoint is not None and selected.get('schema') == 5 and selected['source_branch'] == 'main':
        transition = delivery_history()['latest_transition']
        cutoff = transition['after_commit']
        first_main = [row for row in records if row.get('schema') == 5 and
                      row['source_branch'] == 'main' and row['source_sha'] != cutoff and
                      ancestor(cutoff, row['source_sha'])]
        if (transition['aliases'].get(endpoint) == raw and len(first_main) == 1 and
                first_main[0] == selected and
                newest_main(records, selected['source_sha'], ancestor) == selected):
            return
    raise ValueError('Unknown or divergent latest alias; refusing rollback/adoption')


def validate_provenance(value):
    inputs = value['release_inputs']
    sha = re.compile(r'[0-9a-f]{40}')
    if not (value.get('schema') == 5 and isinstance(inputs, dict) and
            set(inputs) == {'source_branch', 'source_sha'} and
            all(isinstance(v, str) and v == v.strip() for v in inputs.values()) and
            inputs['source_branch'] == value['source_branch'] and inputs['source_branch'] and
            sha.fullmatch(value['source_sha']) and sha.fullmatch(value['source_branch_head']) and
            sha.fullmatch(value['control_sha']) and
            re.fullmatch(r'[1-9][0-9]*-[1-9][0-9]*', value['release_run_id']) and
            str(uuid.UUID(value['release_request_id'])) == value['release_request_id'] and
            (inputs['source_sha'] == value['source_sha'] if inputs['source_sha'] else
             value['source_sha'] == value['source_branch_head'])):
        raise ValueError('Invalid schema5 build provenance')


@contextmanager
def forgejo_request(url, authorization, *, method='GET', data=None, headers=None, timeout=30, limit=None):
    target = urllib.parse.urlsplit(url)
    if (target.scheme != 'https' or not target.hostname or target.username is not None or
            target.password is not None):
        raise ValueError('Forgejo request requires an HTTPS URL without credentials')
    if authorization is not None and not re.fullmatch(r'token [A-Za-z0-9_.-]+', authorization):
        raise ValueError('Invalid Forgejo token format')
    with tempfile.TemporaryDirectory(prefix='sub2api-http-') as scratch, tempfile.TemporaryFile() as body:
        directory = Path(scratch)
        header_path = directory / 'headers'
        command = ['curl', '-q', '--config', '-', '--noproxy', '*', '--globoff', '--proto', '=https',
                   '--silent', '--show-error', '--max-time', str(timeout), '--request', method,
                   '--dump-header', str(header_path)]
        if limit is not None:
            command += ['--max-filesize', str(limit)]
        for name, value in (headers or {}).items():
            command += ['--header', f'{name}: {value}']
        if data is not None:
            payload = directory / 'request'
            payload.write_bytes(data)
            command += ['--data-binary', '@' + str(payload)]
        env = {key: value for key, value in os.environ.items()
               if key not in ('GITEA_TOKEN', 'GITEA_RELEASE_TOKEN', 'FORGEJO_TOKEN', 'FORGEJO_RELEASE_TOKEN', 'GITHUB_TOKEN')}
        config = f'header = {json.dumps("Authorization: " + authorization)}\n' if authorization else ''
        result = subprocess.run([*command, '--url', url], input=config.encode(), stdout=body,
                                stderr=subprocess.PIPE, env=env)
        if result.returncode:
            raise OSError(f'curl exit {result.returncode}')
        blocks = header_path.read_bytes().split(b'\r\n\r\n')
        response = next((block for block in reversed(blocks) if block.startswith(b'HTTP/')), b'')
        status_line, _, fields = response.partition(b'\r\n')
        status = re.match(rb'^HTTP/\S+ ([1-5][0-9]{2})(?: |$)', status_line)
        if status is None:
            raise OSError('Missing HTTP status from curl')
        body.seek(0)
        yield int(status[1]), BytesHeaderParser().parsebytes(fields), body


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def load_token(service, variable):
    token = os.environ.get(variable, '')
    require(token.strip(), 'Missing Actions Secret: ' + variable)
    return token


def encode(value):
    return urllib.parse.quote(value, safe='')


def authority(url):
    parsed = urllib.parse.urlsplit(url)
    require(parsed.scheme == 'https' and parsed.hostname and parsed.username is None
            and parsed.password is None, 'API 或下载地址必须是无凭据的 HTTPS 地址')
    return parsed.netloc


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class API:
    def __init__(self, root, authorization, download_hosts=()):
        self.root = root
        self.host = authority(root)
        self.auth = authorization
        self.download_hosts = download_hosts
        self.opener = urllib.request.build_opener(NoRedirect)

    @contextmanager
    def open(self, url, phase, *, method='GET', data=None, headers=None, download=False):
        headers = {'Authorization': self.auth, 'User-Agent': 'forgejo-init-manual-sync',
                   'Accept': 'application/json', **(headers or {})}
        require(authority(url) == self.host or (method == 'POST' and
                self.host == 'api.github.com' and authority(url) == 'uploads.github.com'),
                phase + '：拒绝向非预期站点发送凭据')
        for _ in range(4):
            if self.host == authority(FORGEJO_API):
                auth = headers.get('Authorization')
                request_headers = {key: value for key, value in headers.items() if key != 'Authorization'}
                with ExitStack() as response_context:
                    try:
                        code, fields, body = response_context.enter_context(forgejo_request(
                            url, auth, method=method, data=data, headers=request_headers, timeout=180, limit=90_000_000))
                    except OSError:
                        raise RuntimeError(f'{phase}失败：网络或超时错误；未自动重试') from None
                    if 200 <= code < 300:
                        body.headers = fields
                        yield body
                        return
                    location = fields.get('Location')
            else:
                request = urllib.request.Request(url, data=data, headers=headers, method=method)
                try:
                    response = self.opener.open(request, timeout=60)
                except urllib.error.HTTPError as err:
                    code, location = err.code, err.headers.get('Location')
                    err.close()
                except OSError:
                    raise RuntimeError(f'{phase}失败：网络或超时错误；未自动重试') from None
                else:
                    with response:
                        yield response
                    return
            if download and code in (301, 302, 303, 307, 308) and location:
                url = urllib.parse.urljoin(url, location)
                require(authority(url) in (self.host, *self.download_hosts),
                        phase + '：拒绝未批准的下载跳转')
                headers.pop('Authorization', None)
                continue
            raise RuntimeError(f'{phase}失败：HTTP {code}；未自动重试') from None
        raise RuntimeError(phase + '：下载跳转过多')

    def json(self, path, phase, *, method='GET', payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        headers = {'Content-Type': 'application/json'} if data is not None else None
        with self.open(self.root + path, phase, method=method, data=data, headers=headers) as response:
            return json.load(response)

    def pages(self, path, phase, *, size_key):
        items = []
        ids = set()
        expected = None
        page = 1
        while True:
            url = f'{self.root}{path}?{size_key}=50&page={page}'
            with self.open(url, phase) as response:
                batch = json.load(response)
                link = response.headers.get('Link', '')
                total = response.headers.get('X-Total-Count')
            if total is not None:
                require(total.isdecimal() and (expected is None or expected == int(total)),
                        phase + '：分页总数变化')
                expected = int(total)
            require(isinstance(batch, list), phase + '：分页结果不是列表')
            if not batch:
                require('rel="next"' not in link and (expected is None or len(items) == expected),
                        phase + '：分页结果不完整')
                return items
            for item in batch:
                entry_id = item['id']
                require(type(entry_id) is int and entry_id > 0 and entry_id not in ids,
                        phase + '：分页 ID 无效或重复')
                ids.add(entry_id)
                items.append(item)
            if expected is not None:
                require(len(items) <= expected and (len(items) < expected or 'rel="next"' not in link),
                        phase + '：分页结果与总数冲突')
                if len(items) == expected:
                    return items
            page += 1

    def forgejo_asset(self, asset, path):
        url = urllib.parse.urljoin(self.root + '/', asset['browser_download_url'])
        return self.download(url, path, '下载 Forgejo 附件')

    def github_asset(self, asset, path):
        return self.download(f'{self.root}/releases/assets/{asset["id"]}', path, '下载 GitHub 附件')

    def download(self, url, path, phase):
        digest = hashlib.sha256()
        with self.open(url, phase, headers={'Accept': 'application/octet-stream'}, download=True) as response:
            if path is None:
                for block in iter(lambda: response.read(1024 * 1024), b''):
                    digest.update(block)
            else:
                with path.open('wb') as output:
                    for block in iter(lambda: response.read(1024 * 1024), b''):
                        digest.update(block)
                        output.write(block)
        return digest.hexdigest()

    def upload(self, release, name, path):
        url = release['upload_url'].removesuffix('{?name,label}')
        route = f'https://uploads.github.com{urllib.parse.urlsplit(self.root).path}/releases/{release["id"]}/assets'
        require(url.lower() == route.lower(), 'GitHub 附件上传地址不属于目标 Release')
        headers = {'Content-Type': 'application/octet-stream', 'Content-Length': str(path.stat().st_size)}
        with path.open('rb') as content, self.open(url + '?name=' + encode(name), '上传 GitHub 附件',
                                                    method='POST', data=content, headers=headers) as response:
            asset = json.load(response)
        require(asset.get('name') == name and asset.get('state') == 'uploaded' and
                type(asset.get('id')) is int and asset['id'] > 0, 'GitHub 附件上传响应不完整')
        return asset


def index(items, key, phase):
    result = {}
    for item in items:
        value = item[key]
        require(isinstance(value, str) and value and value not in result, phase + '：名称无效或重复')
        result[value] = item
    return result


def run(args, phase, *, cwd=None, allowed=(0,)):
    environment = child_env()
    environment.update(GIT_TERMINAL_PROMPT='0', LC_ALL='C')
    result = subprocess.run(args, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=environment)
    require(result.returncode in allowed, f'{phase}失败（退出码 {result.returncode}）')
    return result


class Snapshot:
    def __init__(self, directory, project=None):
        self.directory = directory
        self.git('init', '--bare', str(directory), phase='创建临时仓库')
        self.git('remote', 'add', 'source', FORGEJO_GIT)
        self.git('remote', 'add', 'destination', GITHUB_GIT)
        self.git('fetch', '--no-tags', 'source', 'refs/heads/*:refs/heads/*', 'refs/tags/*:refs/tags/*',
                 phase='读取 Forgejo 引用')
        self.refs = self.parse(self.git('for-each-ref', '--format=%(objectname)%09%(refname)',
                                       'refs/heads', 'refs/tags').stdout)
        self.heads = {r: sha for r, sha in self.refs.items() if r.startswith('refs/heads/') and
                      (BRANCHES == ['*'] or r.removeprefix('refs/heads/') in BRANCHES)}
        require('refs/heads/main' in self.heads, '同步范围或 Forgejo 快照缺少 main')

    def git(self, *args, phase='读取 Git 快照', allowed=(0,)):
        # 不执行宿主全局钩子；所有 GitHub 写入均在显式同步阶段执行。
        cwd = self.directory if self.directory.exists() else self.directory.parent
        return run(['git', '-c', 'core.hooksPath=/dev/null', '-c', 'push.followTags=false',
                    '-c', 'push.pushOption=', *args], phase, cwd=cwd, allowed=allowed)

    @staticmethod
    def parse(output):
        refs = {}
        for line in output.decode().splitlines():
            sha, ref = line.split('\t', 1)
            require(SHA.fullmatch(sha) and ref.startswith(('refs/heads/', 'refs/tags/')) and
                    ref not in refs, 'Git 返回无效或重复的引用')
            refs[ref] = sha
        return refs

    def target(self):
        return self.parse(self.git('ls-remote', '--refs', '--heads', '--tags', 'destination',
                                   phase='读取 GitHub 引用').stdout)

    def commit(self, ref):
        result = self.git('rev-parse', '--verify', '--quiet', ref + '^{commit}', allowed=(0, 1))
        return result.stdout.decode().strip() if result.returncode == 0 else None

    def ancestor(self, older, newer):
        result = self.git('cat-file', '-t', older, allowed=(0, 128))
        if result.stdout != b'commit\n':
            return False
        return self.git('merge-base', '--is-ancestor', older, newer, allowed=(0, 1)).returncode == 0

    def in_scope(self, ref):
        if BRANCHES == ['*']:
            return True
        commit = self.commit(ref)
        return commit is not None and any(self.ancestor(commit, tip) for tip in self.heads.values())

    def changes(self, refs, target):
        changes = []
        for ref, sha in sorted(refs.items()):
            old = target.get(ref)
            if old == sha:
                continue
            require(old is None or (ref.startswith('refs/heads/') and self.ancestor(old, sha)),
                    f'GitHub 分支分叉或 TAG 冲突：{ref}')
            changes.append(f'{ref}:{ref}')
        return changes

    def lfs(self, refs):
        require(shutil.which('git-lfs'), '缺少命令：git-lfs')
        self.git('lfs', 'fetch', '--all', 'source', *refs, phase='下载 Forgejo LFS 对象')
        self.git('lfs', 'push', '--all', 'destination', *refs, phase='补齐 GitHub LFS 对象')


def read_manifest(ref, authfile, *, allow_missing=False):
    # Skopeo 1.18 can return 2 for a missing manifest; the registry error still decides.
    result = run(['skopeo', 'inspect', '--raw', '--authfile', authfile, 'docker://' + ref],
                 '读取 OCI 清单', allowed=(0, 1, 2))
    if result.returncode != 0:
        # 首次复制时版本清单或整个包尚不存在；认证、限流等错误不视为缺失。
        if allow_missing and any(code in result.stderr.lower() for code in (b'manifest unknown', b'name unknown')):
            return None
        raise RuntimeError('读取 OCI 清单失败；请核对镜像、权限和 Registry 状态')
    return result.stdout


def verify_image(ref, image, authfile, *, allow_missing=False):
    raw = read_manifest(ref, authfile, allow_missing=allow_missing)
    if raw is None:
        return False
    digest = 'sha256:' + hashlib.sha256(raw).hexdigest()
    require(digest == image['digest'], 'OCI 顶层摘要冲突')
    manifests = json.loads(raw)['manifests']
    require(len(manifests) == 2, 'OCI requires exactly two platforms')
    for platform, expected in image['platforms'].items():
        system, arch = platform.split('/')
        matches = [m['digest'] for m in manifests if m.get('platform', {}).get('os') == system
                   and m.get('platform', {}).get('architecture') == arch]
        require(matches == [expected], 'OCI 架构摘要冲突：' + platform)
        repository = ref.rsplit('@', 1)[0] if '@' in ref else ref.rsplit(':', 1)[0]
        child = read_manifest(repository + '@' + expected, authfile)
        require('sha256:' + hashlib.sha256(child).hexdigest() == expected, 'OCI 子清单摘要冲突：' + platform)
    return True


def package_names(tag):
    return {f'{FORGEJO_REPO}_{tag}_{system}_{arch}.{extension}'
            for system, extension in (('linux', 'tar.gz'), ('darwin', 'tar.gz'), ('windows', 'zip'))
            for arch in ('amd64', 'arm64')}


def is_formal(release):
    return release['draft'] is False and release['prerelease'] is False


def auth_file():
    value = os.environ.get('CI_REGISTRY_AUTH')
    require(value, 'Missing explicit CI Registry authentication')
    return value


def verify_arch_tags(ref, image, authfile):
    for arch in ('amd64', 'arm64'):
        raw = read_manifest(ref + '-' + arch, authfile)
        require('sha256:' + hashlib.sha256(raw).hexdigest() == image['platforms']['linux/' + arch],
                'Forgejo architecture tag conflict: ' + arch)


def public_image(ref, image, authfile, *, arch_tags=False):
    if arch_tags:
        verify_arch_tags(ref, image, authfile)
    verify_image(ref, image, authfile)
    try:
        with anonymous() as empty:
            verify_image(ref, image, empty)
            if arch_tags:
                verify_arch_tags(ref, image, empty)
    except RuntimeError as error:
        if ref.startswith('ghcr.io/'):
            raise RuntimeError('GHCR package is not publicly readable. Open https://github.com/users/eezd/packages/container/sub2api/settings and set Package visibility to Public; rerun to fill gaps without rebuilding') from error
        raise


def verify_archives(source, target, snapshot, dest_refs, releases, target_releases):
    history = delivery_history()
    require(history['repositories'] == {'forgejo': FORGEJO_GIT, 'github': GITHUB_GIT},
            'Historical archive repository mismatch')
    archives = index(history['archives'], 'tag', 'Historical archive')
    assets_checked = 0
    bytes_checked = 0
    for tag, archive in archives.items():
        ref = 'refs/tags/' + tag
        require(snapshot.refs.get(ref) == dest_refs.get(ref) == archive['tag_object'] and
                snapshot.commit(ref) == archive['commit'], 'Historical archive TAG conflict: ' + tag)
        expected = index(archive['assets'], 'name', 'Historical archive attachments')
        for service, api, entries, size_key in (
                ('forgejo', source, releases, 'limit'), ('github', target, target_releases, 'per_page')):
            release = entries.get(tag)
            require(release is not None and is_formal(release) and
                    release['id'] == archive[service + '_id'] and release['name'] == archive['name'] and
                    hashlib.sha256(release['body'].encode()).hexdigest() == archive['body_sha256'],
                    f'Historical archive {service} Release conflict: {tag}')
            path = f'/releases/{release["id"]}/assets'
            # Forgejo 17 returns the full list and ignores pagination on this endpoint.
            entries = (api.json(path, '核对历史附件') if service == 'forgejo' else
                       api.pages(path, '核对历史附件', size_key=size_key))
            assets = index(entries, 'name', 'Historical archive attachments')
            require(assets.keys() == expected.keys(), f'Historical archive {service} attachment set conflict: {tag}')
            for name, frozen in expected.items():
                asset = assets[name]
                require(asset['id'] == frozen[service + '_id'] and asset['size'] == frozen['size'],
                        f'Historical archive {service} attachment identity conflict: {tag}/{name}')
                if service == 'forgejo':
                    require(asset['uuid'] == frozen['forgejo_uuid'] and
                            api.forgejo_asset(asset, None) == frozen['sha256'],
                            f'Historical archive Forgejo attachment digest conflict: {tag}/{name}')
                else:
                    require(asset.get('state') == 'uploaded' and asset.get('digest') == 'sha256:' + frozen['sha256'],
                            f'Historical archive GitHub attachment digest conflict: {tag}/{name}')
        assets_checked += len(expected)
        bytes_checked += sum(asset['size'] for asset in expected.values())
    print(f'历史只读档案已核对：{len(archives)} 个版本 / {assets_checked} 个附件 / {bytes_checked} 字节；不作为新完成证明', flush=True)
    return archives


def completed_metadata(tag, assets, path, source, snapshot):
    require({BUILD, COMPLETE} <= assets.keys(), '正式版本缺少发布元数据或验收证明')
    build_hash = source.forgejo_asset(assets[BUILD], path / BUILD)
    source.forgejo_asset(assets[COMPLETE], path / COMPLETE)
    build = json.loads((path / BUILD).read_bytes())
    completion = json.loads((path / COMPLETE).read_bytes())
    require(build.get('schema') in (None, 4, 5), 'Unknown completed build schema')
    if build.get('schema') == 5:
        validate_provenance(build)
    sha = build['source_sha']
    require(SHA.fullmatch(sha) and VERSION.fullmatch(tag) and
            snapshot.commit('refs/tags/' + tag) == sha and
            build['version'] == completion['version'] == tag and completion['source_sha'] == sha and
            completion == {'version': tag, 'source_sha': sha, 'build_sha256': build_hash},
            '验收记录、TAG、SHA 或 build.json 哈希不一致')
    require(isinstance(build['source_branch'], str) and build['source_branch'], '缺少发布来源分支')
    return build, build_hash


def skipped_completion_records(skipped, directory, source, snapshot):
    # Read minimal proofs for fork detection, never prepare these versions for sync.
    records = []
    with tempfile.TemporaryDirectory(prefix='latest-proof-', dir=directory) as temporary:
        for tag, release in skipped:
            assets = index(release['assets'], 'name', 'Forgejo 附件')
            if COMPLETE in assets:
                build, _ = completed_metadata(tag, assets, Path(temporary), source, snapshot)
                if build['source_branch'] == 'main':
                    records.append(build)
    return records


class Version:
    def __init__(self, tag, release, directory, source, target, snapshot, target_release):
        self.tag = tag
        self.path = directory / tag
        self.path.mkdir()
        self.target = target_release
        self.target_path = None
        self.title = release['name']
        self.body = release['body']
        require(isinstance(self.title, str) and isinstance(self.body, str), 'Forgejo Release 标题或正文无效')
        assets = index(release['assets'], 'name', 'Forgejo 附件')
        require({BUILD, COMPLETE, SUMS} <= assets.keys(), '正式版本缺少发布元数据或验收证明')
        build, build_hash = completed_metadata(tag, assets, self.path, source, snapshot)
        self.metadata = build
        self.sha = build['source_sha']
        hashes = index(build['packages'], 'name', 'build.json 包清单')
        require(hashes.keys() == package_names(tag) and assets.keys() == hashes.keys() | {BUILD, SUMS, COMPLETE}, '六平台附件清单不匹配')
        self.hashes = {name: package['sha256'] for name, package in hashes.items()}
        require(all(HASH.fullmatch(value) for value in self.hashes.values()), '附件 SHA256 格式无效')
        self.hashes[SUMS] = source.forgejo_asset(assets[SUMS], self.path / SUMS)
        sums = {}
        for line in (self.path / SUMS).read_text().splitlines():
            match = re.fullmatch(r'([0-9a-f]{64}) [ *](.+)', line)
            require(match and match[2] not in sums, 'SHA256SUMS 格式无效或条目重复')
            sums[match[2]] = match[1]
        require(sums == {name: self.hashes[name] for name in hashes}, 'SHA256SUMS 与 build.json 不一致')
        self.hashes[BUILD] = build_hash
        self.image = build['image']
        require(self.image['ref'] == FORGEJO_IMAGE + ':' + tag and
                self.image['platforms'].keys() == set(PLATFORMS) and all(
                    digest.startswith('sha256:') and HASH.fullmatch(digest[7:])
                    for digest in [self.image['digest'], *self.image['platforms'].values()]),
                'OCI 仓库、架构或摘要不符合固定发布契约')
        self.authfile = auth_file()
        self.source_image = FORGEJO_IMAGE + '@' + self.image['digest']
        self.target_image = f'ghcr.io/{GITHUB_OWNER.lower()}/{GITHUB_REPO.lower()}:{tag}'
        public_image(self.image['ref'], self.image, self.authfile, arch_tags=build.get('schema') == 5)
        self.image_exists = verify_image(self.target_image, self.image, self.authfile, allow_missing=True)
        self.missing = set(self.hashes)
        if self.target is not None:
            self.check_release(self.target)
            self.target_path = '/releases/' + str(self.target['id'])
            target_assets = index(target.pages(self.target_path + '/assets', '读取 GitHub 附件', size_key='per_page'),
                                  'name', 'GitHub 附件')
            require(target_assets.keys() <= self.hashes.keys(), 'GitHub Release 存在非预期附件')
            for name, asset in target_assets.items():
                require(asset.get('state') == 'uploaded' and
                        target.github_asset(asset, None) == self.hashes[name], 'GitHub 附件内容冲突：' + name)
                self.missing.remove(name)
        for name in sorted(hashes):
            file = self.path / name if name in self.missing else None
            require(source.forgejo_asset(assets[name], file) == self.hashes[name], 'Forgejo 附件 SHA256 不匹配：' + name)

    def check_release(self, release):
        require(release.get('tag_name') == self.tag and release.get('name') == self.title and
                release.get('body') == self.body and release.get('prerelease') is False and
                type(release.get('draft')) is bool, 'GitHub 同名 Release 身份、标题或正文冲突')

    def sync(self, target):
        release = self.target
        if release is None:
            release = target.json('/releases', '创建 GitHub 草稿', method='POST',
                                  payload={'tag_name': self.tag, 'target_commitish': self.sha, 'name': self.title,
                                           'body': self.body, 'draft': True, 'prerelease': False})
            self.check_release(release)
            require(release['draft'] is True, 'GitHub 未返回草稿状态')
            self.target_path = '/releases/' + str(release['id'])
        for name in sorted(self.missing):
            asset = target.upload(release, name, self.path / name)
            require(target.github_asset(asset, None) == self.hashes[name], 'GitHub 上传后 SHA256 不匹配：' + name)
            print(f'{self.tag}：已验证附件 {name}', flush=True)
        if not self.image_exists:
            run(['skopeo', 'copy', '--all', '--preserve-digests', '--authfile', self.authfile,
                 'docker://' + self.source_image, 'docker://' + self.target_image], '复制 GHCR 镜像')
            verify_image(self.target_image, self.image, self.authfile)
            print(f'{self.tag}：已验证 GHCR 多架构镜像', flush=True)
        public_image(self.target_image, self.image, self.authfile)
        if release['draft']:
            published = target.json(self.target_path, '发布 GitHub 草稿', method='PATCH',
                                    payload={'draft': False, 'make_latest': 'false'})
            self.check_release(published)
            require(is_formal(published), 'GitHub 未确认正式 Release')
        print(f'{self.tag}：GitHub Release 与镜像已验收', flush=True)


def latest_preflight(versions, snapshot, completion_records=None):
    records = [version.metadata for version in versions]
    chosen = newest_main(records, snapshot.heads['refs/heads/main'], snapshot.ancestor)
    if chosen is None or chosen.get('schema') != 5:
        return
    if completion_records is not None:
        records += completion_records()
        # Old schemas count for forks; only schema5 counts for the bootstrap window.
        newest_main(records, chosen['source_sha'], snapshot.ancestor)
    auth = auth_file()
    destinations = (FORGEJO_IMAGE + ':latest', f'ghcr.io/{GITHUB_OWNER}/{GITHUB_REPO}:latest')
    # Inspect *both* aliases before the first write: never partially adopt an unknown alias.
    current = [read_manifest(ref, auth, allow_missing=True) for ref in destinations]
    for ref, raw in zip(destinations, current):
        digest = 'sha256:' + hashlib.sha256(raw).hexdigest() if raw is not None else None
        check_alias(digest, chosen, records, snapshot.ancestor, endpoint=ref)
    return chosen, tuple(zip(destinations, current))


def sync_latest(versions, snapshot, completion_records=None):
    checked = latest_preflight(versions, snapshot, completion_records)
    if checked is None:
        return
    chosen, destinations = checked
    auth = auth_file()
    public_image(chosen['image']['ref'], chosen['image'], auth)
    for ref, raw in destinations:
        if raw is None or 'sha256:' + hashlib.sha256(raw).hexdigest() != chosen['image']['digest']:
            run(['skopeo', 'copy', '--multi-arch=index-only' if ref.startswith(FORGEJO_IMAGE) else '--all',
                 '--preserve-digests', '--authfile', auth,
                 'docker://' + FORGEJO_IMAGE + '@' + chosen['image']['digest'], 'docker://' + ref], '补齐 main latest')
        public_image(ref, chosen['image'], auth)


def sync(directory, source, target, project=None):
    settings = target.json('', '读取 GitHub 仓库')
    require(settings.get('full_name', '').lower() == f'{GITHUB_OWNER}/{GITHUB_REPO}'.lower() and
            settings.get('private') is (GITHUB_PRIVATE == 'true') and settings.get('archived') is False and
            settings.get('permissions', {}).get('push') is True, 'GitHub 目标、可见性或写权限与接入配置不符')
    require(target.json('/actions/permissions', '核对 GitHub Actions').get('enabled') is False,
            'GitHub Actions 尚未关闭，拒绝任何 GitHub 写入')
    # Forgejo 只向有 Release 写权限的令牌显示草稿；同时要求保留版本形状的裸 TAG 不外推。
    require(source.json('', '读取 Forgejo 仓库').get('permissions', {}).get('push') is True,
            '请使用可查看 Forgejo 草稿的仓库写权限令牌')
    snapshot = Snapshot(directory / 'git', project)
    dest_refs = snapshot.target()
    releases = index(source.pages('/releases', '读取 Forgejo Release', size_key='limit'), 'tag_name', 'Forgejo Release')
    target_releases = index(target.pages('/releases', '读取 GitHub Release', size_key='per_page'),
                            'tag_name', 'GitHub Release')
    archives = verify_archives(source, target, snapshot, dest_refs, releases, target_releases)
    refs = dict(snapshot.heads)
    versions = []
    gaps = []
    skipped = []
    for tag in sorted(releases):
        if tag in archives:
            continue  # Frozen imports are verified read-only, never repaired or promoted to Version.
        release = releases[tag]
        ref = 'refs/tags/' + tag
        if not is_formal(release):
            print(f'跳过草稿或预发布 TAG：{tag!r}', flush=True)
            continue
        if ref in snapshot.refs and not snapshot.in_scope(ref):
            skipped.append((tag, release))
            print(f'跳过未获准公开的 Release：{tag!r}', flush=True)
            continue
        asset_names = index(release['assets'], 'name', 'Forgejo 附件')
        if not VERSION.fullmatch(tag) or COMPLETE not in asset_names:
            gaps.append(tag)
            print(f'未同步历史或未验收正式版本（缺少适用完成证据）：{tag!r}', flush=True)
            continue
        print(f'{tag}：预检 Forgejo 原件与 GitHub 目标', flush=True)
        require(ref in snapshot.refs, 'Forgejo 正式版本缺少对应 TAG；请在发布结束后重跑')
        require(tag not in target_releases or dest_refs.get(ref) == snapshot.refs[ref],
                'GitHub 已有同名 Release，但 TAG 缺失或冲突：' + tag)
        versions.append(Version(tag, release, directory, source, target, snapshot, target_releases.get(tag)))
        refs[ref] = snapshot.refs[ref]
    for ref, sha in sorted(snapshot.refs.items()):
        if not ref.startswith('refs/tags/') or not snapshot.in_scope(ref):
            continue
        tag = ref.removeprefix('refs/tags/')
        if tag not in releases and not VERSION.fullmatch(tag):
            refs[ref] = sha
        elif tag not in releases:
            print(f'跳过尚无验收 Release 的版本 TAG：{tag!r}', flush=True)
    changes = snapshot.changes(refs, dest_refs)
    completion_records = lambda: skipped_completion_records(skipped, directory, source, snapshot)
    latest_preflight(versions, snapshot, completion_records)
    if USE_LFS == 'true':
        snapshot.lfs(sorted(refs))
    if changes:
        # 不请求强推、镜像或删除。保护规则拒绝个别引用时保留已推送项，重跑会重新核对。
        snapshot.git('push', '--no-force', '--no-mirror', '--no-prune', 'destination', *changes,
                     phase='快进推送 GitHub 引用')
    pushed = snapshot.target()
    require(all(pushed.get(ref) == sha for ref, sha in refs.items()), 'GitHub 引用校验失败')
    print(f'GitHub 引用已验收（更新 {len(changes)} 项）；继续补齐正式产物', flush=True)
    for version in versions:
        print(f'{version.tag}：开始同步正式产物', flush=True)
        version.sync(target)
    require(not gaps, f'仍有 {len(gaps)} 个正式版本未同步；需核实并补录原始发布证据')
    sync_latest(versions, snapshot, completion_records)
    print(f'GitHub 手动同步完成，已验收 {len(versions)} 个正式版本', flush=True)


def main(project):
    global BRANCHES
    values = (FORGEJO_API, FORGEJO_OWNER, FORGEJO_REPO, FORGEJO_GIT, FORGEJO_IMAGE, GITHUB_OWNER, GITHUB_REPO, GITHUB_GIT,
              BRANCHES_JSON)
    require(not any('@@' in value for value in values) and GITHUB_PRIVATE in ('true', 'false') and
            USE_LFS in ('true', 'false'), '同步模板尚未配置')
    BRANCHES = json.loads(BRANCHES_JSON)
    require(isinstance(BRANCHES, list) and all(isinstance(branch, str) and branch for branch in BRANCHES) and
            (BRANCHES == ['*'] or ('main' in BRANCHES and '*' not in BRANCHES)), '分支同步范围无效')
    require(all(re.fullmatch(r'[A-Za-z0-9_.-]+', name) and name not in ('.', '..')
                for name in (FORGEJO_OWNER, FORGEJO_REPO, GITHUB_OWNER, GITHUB_REPO)), '仓库名称配置无效')
    for url in (FORGEJO_GIT, GITHUB_GIT):
        require(not url.startswith('-') and '::' not in url, 'Git 远端地址配置无效')
        if url.startswith('https://'):
            require(not urllib.parse.urlsplit(url).query, 'Git URL 不得携带查询凭据')
            authority(url)
        else:
            require(url.startswith('ssh://') or re.fullmatch(r'[\w.-]+@[\w.-]+:[^\s]+', url),
                    'Git URL 必须使用配置好的 SSH 或 HTTPS')
            if url.startswith('ssh://'):
                require(urllib.parse.urlsplit(url).password is None, 'Git URL 不得携带密码')
    forgejo_token = load_token('Forgejo', 'FORGEJO_TOKEN')
    github_token = load_token('GitHub', 'GITHUB_TOKEN')
    source = API(f'{FORGEJO_API.rstrip("/")}/repos/{FORGEJO_OWNER}/{FORGEJO_REPO}',
                 'token ' + forgejo_token, FORGEJO_DOWNLOAD_HOSTS)
    target = API(f'https://api.github.com/repos/{GITHUB_OWNER}/{GITHUB_REPO}',
                 'Bearer ' + github_token, GITHUB_DOWNLOAD_HOSTS)
    require(shutil.which('git'), '缺少命令：git')
    require(os.environ.get('CONTROL_REF') == 'refs/heads/main', 'Sync must run from trusted main')
    with credentials('sync', {FORGEJO_GIT: forgejo_token, GITHUB_GIT: github_token}) as auth:
        with tempfile.TemporaryDirectory(prefix='sub2api-sync-', dir=Path(auth).parent) as directory:
            # Registry usernames are fixed repository owners, never discovered from a developer HOME.
            login(auth, 'git.134333.xyz', FORGEJO_OWNER, forgejo_token)
            login(auth, 'ghcr.io', GITHUB_OWNER, github_token)
            os.environ['CI_REGISTRY_AUTH'] = auth
            sync(Path(directory), source, target, project)


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--cleanup']:
            cleanup_auth('sync')
        elif not sys.argv[1:]:
            main(Path(__file__).resolve().parents[1])
        else:
            raise RuntimeError('Usage: sync-github.py [--cleanup]')
    except KeyboardInterrupt:
        print('同步中断；已写入的内容保留，核查后重跑以补齐缺口', file=sys.stderr)
        sys.exit(130)
    except RuntimeError as exc:
        print(f'同步失败：{exc}；已完成阶段见前文，核查后重跑', file=sys.stderr)
        sys.exit(1)
    except (OSError, http.client.HTTPException, ValueError, KeyError, TypeError, AttributeError, IndexError):
        print('同步失败：配置、响应数据或 I/O 异常；已完成阶段见前文，核查后重跑', file=sys.stderr)
        sys.exit(1)
