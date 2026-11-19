#!/usr/bin/env python3
"""Trusted Gitea release controller. Run only from the workflow on main."""

import base64
from collections import deque
import datetime as dt
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.parse
import uuid

from gitea_http import request as http_request
from release_build import IMAGE, LIMIT, ROOT, build, names, oci_image, run, sha

API = 'https://git.134333.xyz/api/v1'
REPO = 'eezd/sub2api'
WEB = API.removesuffix('/api/v1') + '/' + REPO
STATE = Path('/docker/gitea/mirrors/sub2api')
COMPLETE = 'release-complete.json'
HEX = re.compile(r'[0-9a-f]{40}')
VERSION = re.compile(r'\d{8}-[0-9a-f]{8}')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec='seconds').replace('+00:00', 'Z')


def write_json(path, data):
    temporary = path.with_suffix('.next')
    with temporary.open('w') as file:
        json.dump(data, file, sort_keys=True, indent=2)
        file.write('\n')
        file.flush()
        os.fsync(file.fileno())
    temporary.replace(path)
    directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def digest(data):
    return hashlib.sha256(data).hexdigest()


class Gitea:
    def __init__(self, token):
        require(token, 'Missing GITEA_RELEASE_TOKEN')
        self.token = token

    def request(self, path, method='GET', data=None, *, missing=False, raw=False, content_type='application/json'):
        url = path if path.startswith('https://') else API + path
        parsed = urllib.parse.urlsplit(url)
        require((parsed.scheme, parsed.netloc) == ('https', 'git.134333.xyz'), 'Unexpected authenticated Gitea origin')
        if data is not None and not isinstance(data, bytes):
            data = json.dumps(data).encode()
        headers = {'Content-Type': content_type, 'Accept': 'application/octet-stream' if raw else 'application/json'}
        try:
            with http_request(url, 'token ' + self.token, method=method, data=data, headers=headers,
                              timeout=180, limit=LIMIT) as (status, _, response):
                if missing and status == 404:
                    return None
                if not 200 <= status < 300:
                    raise RuntimeError(f'Gitea {method} {parsed.path}: HTTP {status}')
                result = response.read(LIMIT + 1)
        except OSError:
            raise RuntimeError(f'Gitea {method} {parsed.path}: network error; reconcile before retrying') from None
        require(len(result) <= LIMIT, 'Oversized Gitea response')
        return result if raw else json.loads(result) if result else None

    def repo(self, path='', *args, **kwargs):
        return self.request('/repos/' + REPO + path, *args, **kwargs)

    def release(self, version):
        # The list endpoint can explicitly query drafts even if lookups by tag hide them.
        release = self.repo('/releases/tags/' + version, missing=True)
        if release is not None:
            return release
        drafts = self.repo('/releases?draft=true&tag_filter=' + version)
        matches = [row for row in drafts if row['tag_name'] == version]
        require(len(matches) <= 1, f'Multiple Gitea releases for {version}')
        return matches[0] if matches else None

    def assets(self, release):
        rows = self.repo(f'/releases/{release["id"]}/assets?limit=50')
        assets = {row['name']: row for row in rows}
        require(len(assets) == len(rows), 'Duplicate Gitea release attachment')
        return assets

    def download(self, asset):
        return self.request(asset['browser_download_url'], raw=True)

    def upload(self, release, file):
        require(file.stat().st_size < LIMIT, f'Attachment exceeds upload budget: {file.name}')
        boundary = 'sub2api-' + uuid.uuid4().hex
        header = (f'--{boundary}\r\nContent-Disposition: form-data; name="attachment"; '
                  f'filename="{file.name}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()
        body = header + file.read_bytes() + f'\r\n--{boundary}--\r\n'.encode()
        self.repo(f'/releases/{release["id"]}/assets?name={urllib.parse.quote(file.name)}',
                  'POST', body, content_type=f'multipart/form-data; boundary={boundary}')

    def git(self, *args):
        auth = base64.b64encode(('x-access-token:' + self.token).encode()).decode()
        env = {**os.environ, 'GIT_TERMINAL_PROMPT': '0', 'GIT_CONFIG_COUNT': '2',
               'GIT_CONFIG_KEY_0': 'credential.helper', 'GIT_CONFIG_VALUE_0': '',
               'GIT_CONFIG_KEY_1': 'http.https://git.134333.xyz/.extraHeader',
               'GIT_CONFIG_VALUE_1': 'Authorization: Basic ' + auth}
        return run('git', '-C', str(ROOT), *args, env=env)


def git(*args):
    return run('git', '-C', str(ROOT), *args)


def baseline(records, branch, commit):
    past = [row for row in records if row['source_branch'] == branch]
    if not past:
        return None
    parents = {parts[0]: parts[1:] for line in git('rev-list', '--parents', commit).splitlines() if (parts := line.split())}
    pending = deque([(commit, 0)])
    distance = {commit: 0}
    while pending:
        node, depth = pending.popleft()
        for parent in parents[node]:
            if parent not in distance:
                distance[parent] = depth + 1
                pending.append((parent, depth + 1))
    candidates = [row for row in past if row['source_sha'] in distance]
    return min(candidates, key=lambda row: (distance[row['source_sha']],
               -dt.datetime.fromisoformat(row['completed_at']).timestamp(), row['version']), default=None)


def notes(previous, commit):
    if previous is None:
        return 'init'
    base = previous['source_sha']
    groups = {'Features': [], 'Bug Fixes': [], 'Other': []}
    for row in git('log', '--format=%ct%x00%H%x00%s', f'{base}..{commit}').splitlines() if base != commit else []:
        stamp, sha_, title = row.split('\0', 2)
        group = 'Features' if re.match(r'^feat(?:[(:!])', title) else 'Bug Fixes' if re.match(r'^fix(?:[(:!])', title) else 'Other'
        groups[group].append((int(stamp), sha_, title))
    sections = [f'## {group}\n\n' + '\n'.join(f'- {title} ([{sha_[:8]}]({WEB}/commit/{sha_}))'
                for _, sha_, title in sorted(items)) for group, items in groups.items() if items]
    sections.append(f'[Compare changes]({WEB}/compare/{base}...{commit})')
    return '\n\n'.join(sections)


def matches_release(release, row):
    version = row['version']
    require(release and release['tag_name'] == version and release['name'] == version and
            release['body'] == row['body'] and release['prerelease'] is False,
            f'Gitea Release conflicts with reserved version {version}')


def tag(api, row, *, create=False):
    version, commit = row['version'], row['source_sha']
    ref = 'refs/tags/' + version
    remote = api.git('ls-remote', WEB + '.git', ref, ref + '^{}')
    if not remote and create:
        api.repo('/tags', 'POST', {'tag_name': version, 'target': commit, 'message': 'Release ' + version})
        remote = api.git('ls-remote', WEB + '.git', ref, ref + '^{}')
    refs = dict(line.split('\t', 1)[::-1] for line in remote.splitlines())
    require(ref in refs and refs.get(ref + '^{}') == commit, f'Missing or conflicting annotated tag {version}')


def checkpoint(directory, row):
    files = set(names(row['version'])) | {'build.json', 'SHA256SUMS', 'image.oci'}
    proof = json.loads((directory / 'checkpoint.json').read_text())
    require(set(proof) == files and all(sha(directory / name) == proof[name] for name in files), 'Checkpoint hash mismatch')
    metadata = json.loads((directory / 'build.json').read_text())
    require(all(metadata[key] == row[key] for key in ('version', 'source_sha', 'source_branch', 'previous_tag', 'built_at')),
            'Checkpoint belongs to a different release')
    require({pkg['name']: pkg['sha256'] for pkg in metadata['packages']} == {name: proof[name] for name in names(row['version'])},
            'Invalid package manifest')
    require(metadata['image'] == oci_image(directory / 'image.oci', row['version']), 'Checkpoint OCI mismatch')
    return metadata, proof


def registry(authfile, image, directory=None):
    ref, expected = image['ref'], image['digest']

    def inspect(reference):
        result = subprocess.run(['skopeo', 'inspect', '--authfile', authfile, '--raw', 'docker://' + reference], capture_output=True)
        if result.returncode:
            if b'manifest unknown' in result.stderr.lower():
                return None
            raise RuntimeError('Cannot inspect Gitea Registry manifest')
        return result.stdout

    raw = inspect(ref)
    if raw is None and directory is not None:
        subprocess.run(['skopeo', 'copy', '--all', '--preserve-digests', '--dest-authfile', authfile,
                        'oci-archive:' + str(directory / 'image.oci'), 'docker://' + ref], check=True)
        raw = inspect(ref)
    require(raw is not None and 'sha256:' + digest(raw) == expected, 'Gitea Registry digest conflict')
    platforms = {f'{item["platform"]["os"]}/{item["platform"]["architecture"]}': item['digest']
                 for item in json.loads(raw)['manifests']}
    require(platforms == image['platforms'], 'Gitea Registry platforms differ')
    for value in platforms.values():
        leaf = inspect(IMAGE + '@' + value)
        require(leaf is not None and 'sha256:' + digest(leaf) == value, 'Gitea Registry platform manifest not readable')


def marker(row, build_sha):
    return {'version': row['version'], 'source_sha': row['source_sha'], 'build_sha256': build_sha}


def verify(api, release, row, proof, *, complete=False):
    matches_release(release, row)
    require(release['draft'] is False, 'Expected a formal Gitea Release')
    assets = api.assets(release)
    expected = set(proof) - {'image.oci'}
    require(expected <= assets.keys() <= expected | {COMPLETE}, 'Unexpected Gitea Release attachment set')
    for name in expected:
        require(digest(api.download(assets[name])) == proof[name], f'Gitea attachment digest differs: {name}')
    if complete or COMPLETE in assets:
        require(COMPLETE in assets and json.loads(api.download(assets[COMPLETE])) == marker(row, proof['build.json']),
                'Missing or conflicting Gitea completion record')


def pending(api, row, directory):
    if row.get('phase') == 'complete':
        return False
    if row.get('phase') != 'building' or directory.exists():
        return True
    release = api.release(row['version'])
    if release is not None:
        matches_release(release, row)
        return True
    return bool(api.git('ls-remote', WEB + '.git', 'refs/tags/' + row['version']))


def publish(api, authfile, row, directory, path):
    metadata, proof = checkpoint(directory, row)
    if row['phase'] == 'complete':
        tag(api, row)
        verify(api, api.release(row['version']), row, proof, complete=True)
        registry(authfile, metadata['image'])
        return
    row['phase'] = 'publishing'
    write_json(path, row)
    tag(api, row, create=True)
    release = api.release(row['version'])
    if not release:
        release = api.repo('/releases', 'POST', {'tag_name': row['version'], 'target_commitish': row['source_sha'],
                           'name': row['version'], 'body': row['body'], 'draft': True, 'prerelease': False})
    matches_release(release, row)
    assets = api.assets(release)
    expected = set(proof) - {'image.oci'}
    require(assets.keys() <= expected | {COMPLETE}, 'Unexpected existing release assets')
    require(not release['draft'] or COMPLETE not in assets, 'Draft unexpectedly has a completion record')
    # Validate all existing names before any new uploads.
    for name in assets.keys() & expected:
        require(digest(api.download(assets[name])) == proof[name], f'Existing Gitea asset conflicts: {name}')
    if release['draft']:
        for name in sorted(expected - assets.keys()):
            api.upload(release, directory / name)
    else:
        require(expected <= assets.keys(), 'Formal Release is missing original attachments')
    registry(authfile, metadata['image'], directory if release['draft'] else None)
    if release['draft']:
        release = api.repo(f'/releases/{release["id"]}', 'PATCH', {'draft': False})
    tag(api, row)
    verify(api, release, row, proof)
    if COMPLETE not in api.assets(release):
        with tempfile.TemporaryDirectory() as scratch:
            completion = Path(scratch) / COMPLETE
            write_json(completion, marker(row, proof['build.json']))
            api.upload(release, completion)
    verify(api, release, row, proof, complete=True)
    row.update(phase='complete', completed_at=utc_now())
    write_json(path, row)


def execute():
    branch, commit, run_id = (os.environ[key] for key in ('SOURCE_BRANCH', 'SOURCE_SHA', 'RELEASE_RUN_ID'))
    require(HEX.fullmatch(commit) and re.fullmatch(r'[0-9]+-[0-9]+', run_id), 'Invalid source SHA or run ID')
    require(not branch.startswith('-') and git('check-ref-format', '--branch', branch) == branch, 'Invalid source branch')
    require(os.environ.get('RELEASE_REF') == 'refs/heads/main', 'Release must run from trusted main')
    approval = STATE / 'dedicated-worker-approved'
    require(STATE.is_dir() and os.path.ismount(STATE) and approval.is_file(),
            'Dedicated release worker and its approved persistent mount are required')
    test_engine = ipaddress.IPv4Address(approval.read_text().strip())
    require(any(test_engine in ipaddress.IPv4Network(block) for block in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16')),
            'A dedicated integration Docker host on a private network is required')
    require(run('docker', 'info', '--format', '{{.Architecture}}') in ('x86_64', 'amd64'), 'Require a native Linux amd64 release worker')
    os.umask(0o077)
    api = Gitea(os.environ.get('GITEA_RELEASE_TOKEN'))
    identity = api.request('/user')
    repo = api.repo()
    require(identity.get('login') and repo.get('full_name') == REPO and repo.get('private') is True and
            repo.get('permissions', {}).get('push') is True, 'Release requires write access to the private Gitea primary')
    with (STATE / '.release.lock').open('a') as lock, tempfile.TemporaryDirectory() as authdir:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        branch_ref = 'refs/heads/' + branch
        api.git('fetch', '--no-tags', WEB + '.git', branch_ref)
        require(subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor', commit, 'FETCH_HEAD']).returncode == 0,
                'Fixed commit is no longer reachable from the source branch')
        date = dt.datetime.fromisoformat(git('show', '-s', '--format=%cI', commit))
        version = date.astimezone(dt.timezone.utc).strftime('%Y%m%d') + '-' + commit[:8]
        authfile = str(Path(authdir) / 'auth.json')
        subprocess.run(['skopeo', 'login', '--authfile', authfile, '--username', identity['login'], '--password-stdin',
                        'git.134333.xyz'], input=api.token, text=True, check=True)
        records = STATE / 'records'
        records.mkdir(exist_ok=True)
        path = records / (version + '.json')
        directory = STATE / version
        rows = [json.loads(file.read_text()) for file in sorted(records.glob('*.json'))]
        for row in rows:
            require(VERSION.fullmatch(row['version']), 'Malformed persisted release record')
            if row['version'] != version and pending(api, row, STATE / row['version']):
                raise RuntimeError('Recover pending version first: ' + row['version'])
        if path.exists():
            row = json.loads(path.read_text())
            require(row['source_sha'] == commit and row.get('phase') in ('building', 'publishing', 'complete'),
                    'Version collision or unrecognized persisted state')
        else:
            require(not directory.exists() and not api.release(version) and
                    not api.git('ls-remote', WEB + '.git', 'refs/tags/' + version), 'Untracked remote or local release state')
            candidates = [row for row in rows if row.get('phase') == 'complete' and row['source_branch'] == branch]
            for candidate in candidates:
                candidate_dir = STATE / candidate['version']
                build_sha = sha(candidate_dir / 'build.json')
                release = api.release(candidate['version'])
                matches_release(release, candidate)
                assets = api.assets(release)
                require(not release['draft'] and COMPLETE in assets and 'build.json' in assets and
                        json.loads(api.download(assets[COMPLETE])) == marker(candidate, build_sha) and
                        digest(api.download(assets['build.json'])) == build_sha, 'Prior release lacks a valid completion record')
            previous = baseline(candidates, branch, commit)
            row = {'version': version, 'source_sha': commit, 'source_branch': branch, 'built_at': utc_now(),
                   'previous_tag': previous['version'] if previous else None, 'body': notes(previous, commit), 'phase': 'building'}
            write_json(path, row)
        if not directory.exists():
            require(row['phase'] == 'building' and not pending(api, row, directory),
                    'Original release artifacts are missing; recovery requires the original checkpoint')
            with tempfile.TemporaryDirectory(prefix='.candidate-', dir=STATE) as scratch:
                staging = Path(scratch) / version
                staging.mkdir()
                build(staging, row, run_id, str(test_engine))
                proof = {name: sha(staging / name) for name in [*names(version), 'build.json', 'SHA256SUMS', 'image.oci']}
                for name in proof:
                    with (staging / name).open('rb') as file:
                        os.fsync(file.fileno())
                write_json(staging / 'checkpoint.json', proof)
                staging.rename(directory)
                fd = os.open(STATE, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    os.fsync(fd)
                finally:
                    os.close(fd)
        publish(api, authfile, row, directory, path)
        print('Verified Gitea release:', WEB + '/releases/tag/' + version, flush=True)


if __name__ == '__main__':
    try:
        execute()
    except (RuntimeError, ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f'Release stopped: {error}')
