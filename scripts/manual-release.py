#!/usr/bin/env python3
"""Trusted Forgejo release controller. Run only from the workflow on main."""

from collections import deque
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.parse
import uuid

from delivery_ci import credentials, cleanup_auth, child_env, login, anonymous, newest_main, check_alias, identity
from gitea_http import request as http_request
from release_build import IMAGE, LIMIT, ROOT, build, names, oci_image, run, sha

API = 'https://git.134333.xyz/api/v1'
REPO = 'eezd/sub2api'
WEB = API.removesuffix('/api/v1') + '/' + REPO
COMPLETE = 'release-complete.json'
HEX = re.compile(r'[0-9a-f]{40}')
SHA256 = re.compile(r'[0-9a-f]{64}')
VERSION = re.compile(r'v([0-9]+\.[0-9]+\.[0-9]+)-custom\.([0-9]+)')
# Update only after an explicitly approved integration of a new official upstream release.
UPSTREAM_TAG = 'v2.24.1'
UPSTREAM_COMMIT = '0755d5ef037f64d203828d0edcc513764b69e1d2'
WORKFLOW = 'release.yml'
LEGACY_WORKFLOW = 'sub2api-manual-release.yml'
CONTEXT_KEYS = ('source_branch_head', 'release_inputs', 'control_sha', 'release_run_id', 'release_request_id')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def packed(value):
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


def originals(version):
    return [*names(version), 'SHA256SUMS', 'build.json', 'image.oci']


def utc_now():
    return dt.datetime.now(dt.timezone.utc).isoformat(timespec='seconds').replace('+00:00', 'Z')


def digest(data):
    return hashlib.sha256(data).hexdigest()


class Forgejo:
    def __init__(self, token):
        require(token, 'Missing FORGEJO_RELEASE_TOKEN')
        self.token = token

    def request(self, path, method='GET', data=None, *, missing=False, raw=False,
                content_type='application/json'):
        url = path if path.startswith('https://') else API + path
        parsed = urllib.parse.urlsplit(url)
        require((parsed.scheme, parsed.netloc) == ('https', 'git.134333.xyz'), 'Unexpected authenticated Forgejo origin')
        if data is not None and not isinstance(data, bytes):
            data = json.dumps(data).encode()
        headers = {'Content-Type': content_type, 'Accept': 'application/octet-stream' if raw else 'application/json',
                   'User-Agent': 'sub2api-forgejo-release'}
        try:
            with http_request(url, 'token ' + self.token, method=method, data=data, headers=headers,
                              timeout=180, limit=LIMIT) as (status, _, response):
                if missing and status == 404:
                    return None
                if not 200 <= status < 300:
                    raise RuntimeError(f'Forgejo {method} {parsed.path}: HTTP {status}')
                result = response.read(LIMIT + 1)
        except OSError:
            raise RuntimeError(f'Forgejo {method} {parsed.path}: network error; reconcile before retrying') from None
        require(len(result) <= LIMIT, 'Oversized Forgejo response')
        return result if raw else json.loads(result) if result else None

    def pages(self, path):
        separator = '&' if '?' in path else '?'
        page = 1
        while rows := self.request(f'{path}{separator}limit=50&page={page}'):
            yield from rows
            page += 1

    def repo(self, path='', *args, **kwargs):
        return self.request('/repos/' + REPO + path, *args, **kwargs)

    def release(self, version):
        # The list endpoint can explicitly query drafts even if lookups by tag hide them.
        release = self.repo('/releases/tags/' + version, missing=True)
        if release is not None:
            return release
        drafts = self.repo('/releases?draft=true&tag_filter=' + version)
        matches = [row for row in drafts if row['tag_name'] == version]
        require(len(matches) <= 1, f'Multiple Forgejo releases for {version}')
        return matches[0] if matches else None

    def assets(self, release):
        rows = self.repo(f'/releases/{release["id"]}/assets')
        assets = {row['name']: row for row in rows}
        require(len(assets) == len(rows), 'Duplicate Forgejo release attachment')
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
        return run('git', '-C', str(ROOT), *args, env=child_env())


def git(*args):
    return run('git', '-C', str(ROOT), *args, env=child_env())


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
    require(candidates, f'No reachable release baseline for {branch}; confirm a new baseline before publishing')
    return min(candidates, key=lambda row: (distance[row['source_sha']],
               -dt.datetime.fromisoformat(row['completed_at']).timestamp(), row['version']))


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
            f'Forgejo Release conflicts with version {version}')


def tag(api, row, *, create=False):
    version, commit = row['version'], row['source_sha']
    ref = 'refs/tags/' + version
    remote = api.git('ls-remote', WEB + '.git', ref, ref + '^{}')
    if not remote and create:
        api.repo('/tags', 'POST', {'tag_name': version, 'target': commit, 'message': packed(attempt_identity(row)).decode().strip()})
        remote = api.git('ls-remote', WEB + '.git', ref, ref + '^{}')
    refs = dict(line.split('\t', 1)[::-1] for line in remote.splitlines())
    require(ref in refs and refs.get(ref + '^{}') == commit, f'Missing or conflicting annotated tag {version}')


def file_hashes(metadata):
    packages = {pkg['name']: pkg['sha256'] for pkg in metadata['packages']}
    require(len(packages) == len(metadata['packages']) and set(packages) == set(names(metadata['version'])) and
            all(SHA256.fullmatch(value) for value in packages.values()), 'Invalid package manifest')
    return packages


def image_metadata(metadata):
    image = metadata['image']
    require(image['ref'] == f'{IMAGE}:{metadata["version"]}' and
            set(image['platforms']) == {'linux/amd64', 'linux/arm64'} and
            all(value.startswith('sha256:') and SHA256.fullmatch(value[7:])
                for value in [image['digest'], *image['platforms'].values()]), 'Invalid Forgejo image identity')
    return image


def sums_bytes(packages):
    return ''.join(f'{packages[name]}  {name}\n' for name in sorted(packages)).encode()


def verify_sums(content, packages):
    require(sorted(content.splitlines()) == sorted(sums_bytes(packages).splitlines()), 'Invalid SHA256SUMS')


def verify_artifacts(directory, row, proof):
    require(set(proof) == set(originals(row['version'])) and
            all(sha(directory / name) == value for name, value in proof.items()), 'Build artifact hash mismatch')
    metadata = json.loads((directory / 'build.json').read_text())
    require(all(metadata[key] == row[key] for key in ('version', 'source_sha', 'source_branch', 'previous_tag', 'built_at')),
            'Build belongs to a different release')
    if row.get('schema') == 5:
        require(metadata.get('schema') == 5 and all(metadata.get(key) == row[key] for key in CONTEXT_KEYS),
                'Build context differs from current Actions run')
        validate_context(metadata)
    packages = file_hashes(metadata)
    require(all(proof[name] == value for name, value in packages.items()), 'Invalid package manifest')
    verify_sums((directory / 'SHA256SUMS').read_bytes(), packages)
    require(image_metadata(metadata) == oci_image(directory / 'image.oci', row['version']), 'Build OCI mismatch')
    return metadata


def inspect_registry(authfile, reference):
    result = subprocess.run(['skopeo', 'inspect', '--authfile', authfile, '--raw', 'docker://' + reference],
                            capture_output=True, env=child_env())
    if result.returncode:
        if result.returncode in (1, 2) and any(code in result.stderr.lower() for code in
                                               (b'manifest unknown', b'name unknown')):
            return None
        raise RuntimeError('Cannot inspect Forgejo Registry manifest')
    return result.stdout


def registry(authfile, image, directory=None):
    ref, expected = image['ref'], image['digest']
    raw = inspect_registry(authfile, ref)
    if directory is not None:
        # Publish children first. Never let --all implicitly create an incomplete version tag.
        for arch in ('amd64', 'arm64'):
            arch_ref = ref + '-' + arch
            wanted = image['platforms']['linux/' + arch]
            leaf = inspect_registry(authfile, arch_ref)
            if leaf is None:
                subprocess.run(['skopeo', 'copy', '--preserve-digests', '--override-os', 'linux',
                                '--override-arch', arch, '--dest-authfile', authfile,
                                'oci-archive:' + str(directory / 'image.oci'), 'docker://' + arch_ref],
                               check=True, env=child_env())
                leaf = inspect_registry(authfile, arch_ref)
            require(leaf is not None and 'sha256:' + digest(leaf) == wanted, 'Forgejo architecture tag conflict')
        if raw is None:
            subprocess.run(['skopeo', 'copy', '--multi-arch=index-only', '--preserve-digests',
                            '--dest-authfile', authfile, 'oci-archive:' + str(directory / 'image.oci'),
                            'docker://' + ref], check=True, env=child_env())
            raw = inspect_registry(authfile, ref)
    require(raw is not None and 'sha256:' + digest(raw) == expected, 'Forgejo Registry digest conflict')
    manifests = json.loads(raw)['manifests']
    platforms = {f'{item["platform"]["os"]}/{item["platform"]["architecture"]}': item['digest'] for item in manifests}
    require(len(manifests) == 2 and platforms == image['platforms'], 'Forgejo Registry platforms differ')
    for value in platforms.values():
        leaf = inspect_registry(authfile, IMAGE + '@' + value)
        require(leaf is not None and 'sha256:' + digest(leaf) == value, 'Forgejo Registry platform manifest not readable')


def public_registry(authfile, image, *, arch_tags=False):
    registry(authfile, image)
    with anonymous() as empty:
        registry(empty, image)
        if arch_tags:
            for arch in ('amd64', 'arm64'):
                for auth in (authfile, empty):
                    raw = inspect_registry(auth, image['ref'] + '-' + arch)
                    require(raw is not None and 'sha256:' + digest(raw) == image['platforms']['linux/' + arch],
                            'Forgejo architecture tag not publicly readable')


def latest(api, authfile):
    history, _ = release_history(api)
    records = [row for _, row, _ in history.values()]
    api.git('fetch', '--no-tags', WEB + '.git', 'refs/heads/main')
    def ancestor(older, newer):
        return subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor', older, newer],
                              env=child_env()).returncode == 0
    chosen = newest_main(records, git('rev-parse', 'FETCH_HEAD'), ancestor)
    if chosen is None:
        return
    # Historical completed versions are reusable, but do not manufacture new aliases for imports.
    if chosen.get('schema') != 5:
        return
    for version, (release, row, proof) in history.items():
        if version == chosen['version']:
            verify(api, release, row, proof, complete=True)
    public_registry(authfile, chosen['image'], arch_tags=True)
    ref = IMAGE + ':latest'
    raw = inspect_registry(authfile, ref)
    check_alias('sha256:' + digest(raw) if raw is not None else None, chosen, records, ancestor, endpoint=ref)
    if raw is None or 'sha256:' + digest(raw) != chosen['image']['digest']:
        subprocess.run(['skopeo', 'copy', '--multi-arch=index-only', '--preserve-digests', '--authfile', authfile,
                        'docker://' + IMAGE + '@' + chosen['image']['digest'], 'docker://' + ref],
                       check=True, env=child_env())
    public_registry(authfile, dict(chosen['image'], ref=ref))


def marker(row, build_sha):
    return {'version': row['version'], 'source_sha': row['source_sha'], 'build_sha256': build_sha}


def verify(api, release, row, proof, *, complete=False, draft=False):
    matches_release(release, row)
    require(release['draft'] is draft, 'Unexpected Forgejo Release draft state')
    assets = api.assets(release)
    expected = set(proof) - {'image.oci'}
    require(expected <= assets.keys() <= expected | {COMPLETE}, 'Unexpected Forgejo Release attachment set')
    for name in expected:
        require(digest(api.download(assets[name])) == proof[name], f'Forgejo attachment digest differs: {name}')
    if complete or COMPLETE in assets:
        require(COMPLETE in assets and json.loads(api.download(assets[COMPLETE])) == marker(row, proof['build.json']),
                'Missing or conflicting Forgejo completion record')


def publish(api, authfile, row, directory, proof):
    metadata = verify_artifacts(directory, row, proof)
    tag(api, row, create=True)
    require(api.release(row['version']) is None, 'Release appeared while building; stop without overwriting')
    release = api.repo('/releases', 'POST', {'tag_name': row['version'], 'target_commitish': row['source_sha'],
                       'name': row['version'], 'body': row['body'], 'draft': True, 'prerelease': False})
    matches_release(release, row)
    require(release['draft'] is True and not api.assets(release), 'Expected a new empty draft')
    for name in sorted(set(proof) - {'image.oci'}):
        api.upload(release, directory / name)
    registry(authfile, metadata['image'], directory)
    public_registry(authfile, metadata['image'], arch_tags=True)
    verify(api, release, row, proof, draft=True)
    release = api.repo(f'/releases/{release["id"]}', 'PATCH', {'draft': False})
    tag(api, row)
    verify(api, release, row, proof)
    completion = directory / COMPLETE
    completion.write_bytes(packed(marker(row, proof['build.json'])))
    api.upload(release, completion)
    verify(api, release, row, proof, complete=True)


def context():
    value = json.loads(Path(os.environ['RELEASE_CONTEXT']).read_bytes())
    validate_context(value)
    return value


def validate_context(value):
    import importlib.util
    spec = importlib.util.spec_from_file_location('resolve_release', Path(__file__).with_name('resolve-release.py'))
    resolver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(resolver)
    inputs = resolver.normalize_inputs(value['release_inputs'])
    require(inputs == value['release_inputs'] and inputs['source_branch'] == value['source_branch'] and
            HEX.fullmatch(value['source_branch_head']) and HEX.fullmatch(value['source_sha']) and
            (not inputs['source_sha'] or inputs['source_sha'] == value['source_sha']) and
            (inputs['source_sha'] or value['source_sha'] == value['source_branch_head']) and
            value['control_sha'] == os.environ.get('CONTROL_SHA') and HEX.fullmatch(value['control_sha']) and
            value['release_run_id'] == identity() and
            str(uuid.UUID(value['release_request_id'])) == value['release_request_id'] and
            os.environ.get('CONTROL_REF') == 'refs/heads/main', 'Invalid current Actions release context')


def release_inputs():
    value = context()
    return value['source_branch'], value['source_sha'], value['release_run_id']


def worker_build_engine():
    host = urllib.parse.urlsplit(os.environ.get('DOCKER_HOST', ''))
    require(host.scheme == 'tcp' and host.hostname and host.port == 2376 and
            not host.username and not host.password and not host.path and not host.query and not host.fragment,
            'Require an explicit remote TLS Docker connection on port 2376')
    require(os.environ.get('DOCKER_TLS_VERIFY') == '1' and os.environ.get('DOCKER_CERT_PATH'),
            'Require DOCKER_TLS_VERIFY=1 and DOCKER_CERT_PATH from the Runner')
    require(not os.environ.get('DOCKER_CONTEXT'), 'DOCKER_CONTEXT must not override the Runner TLS connection')
    # Preserve the Runner connection and its certificates; never fall back to a local socket.
    require(run('docker', 'info', '--format', '{{.OSType}}/{{.Architecture}}') in ('linux/x86_64', 'linux/amd64'),
            'Require a native Linux amd64 build engine')


def require_ancestor(commit, ref):
    require(subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor', commit, ref], env=child_env()).returncode == 0,
            'Fixed commit is no longer reachable from the source branch')


def version_for(versions):
    numbers = [int(match[2]) for version in versions if (match := VERSION.fullmatch(version))
               and 'v' + match[1] == UPSTREAM_TAG]
    return f'{UPSTREAM_TAG}-custom.{max(numbers, default=0) + 1}'


def verify_build():
    branch, commit, run_id = release_inputs()
    # Full-history checkout supplies the branch snapshot without release credentials.
    require_ancestor(commit, git('show-ref', '--verify', '--hash', 'refs/remotes/origin/' + branch))
    require_ancestor(UPSTREAM_COMMIT, commit)
    worker_build_engine()
    version = version_for([])  # Verification-only package label; no counter is queried or consumed.
    row = {'version': version, 'source_sha': commit, 'source_branch': branch,
           'built_at': utc_now(), 'previous_tag': None}
    with tempfile.TemporaryDirectory(prefix='sub2api-verify-') as scratch:
        build(Path(scratch), row, run_id)
    print(f'Build verified (verification-only label, not allocated): {version} ({commit}); nothing published', flush=True)


def native_tags(api):
    result = set()
    for line in api.git('ls-remote', '--tags', '--refs', WEB + '.git').splitlines():
        version = line.split('\t', 1)[1].removeprefix('refs/tags/')
        if VERSION.fullmatch(version):
            result.add(version)
    return result


def release_history(api):
    accepted, unfinished = {}, native_tags(api)
    seen = set()
    # A writer sees drafts by default; the optional booleans would filter out other states.
    path = '/repos/' + REPO + '/releases'
    for release in api.pages(path):
        version = release['tag_name']
        if not VERSION.fullmatch(version):
            continue  # Imported historical distribution tags are not Forgejo baselines.
        require(version not in seen, f'Duplicate Forgejo Release for {version}')
        seen.add(version)
        assets = api.assets(release)
        if COMPLETE not in assets:
            unfinished.add(version)
            continue
        require(release['draft'] is False and release['prerelease'] is False,
                f'Completion record on non-final Release: {version}')
        expected = set(names(version)) | {'build.json', 'SHA256SUMS', COMPLETE}
        require(assets.keys() == expected, f'Completed Release has unexpected attachments: {version}')
        build_bytes = api.download(assets['build.json'])
        metadata = json.loads(build_bytes)
        row = dict(metadata, body=release['body'], completed_at=release['published_at'])
        validate_metadata(row, version)
        require(dt.datetime.fromisoformat(row['completed_at']).tzinfo is not None,
                f'Invalid Forgejo Release timestamp: {version}')
        matches_release(release, row)
        require(json.loads(api.download(assets[COMPLETE])) == marker(row, digest(build_bytes)),
                f'Invalid Forgejo completion record: {version}')
        sums = api.download(assets['SHA256SUMS'])
        proof = file_hashes(metadata)
        image_metadata(metadata)
        verify_sums(sums, proof)
        proof.update({'build.json': digest(build_bytes), 'SHA256SUMS': digest(sums)})
        tag(api, row)
        accepted[version] = (release, row, proof)
        unfinished.discard(version)
    return accepted, unfinished


def validate_metadata(row, version):
    require(row.get('schema') in (None, 4, 5), 'Unknown completed build schema')
    if row.get('schema') == 5:
        from delivery_ci import validate_provenance
        validate_provenance(row)
    require(row['version'] == version and HEX.fullmatch(row['source_sha']) and
            VERSION.fullmatch(version) and row['source_branch'] and
            (row['previous_tag'] is None or VERSION.fullmatch(row['previous_tag'])) and
            isinstance(row['body'], str) and dt.datetime.fromisoformat(row['built_at']).tzinfo is not None,
            f'Malformed Forgejo build metadata: {version}')


def attempt_identity(row):
    keys = ('version', 'source_sha', 'source_branch', 'run_id', 'request_id', 'control_sha', 'actor_id')
    if row.get('schema') == 5:
        keys += ('schema', *CONTEXT_KEYS)
    return {key: row[key] for key in keys}


def failed_attempt(api, row, current_run):
    tag(api, row)
    annotation = api.repo('/tags/' + row['version'])
    owner = json.loads(annotation['message'])
    require(annotation['name'] == row['version'] and annotation['commit']['sha'] == row['source_sha'] and
            owner['version'] == row['version'] and owner['source_sha'] == row['source_sha'] and
            owner['source_branch'] == row['source_branch'] and HEX.fullmatch(owner['control_sha']) and
            re.fullmatch(r'[0-9]+-[0-9]+', owner['run_id']) and
            str(uuid.UUID(owner['request_id'])) == owner['request_id'], 'Unknown unfinished version ownership')
    old_id = int(owner['run_id'].split('-')[0])
    require(old_id != int(current_run.split('-')[0]), 'Cannot clean up an active or rerun task')
    previous = api.repo('/actions/runs/' + str(old_id))
    payload = json.loads(previous['event_payload'])
    workflow = WORKFLOW if owner.get('schema') == 5 else LEGACY_WORKFLOW
    expected_inputs = owner.get('release_inputs') if owner.get('schema') == 5 else {
        'source_branch': owner['source_branch'], 'source_sha': owner['source_sha'], 'request_id': owner['request_id']}
    require(owner.get('schema') in (None, 4, 5), 'Unknown unfinished build schema')
    if owner.get('schema') == 5:
        from delivery_ci import validate_provenance
        validate_provenance(owner)
        import importlib.util
        spec = importlib.util.spec_from_file_location('resolver', Path(__file__).with_name('resolve-release.py'))
        resolver = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(resolver)
        require(resolver.normalize_inputs(expected_inputs) == expected_inputs and
                expected_inputs['source_branch'] == owner['source_branch'] and
                (not expected_inputs['source_sha'] or expected_inputs['source_sha'] == owner['source_sha']) and
                owner['release_run_id'] == owner['run_id'] and owner['release_request_id'] == owner['request_id'] and
                HEX.fullmatch(owner['source_branch_head']), 'Invalid schema5 attempt context')
    event_inputs = resolver.normalize_inputs(payload['inputs']) if owner.get('schema') == 5 else payload['inputs']
    require(previous['id'] == old_id and previous['repository']['full_name'] == REPO and
            previous['workflow_id'] == workflow and previous['event'] == 'workflow_dispatch' and
            previous['commit_sha'] == owner['control_sha'] and payload['ref'] == 'refs/heads/main' and
            event_inputs == expected_inputs, 'Original Actions run identity differs')
    require(previous['status'] in ('failure', 'cancelled', 'skipped') and previous.get('stopped'),
            'Original Actions run has not terminated unsuccessfully')
    require(type(owner['actor_id']) is int and owner['actor_id'] > 0, 'Unknown release credential identity')
    ensure_no_other_runs(api, current_run)
    return owner


def ensure_no_other_runs(api, current_run):
    page = 1
    seen = set()
    total = None
    while True:
        response = api.repo(f'/actions/runs?limit=50&page={page}')
        count = response['total_count']
        require(type(count) is int and count >= 1 and (total is None or count == total),
                'Unknown or changing Actions run pagination')
        total = count
        rows = response['workflow_runs']
        require(isinstance(rows, list), 'Unknown Actions run listing')
        for entry in rows:
            require(type(entry['id']) is int and entry['id'] not in seen and entry['workflow_id'] in (WORKFLOW, LEGACY_WORKFLOW, 'sync-github.yml') and
                    entry['repository']['full_name'] == REPO, 'Unknown or repeated Actions run identity')
            seen.add(entry['id'])
            if entry['id'] != int(current_run.split('-')[0]):
                require(entry['status'] in ('success', 'failure', 'cancelled', 'skipped') and entry.get('stopped'),
                        'Another release task may still be writing; no cleanup allowed')
        if not rows:
            require(len(seen) == total, 'Incomplete Actions run pagination; no cleanup allowed')
            require(int(current_run.split('-')[0]) in seen, 'Current Actions run missing from listing; no cleanup allowed')
            break
        page += 1


def unfinished_snapshot(api, row, run_id):
    owner = failed_attempt(api, row, run_id)
    release = api.release(row['version'])
    assets = api.assets(release) if release is not None else {}
    packages = {suffix: api.request(package_path(row['version'] + suffix), missing=True)
                for suffix in ('', '-amd64', '-arm64')}
    raw = []
    expected_image = None
    if 'build.json' in assets:
        expected_image = image_metadata(json.loads(api.download(assets['build.json'])))
    for suffix, package in packages.items():
        manifest = inspect_registry(os.environ.get('CI_REGISTRY_AUTH', ''), f'{IMAGE}:{row["version"]}{suffix}')
        value = 'sha256:' + digest(manifest) if manifest is not None else None
        wanted = expected_image and (expected_image['platforms']['linux/' + suffix[1:]] if suffix else expected_image['digest'])
        require(value is None or (package is not None and wanted == value), 'Unknown unfinished Registry digest')
        require(package is None or wanted is not None, 'Unknown unfinished image metadata')
        if package is not None:
            require(package['name'] == REPO.split('/')[1] and package['version'] == row['version'] + suffix and
                    package['type'] == 'container' and package['owner']['login'] == REPO.split('/')[0] and
                    package['creator']['id'] == owner['actor_id'], 'Unknown unfinished image identity')
        raw.append(value)
    def fields(value, keys):
        return {key: value[key] for key in keys if key in value} if value is not None else None

    release_identity = fields(release, ('id', 'uuid', 'tag_name', 'name', 'body', 'draft', 'prerelease'))
    if release_identity is not None:
        release_identity['author'] = fields(release.get('author'), ('id',))
    package_identities = {}
    for suffix, package in packages.items():
        identity = fields(package, ('id', 'name', 'version', 'type'))
        if identity is not None:
            identity['owner'] = fields(package.get('owner'), ('id', 'login'))
            identity['creator'] = fields(package.get('creator'), ('id',))
        package_identities[suffix] = identity
    annotation = api.repo('/tags/' + row['version'])
    tag_identity = fields(annotation, ('id', 'name', 'message'))
    tag_identity['commit'] = fields(annotation.get('commit'), ('sha',))
    fingerprint = {'release': release_identity,
                   'assets': {name: {'identity': fields(asset, ('id', 'uuid', 'name', 'type', 'content_type',
                                                               'size', 'sha256', 'digest')),
                                     'hash': digest(api.download(asset))} for name, asset in assets.items()},
                   'tag': tag_identity,
                   'tag_refs': sorted(api.git('ls-remote', WEB + '.git', 'refs/tags/' + row['version'],
                                             'refs/tags/' + row['version'] + '^{}').splitlines()),
                   'packages': package_identities, 'digests': raw, 'owner': attempt_identity(owner)}
    return fingerprint


def validate_unfinished(api, row, run_id, release, snapshot):
    require(unfinished_snapshot(api, row, run_id) == snapshot,
            "Unfinished release changed while building; no deletion allowed")
    previous = failed_attempt(api, row, run_id)
    if release is not None:
        require(release['tag_name'] == release['name'] == row['version'] and
                release['prerelease'] is False and release['author']['id'] == previous['actor_id'],
                'Unknown unfinished Release identity')
        assets = api.assets(release)
        require(COMPLETE not in assets and assets.keys() <= set(originals(row['version'])) - {'image.oci'},
                'Unknown or completed Release must not be deleted')
        if 'build.json' in assets:
            metadata = json.loads(api.download(assets['build.json']))
            require(all(metadata[key] == row[key] for key in ('version', 'source_sha', 'source_branch')),
                    'Unfinished build metadata conflicts')
            if previous.get('schema') == 5:
                require(metadata.get('schema') == 5 and all(metadata.get(key) == previous[key]
                        for key in CONTEXT_KEYS), 'Unfinished build provenance conflicts')
            file_hashes(metadata)
            image_metadata(metadata)


def clean_unfinished(api, row, run_id, release, package, snapshot):
    validate_unfinished(api, row, run_id, release, snapshot)
    # All identities and all three tags are checked before the first DELETE.
    if release is not None:
        api.repo(f'/releases/{release["id"]}', 'DELETE')
    for suffix, old_package in snapshot['packages'].items():
        if old_package is not None:
            api.request(package_path(row['version'] + suffix), 'DELETE')
    require(api.release(row['version']) is None and all(
            api.request(package_path(row['version'] + suffix), missing=True) is None
            for suffix in ('', '-amd64', '-arm64')), 'Exact cleanup not confirmed')


def package_path(version):
    owner, name = REPO.split('/')
    return f'/packages/{owner}/container/{name}/{version}'


def continue_release(api, authfile, branch, commit, run_id):
    history, unfinished = release_history(api)
    completed = [record for record in history.values() if record[1]['source_sha'] == commit]
    require(len(completed) <= 1, 'Multiple completed releases for the same full source SHA')
    if completed:
        release, row, proof = completed[0]
        verify(api, release, row, proof, complete=True)
        public_registry(authfile, row['image'], arch_tags=row.get('schema') == 5)
        if branch == row['source_branch'] == 'main':
            latest(api, authfile)
        return row['version']
    # Keep every failed tag in the counter even if its residual artifacts are removed below.
    version = version_for(set(history) | unfinished)
    pending_cleanup = []
    for old_version in sorted(unfinished):
        annotation = api.repo('/tags/' + old_version)
        if annotation['commit']['sha'] != commit:
            continue
        old_release = api.release(old_version)
        package = api.request(package_path(old_version), missing=True)
        arch_packages = [api.request(package_path(old_version + '-' + arch), missing=True) for arch in ('amd64', 'arm64')]
        if old_release is None and package is None and not any(arch_packages):
            continue  # A bare failed tag consumes its number, but has nothing to delete.
        owner = json.loads(annotation['message'])
        old_row = {'version': old_version, 'source_sha': commit, 'source_branch': owner['source_branch']}
        snapshot = unfinished_snapshot(api, old_row, run_id)
        pending_cleanup.append((old_row, old_release, package, snapshot))
    row = {'version': version, 'source_sha': commit, 'source_branch': branch}
    require(api.release(version) is None and api.request(package_path(version), missing=True) is None,
            'New version already has untracked release artifacts')
    require(all(inspect_registry(authfile, f'{IMAGE}:{version}{suffix}') is None for suffix in ('', '-amd64', '-arm64')),
            'Untracked Registry version')
    previous = baseline([record for _, record, _ in history.values()], branch, commit)
    value = context()
    value['version'] = version
    Path(os.environ['RELEASE_CONTEXT']).write_bytes(packed(value))
    row.update(schema=5, **{key: value[key] for key in CONTEXT_KEYS})
    row.update(built_at=utc_now(), previous_tag=previous['version'] if previous else None,
               body=notes(previous, commit), run_id=run_id, request_id=value['release_request_id'], actor_id=api.actor_id)
    with tempfile.TemporaryDirectory(prefix='build-', dir=Path(authfile).parent) as scratch:
        directory = Path(scratch)
        worker_build_engine()
        build(directory, row, run_id)
        proof = {name: sha(directory / name) for name in originals(version)}
        verify_artifacts(directory, row, proof)
        # Check the entire cleanup set before deleting any prior attempt.
        for old_row, old_release, _package, snapshot in pending_cleanup:
            validate_unfinished(api, old_row, run_id, old_release, snapshot)
        for old_row, old_release, package, snapshot in pending_cleanup:
            clean_unfinished(api, old_row, run_id, old_release, package, snapshot)
        publish(api, authfile, row, directory, proof)
        if branch == row['source_branch'] == 'main':
            latest(api, authfile)
    return version


def execute():
    branch, commit, run_id = release_inputs()
    os.umask(0o077)
    api = Forgejo(os.environ.get('FORGEJO_RELEASE_TOKEN'))
    with credentials('release', {WEB + '.git': api.token}) as authfile:
        actor = api.request('/user')
        repo = api.repo()
        require(actor.get('login') and repo.get('full_name') == REPO and repo.get('private') is False and
                repo.get('permissions', {}).get('push') is True, 'Release requires write access to the public Forgejo primary')
        api.actor_id = actor['id']
        value = context()
        api.git('fetch', '--no-tags', WEB + '.git', 'refs/heads/' + branch)
        require_ancestor(value['source_branch_head'], 'FETCH_HEAD')
        require_ancestor(commit, value['source_branch_head'])
        require_ancestor(UPSTREAM_COMMIT, commit)
        login(authfile, 'git.134333.xyz', actor['login'], api.token)
        os.environ['CI_REGISTRY_AUTH'] = authfile
        version = continue_release(api, authfile, branch, commit, run_id)
    print('Verified Forgejo release:', WEB + '/releases/tag/' + version, flush=True)


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--cleanup']:
            from release_build import cleanup_run
            run_id = identity()
            try:
                cleanup_run(run_id)
            finally:
                cleanup_auth('release')
                path = Path(os.environ['RUNNER_TEMP']) / ('sub2api-release-' + run_id + '.json')
                path.unlink(missing_ok=True)
        elif sys.argv[1:] == ['--verify-only']:
            verify_build()
        elif not sys.argv[1:]:
            execute()
        else:
            sys.exit('Usage: manual-release.py [--verify-only]')
    except (RuntimeError, ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        sys.exit(f'Workflow stopped: {error}')
