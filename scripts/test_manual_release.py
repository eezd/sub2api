"""Offline regression tests for the publisher and dispatch client."""
from contextlib import contextmanager, redirect_stderr, redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch

import gitea_http

ROOT = Path(__file__).resolve().parents[1]

spec = importlib.util.spec_from_file_location('manual_release', Path(__file__).with_name('manual-release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class BaselineTests(unittest.TestCase):
    def test_selects_nearest_parent_then_newest_completion(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, 'ROOT', Path(directory)):
            def git(*args):
                return subprocess.check_output(['git', '-C', directory, *args], text=True).strip()

            git('init', '-q', '-b', 'main')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.test')
            git('commit', '-q', '--allow-empty', '-m', 'base')
            root = git('rev-parse', 'HEAD')
            git('checkout', '-q', '-b', 'side')
            git('commit', '-q', '--allow-empty', '-m', 'side')
            side = git('rev-parse', 'HEAD')
            git('checkout', '-q', 'main')
            git('commit', '-q', '--allow-empty', '-m', 'main')
            main = git('rev-parse', 'HEAD')
            git('merge', '-q', '--no-ff', '--no-edit', 'side')
            head = git('rev-parse', 'HEAD')
            same_day = '2025-03-01T00:00:00Z'
            records = [dict(source_sha=commit, source_branch=branch, completed_at=stamp, version=version)
                       for commit, branch, stamp, version in [(root, 'dev', '2025-03-02T00:00:00Z', 'root'),
                           (main, 'dev', same_day, 'a'), (side, 'dev', same_day, 'b'),
                           (head, 'other', same_day, 'other')]]
            self.assertIs(release.baseline(records, 'dev', head), records[1])
            records[2]['completed_at'] = '2025-03-01T00:00:01Z'
            self.assertIs(release.baseline(records, 'dev', head), records[2])
            self.assertIsNone(release.baseline(records, 'feature/demo', head))
            self.assertEqual(release.notes(None, head), 'init')
            with self.assertRaisesRegex(ValueError, 'No reachable release baseline for dev'):
                release.baseline([records[1]], 'dev', side)


class ClientTests(unittest.TestCase):
    def run_client(self, dispatch_response):
        source = (ROOT / 'release.sh').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        calls = []
        sha = 'a' * 40

        @contextmanager
        def reply(url, auth, *, method, data, **_kwargs):
            calls.append((url, method, data))
            self.assertEqual(auth, 'token fixture')
            if '/branches/' in url:
                value = {'commit': {'id': sha}}
            elif '/git/commits/' in url:
                value = {'commit': {'committer': {'date': '2026-11-09T00:30:00+02:00'}}}
            elif url.endswith('/dispatches'):
                if isinstance(dispatch_response, OSError):
                    raise dispatch_response
                value = dispatch_response
            elif url.endswith('/actions/runs/123'):
                value = {'conclusion': 'failure'}
            else:
                raise AssertionError(url)
            yield 200, {}, io.BytesIO(json.dumps(value).encode())

        stdout, stderr = io.StringIO(), io.StringIO()
        with (tempfile.TemporaryDirectory() as home,
              patch.object(gitea_http, 'request', reply),
              patch.dict(os.environ, {'FORGEJO_TOKEN': 'fixture', 'HOME': home}),
              patch.object(sys, 'argv', ['release', 'feature/example', str(ROOT)]),
              patch.object(sys, 'path', sys.path[:]), redirect_stdout(stdout), redirect_stderr(stderr),
              self.assertRaises(SystemExit) as exit_status):
            exec(compile(source, 'release.sh', 'exec'), {})
        self.assertEqual(exit_status.exception.code, 1)
        self.assertIn('/branches/feature%2Fexample', calls[0][0])
        self.assertIn('版本：20261108-aaaaaaaa', stdout.getvalue())
        dispatch, = [call for call in calls if call[1] == 'POST']
        self.assertTrue(dispatch[0].endswith(
            '/actions/workflows/sub2api-manual-release.yml/dispatches'))
        self.assertTrue((ROOT / '.forgejo/workflows/sub2api-manual-release.yml').is_file())
        data = json.loads(dispatch[2])
        self.assertEqual(data['ref'], 'refs/heads/main')
        self.assertEqual(data['inputs']['source_sha'], sha)
        self.assertEqual(data['inputs']['source_branch'], 'feature/example')
        self.assertIs(data['return_run_info'], True)
        self.assertEqual(set(data['inputs']), {'source_branch', 'source_sha', 'request_id'})
        self.assertIn(data['inputs']['request_id'], stdout.getvalue())
        return calls, stderr.getvalue()

    def test_lost_dispatch_response_is_not_replayed(self):
        calls, error = self.run_client(OSError('lost response'))
        self.assertEqual(len(calls), 3)
        self.assertIn('按请求 ID 核查', error)

    def test_only_returned_run_id_is_polled(self):
        calls, error = self.run_client({'id': 123, 'run_number': 8})
        self.assertEqual(len(calls), 4)
        self.assertTrue(calls[-1][0].endswith('/actions/runs/123'))
        self.assertIn('发布未成功：failure', error)
        self.assertIn('/actions/runs/8', error)


class ClientCredentialTests(unittest.TestCase):
    def run_client(self, home, token, *, read_error=None):
        source = (ROOT / 'release.sh').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        calls = []

        @contextmanager
        def reply(url, auth, **kwargs):
            calls.append((url, auth))
            raise OSError('synthetic API boundary')
            yield  # pragma: no cover

        stdout, stderr = io.StringIO(), io.StringIO()
        with (patch.dict(os.environ, {'HOME': str(home)}),
              patch.object(gitea_http, 'request', reply),
              patch.object(sys, 'argv', ['release', 'main', str(ROOT)]),
              patch.object(sys, 'path', sys.path[:]),
              redirect_stdout(stdout), redirect_stderr(stderr)):
            if token is None:
                os.environ.pop('FORGEJO_TOKEN', None)
            else:
                os.environ['FORGEJO_TOKEN'] = token
            with self.assertRaises(SystemExit) as status:
                if read_error:
                    with patch.object(Path, 'read_text', side_effect=read_error):
                        exec(compile(source, 'release.sh', 'exec'), {})
                else:
                    exec(compile(source, 'release.sh', 'exec'), {})
        self.assertEqual(status.exception.code, 1)
        self.assertNotIn('发布成功', stdout.getvalue())
        return calls, stdout.getvalue() + stderr.getvalue()

    def test_environment_token_takes_precedence_without_reading_file(self):
        with tempfile.TemporaryDirectory() as home:
            calls, output = self.run_client(home, 'synthetic-env-token',
                                           read_error=AssertionError('file must not be read'))
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][1], 'token synthetic-env-token')
        self.assertNotIn('synthetic-env-token', output)

    def test_unset_or_empty_environment_uses_calling_users_home(self):
        for token in (None, ''):
            with self.subTest(token=token), tempfile.TemporaryDirectory() as home:
                path = Path(home) / '.config/forgejo/token'
                path.parent.mkdir(parents=True)
                path.write_text('  synthetic-file-token\n', encoding='utf-8')
                path.chmod(0o600)
                calls, output = self.run_client(home, token)
                self.assertEqual(len(calls), 1)
                self.assertEqual(calls[0][1], 'token synthetic-file-token')
                self.assertNotIn('synthetic-file-token', output)

    def test_missing_or_empty_file_fails_before_api(self):
        for content in (None, '', ' \n\t'):
            with self.subTest(content=content), tempfile.TemporaryDirectory() as home:
                path = Path(home) / '.config/forgejo/token'
                if content is not None:
                    path.parent.mkdir(parents=True)
                    path.write_text(content, encoding='utf-8')
                calls, output = self.run_client(home, None)
                self.assertEqual(calls, [])
                self.assertIn(str(path), output)
                self.assertIn('缺少 FORGEJO_TOKEN', output)

    def test_unreadable_file_fails_without_leaking_error_or_requesting_api(self):
        with tempfile.TemporaryDirectory() as home:
            calls, output = self.run_client(home, '', read_error=PermissionError('synthetic-private-content'))
        self.assertEqual(calls, [])
        self.assertIn('读取 Forgejo 凭据文件失败', output)
        self.assertNotIn('synthetic-private-content', output)

    def test_invalid_utf8_fails_without_leaking_content_or_requesting_api(self):
        with tempfile.TemporaryDirectory() as home:
            path = Path(home) / '.config/forgejo/token'
            path.parent.mkdir(parents=True)
            path.write_bytes(b'\xffsynthetic-private-content')
            calls, output = self.run_client(home, '')
        self.assertEqual(calls, [])
        self.assertIn('读取 Forgejo 凭据文件失败', output)
        self.assertNotIn('synthetic-private-content', output)


class VerifyBuildTests(unittest.TestCase):
    def setUp(self):
        directory = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.object(release, 'ROOT', directory))
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.test')
        self.git('commit', '-q', '--allow-empty', '-m', 'initial')
        self.commit = self.git('rev-parse', 'HEAD')
        self.env = self.enterContext(patch.dict(os.environ, {
            'RELEASE_REF': 'refs/heads/main', 'SOURCE_BRANCH': 'feature/demo',
            'SOURCE_SHA': self.commit, 'RELEASE_RUN_ID': '123-1',
            'TRUSTED_SHA': 'c' * 40, 'REQUEST_ID': '00000000-0000-4000-8000-000000000001',
        }))
        self.env.pop('FORGEJO_RELEASE_TOKEN', None)
        self.api = self.enterContext(patch.object(release, 'Forgejo'))
        self.publish = self.enterContext(patch.object(release, 'publish'))
        self.engine = self.enterContext(patch.object(release, 'worker_build_engine'))
        self.build = self.enterContext(patch.object(release, 'build'))

    def git(self, *args):
        return release.git(*args)

    def test_builds_original_sha_without_api_or_checkpoint(self):
        self.git('commit', '-q', '--allow-empty', '-m', 'advance')
        self.git('update-ref', 'refs/remotes/origin/feature/demo', 'HEAD')
        with redirect_stdout(io.StringIO()) as stdout:
            release.verify_build()
        self.build.assert_called_once()
        output, row, run_id = self.build.call_args.args
        self.assertEqual(row['source_sha'], self.commit)
        self.assertEqual(row['source_branch'], 'feature/demo')
        self.assertEqual(run_id, '123-1')
        self.engine.assert_called_once_with()
        self.assertFalse(output.exists())
        self.assertIn('nothing published', stdout.getvalue())
        self.api.assert_not_called()
        self.publish.assert_not_called()

    def test_unreachable_sha_stops_before_worker_access(self):
        self.git('update-ref', 'refs/remotes/origin/feature/demo', self.commit)
        self.git('commit', '-q', '--allow-empty', '-m', 'not on source branch')
        self.env['SOURCE_SHA'] = self.git('rev-parse', 'HEAD')
        with self.assertRaisesRegex(ValueError, 'no longer reachable'):
            release.verify_build()
        self.engine.assert_not_called()
        self.build.assert_not_called()
        self.api.assert_not_called()


class WorkerTests(unittest.TestCase):
    TLS_ENV = {'DOCKER_HOST': 'tcp://docker:2376', 'DOCKER_TLS_VERIFY': '1',
               'DOCKER_CERT_PATH': '/fixture/client'}

    def test_preserves_runner_tls_environment_and_requires_native_linux_amd64(self):
        for platform in ('linux/x86_64', 'linux/amd64', 'linux/aarch64', 'windows/amd64'):
            with (self.subTest(platform=platform), patch.dict(os.environ, self.TLS_ENV, clear=True),
                  patch.object(release, 'run', return_value=platform) as docker):
                if platform in ('linux/x86_64', 'linux/amd64'):
                    release.worker_build_engine()
                else:
                    with self.assertRaisesRegex(ValueError, 'native Linux amd64'):
                        release.worker_build_engine()
                self.assertEqual(dict(os.environ), self.TLS_ENV)
                docker.assert_called_once_with('docker', 'info', '--format', '{{.OSType}}/{{.Architecture}}')

    def test_invalid_connection_never_falls_back_to_socket_or_another_context(self):
        invalid = [{}, {'DOCKER_HOST': ''}, {'DOCKER_HOST': 'unix:///var/run/docker.sock'},
                   {'DOCKER_HOST': 'tcp://docker:2375'}, {'DOCKER_HOST': 'ssh://docker:2376'},
                   {'DOCKER_HOST': 'tcp://secret@docker:2376'}, {'DOCKER_HOST': 'tcp://docker:2376/path'},
                   {'DOCKER_TLS_VERIFY': ''}, {'DOCKER_TLS_VERIFY': '0'}, {'DOCKER_CERT_PATH': ''},
                   {'DOCKER_CONTEXT': 'production'}]
        for changes in invalid:
            environment = dict(self.TLS_ENV, **changes) if changes else {}
            with (self.subTest(changes=changes), patch.dict(os.environ, environment, clear=True),
                  patch.object(release, 'run') as docker, self.assertRaises(ValueError)):
                try:
                    release.worker_build_engine()
                finally:
                    docker.assert_not_called()
                    self.assertEqual(dict(os.environ), environment)


class SyncRegistryTests(unittest.TestCase):
    def setUp(self):
        script = ROOT / 'sysnc-github.sh'
        source = script.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
        scope = {'__name__': 'test_sync_registry'}
        exec(compile(source, str(script), 'exec'), scope)
        self.read_manifest = scope['read_manifest']
        self.ref = 'ghcr.io/eezd/sub2api:20250301-aaaaaaaa'

    def test_allows_explicit_registry_missing_errors_only_for_destination(self):
        for status, error in ((1, b'name unknown'), (2, b'reading manifest: manifest unknown')):
            result = subprocess.CompletedProcess([], status, b'', error)
            with self.subTest(status=status), patch('subprocess.run', return_value=result):
                self.assertIsNone(self.read_manifest(self.ref, 'auth.json', allow_missing=True))
                with self.assertRaisesRegex(RuntimeError, '读取 OCI 清单失败'):
                    self.read_manifest(self.ref, 'auth.json')

    def test_never_treats_auth_rate_limit_or_local_tool_errors_as_missing(self):
        for status, error in ((2, b'unauthorized'), (2, b'toomanyrequests'), (125, b'manifest unknown')):
            result = subprocess.CompletedProcess([], status, b'', error)
            with self.subTest(status=status, error=error), patch('subprocess.run', return_value=result):
                with self.assertRaisesRegex(RuntimeError, '读取 OCI 清单失败'):
                    self.read_manifest(self.ref, 'auth.json', allow_missing=True)


class TransportTests(unittest.TestCase):
    def test_token_only_enters_curl_config_and_post_is_not_replayed(self):
        def curl(args, *, input, stdout, stderr, env):
            self.assertNotIn('fixture-secret', ' '.join(args))
            self.assertNotIn('FORGEJO_TOKEN', env)
            self.assertNotIn('--location', args)
            self.assertNotIn('--retry', args)
            self.assertEqual(input, b'header = "Authorization: token fixture-secret"\n')
            self.assertEqual(Path(args[args.index('--data-binary') + 1][1:]).read_bytes(), b'{}')
            headers = Path(args[args.index('--dump-header') + 1])
            headers.write_bytes(b'HTTP/1.1 100 Continue\r\n\r\nHTTP/2 302\r\nlocation: /next\r\n\r\n')
            stdout.write(b'unchanged')
            return subprocess.CompletedProcess(args, 0)

        with patch.dict(os.environ, {'FORGEJO_TOKEN': 'fixture-secret'}), patch.object(gitea_http.subprocess, 'run', side_effect=curl) as send:
            with gitea_http.request('https://gitea.test/dispatch', 'token fixture-secret', method='POST', data=b'{}') as result:
                status, headers, body = result
                self.assertEqual(status, 302)
                self.assertEqual(headers['Location'], '/next')
                self.assertEqual(body.read(), b'unchanged')
        send.assert_called_once()

    def test_draft_lookup_falls_back_to_exact_tag(self):
        api = release.Forgejo('fixture')
        version = '20250301-aaaaaaaa'
        draft = {'id': 7, 'tag_name': version, 'draft': True}
        with patch.object(api, 'repo', side_effect=[None, [draft]]) as query:
            self.assertEqual(api.release(version), draft)
        self.assertEqual(query.call_args_list[1].args, ('/releases?draft=true&tag_filter=' + version,))


class FakeForgejo:
    """Offline server: records every mutation and can lose one upload response."""

    actor_id = 7

    def __init__(self, row):
        self.row = row
        self.entry = None
        self.package = None
        self.attachments = {}
        self.writes = []
        self.tags = set()
        self.annotation = None
        self.fail_upload = None
        self.old_run = dict(id=100, workflow_id=release.WORKFLOW, repository={'full_name': release.REPO},
                            event='workflow_dispatch', commit_sha='c' * 40, status='failure', stopped='2025-03-02',
                            event_payload=json.dumps({'ref': 'refs/heads/main', 'inputs': {
                                'source_branch': row['source_branch'], 'source_sha': row['source_sha'],
                                'request_id': row['request_id']}}))
        self.runs = [self.old_run, dict(id=200, workflow_id=release.WORKFLOW,
                                       repository={'full_name': release.REPO}, status='running', stopped=None)]

    def release(self, _version):
        return self.entry

    def pages(self, _path):
        return [self.entry] if self.entry else []

    def assets(self, _entry):
        return self.attachments.copy()

    def download(self, asset):
        return asset

    def upload(self, _entry, file):
        self.writes.append(('upload', file.name))
        self.attachments[file.name] = file.read_bytes()
        if self.fail_upload == file.name:
            self.fail_upload = None
            raise RuntimeError('Upload committed but response lost')

    def request(self, path, method='GET', **_kwargs):
        if path != release.package_path(self.row['version']):
            raise AssertionError(path)
        if method == 'DELETE':
            self.writes.append(('DELETE', path))
            self.package = None
            return None
        return self.package

    def repo(self, path, method='GET', payload=None):
        if path == '/tags/' + self.row['version']:
            return self.annotation
        if path == '/actions/runs/100':
            return self.old_run
        if path.startswith('/actions/runs?'):
            return {'total_count': len(self.runs), 'workflow_runs': self.runs if path.endswith('page=1') else []}
        self.writes.append((method, path))
        if path == '/releases' and method == 'POST':
            self.entry = dict(payload, id=42, author={'id': self.actor_id}, published_at='2025-03-02T00:00:00Z')
            return self.entry
        if path == '/releases/42' and method == 'PATCH':
            self.entry.update(payload)
            return self.entry
        if path == '/releases/42' and method == 'DELETE':
            self.entry = None
            self.attachments.clear()
            return None
        raise AssertionError((path, method))


class PublisherTests(unittest.TestCase):
    def setUp(self):
        self.row = dict(version='20250301-aaaaaaaa', source_sha='a' * 40, source_branch='dev', previous_tag=None,
                        built_at='2025-03-01T00:00:00Z', body='init', run_id='100-1', actor_id=7,
                        request_id='00000000-0000-4000-8000-000000000001', control_sha='c' * 40)
        self.api = FakeForgejo(self.row)
        self.image = dict(ref=release.IMAGE + ':' + self.row['version'], digest='sha256:' + 'd' * 64,
                          platforms={'linux/amd64': 'sha256:' + 'a' * 64, 'linux/arm64': 'sha256:' + 'b' * 64})
        self.enterContext(patch.dict(os.environ, {'REQUEST_ID': self.row['request_id'], 'TRUSTED_SHA': 'c' * 40}))
        self.enterContext(patch.object(release, 'version_for', return_value=self.row['version']))
        self.enterContext(patch.object(release, 'utc_now', return_value=self.row['built_at']))
        self.enterContext(patch.object(release, 'baseline', return_value=None))
        self.enterContext(patch.object(release, 'oci_image', return_value=self.image))
        self.enterContext(patch.object(release, 'inspect_registry', return_value=None))
        self.enterContext(patch.object(release, 'native_tags', side_effect=lambda _: self.api.tags.copy()))
        self.enterContext(patch.object(release, 'tag', side_effect=self.tag))
        self.engine = self.enterContext(patch.object(release, 'worker_build_engine'))
        self.builder = self.enterContext(patch.object(release, 'build', side_effect=self.build))
        self.registry = self.enterContext(patch.object(release, 'registry'))
        self.workspaces = []

    def tag(self, _api, row, *, create=False):
        version = row['version']
        if create and version not in self.api.tags:
            self.api.tags.add(version)
            self.api.annotation = dict(name=version, commit={'sha': row['source_sha']},
                                       message=release.packed(release.attempt_identity(row)).decode())
        if version not in self.api.tags:
            raise ValueError('Missing or conflicting annotated tag')
        if self.api.annotation['commit']['sha'] != row['source_sha']:
            raise ValueError('Missing or conflicting annotated tag')

    def build(self, directory, row, *_args):
        self.workspaces.append(directory)
        packages = []
        for name in release.names(row['version']):
            (directory / name).write_bytes((name + ' build ' + str(self.builder.call_count)).encode())
            packages.append({'name': name, 'sha256': release.sha(directory / name)})
        (directory / 'SHA256SUMS').write_bytes(release.sums_bytes({p['name']: p['sha256'] for p in packages}))
        metadata = {key: row[key] for key in ('version', 'source_sha', 'source_branch', 'previous_tag', 'built_at')}
        (directory / 'build.json').write_bytes(release.packed(dict(metadata, packages=packages, image=self.image)))
        (directory / 'image.oci').write_bytes(b'OCI fixture')

    def retry(self, branch='dev', run_id='200-1', sha='a' * 40):
        release.continue_release(self.api, 'auth.json', branch, sha, run_id)

    def partial(self):
        self.api.fail_upload = release.names(self.row['version'])[0]
        with self.assertRaisesRegex(RuntimeError, 'response lost'):
            self.retry(run_id='100-1')
        self.assertNotIn(release.COMPLETE, self.api.attachments)
        self.api.package = dict(name='sub2api', version=self.row['version'], type='container',
                                owner={'login': 'eezd'}, creator={'id': 7})
        self.api.writes.clear()

    def test_first_release_six_packages_and_completion_last_in_disposable_workspace(self):
        self.retry()
        self.assertEqual(self.api.writes[-1], ('upload', release.COMPLETE))
        self.assertEqual(set(self.api.attachments), set(release.names(self.row['version'])) |
                         {'build.json', 'SHA256SUMS', release.COMPLETE})
        self.assertFalse(self.api.entry['draft'])
        self.assertEqual(self.api.entry['body'], 'init')
        self.assertEqual(self.builder.call_args.args[1]['source_sha'], 'a' * 40)
        self.assertTrue(all(not directory.exists() for directory in self.workspaces))

    def test_completed_same_sha_cross_branch_is_zero_write_and_no_rebuild(self):
        self.retry()
        self.api.writes.clear()
        self.builder.reset_mock()
        self.engine.reset_mock()
        self.retry(branch='another')
        self.assertEqual(self.api.writes, [])
        self.builder.assert_not_called()
        self.engine.assert_not_called()
        self.assertEqual(json.loads(self.api.attachments['build.json'])['source_branch'], 'dev')

    def test_partial_upload_is_deleted_exactly_then_fully_rebuilt(self):
        self.partial()
        original = self.api.attachments[release.names(self.row['version'])[0]]
        annotation = self.api.annotation.copy()
        self.retry()
        self.assertEqual(self.api.writes[:2], [('DELETE', '/releases/42'),
                                             ('DELETE', release.package_path(self.row['version']))])
        self.assertEqual(self.builder.call_count, 2)
        self.assertNotEqual(self.api.attachments[release.names(self.row['version'])[0]], original)
        self.assertEqual(self.api.annotation, annotation)
        self.assertTrue(all(not directory.exists() for directory in self.workspaces))

    def test_original_active_or_successful_run_never_deletes(self):
        for status in ('running', 'waiting', 'success', 'unknown'):
            with self.subTest(status=status):
                self.setUpPartialIfNeeded()
                self.api.old_run['status'] = status
                with self.assertRaisesRegex(ValueError, 'not terminated unsuccessfully'):
                    self.retry()
                self.assertEqual(self.api.writes, [])

    def setUpPartialIfNeeded(self):
        if not self.api.tags:
            self.partial()

    def test_later_retry_still_running_blocks_cleanup_even_if_first_run_failed(self):
        self.partial()
        self.api.runs.append(dict(id=150, workflow_id=release.WORKFLOW, repository={'full_name': release.REPO},
                                  status='running', stopped=None))
        with self.assertRaisesRegex(ValueError, 'Another release task'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_repeated_run_pagination_blocks_cleanup(self):
        self.partial()
        original = self.api.repo
        self.api.repo = lambda path, *args: {'total_count': len(self.api.runs), 'workflow_runs': self.api.runs} if path.startswith('/actions/runs?') else original(path, *args)
        with self.assertRaisesRegex(ValueError, 'repeated Actions run'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_incomplete_run_pagination_blocks_cleanup(self):
        self.partial()
        original = self.api.repo

        def incomplete(path, *args):
            result = original(path, *args)
            if path.startswith('/actions/runs?'):
                result['total_count'] += 1
            return result

        self.api.repo = incomplete
        with self.assertRaisesRegex(ValueError, 'Incomplete Actions run pagination'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_missing_current_or_foreign_repository_listing_blocks_cleanup(self):
        self.partial()
        current = self.api.runs.pop()
        with self.assertRaisesRegex(ValueError, 'Current Actions run missing'):
            self.retry()
        self.assertEqual(self.api.writes, [])
        self.api.runs.append(dict(current, repository={'full_name': 'other/sub2api'}))
        with self.assertRaisesRegex(ValueError, 'Unknown or repeated Actions run identity'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_unknown_annotation_or_changed_inputs_never_deletes(self):
        self.partial()
        self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': {}})
        with self.assertRaisesRegex(ValueError, 'identity differs'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_foreign_image_owner_stops_before_release_delete(self):
        self.partial()
        self.api.package['creator']['id'] = 999
        with self.assertRaisesRegex(ValueError, 'Unknown unfinished image'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_tag_sha_conflict_stops_before_any_delete(self):
        self.partial()
        self.api.annotation['commit']['sha'] = 'b' * 40
        with self.assertRaisesRegex(ValueError, 'conflicting annotated tag'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_corrupted_completion_is_never_downgraded_or_deleted(self):
        self.retry()
        self.api.attachments[release.COMPLETE] = b'{}'
        self.api.writes.clear()
        with self.assertRaisesRegex(ValueError, 'Invalid Forgejo completion'):
            self.retry()
        self.assertEqual(self.api.writes, [])
        self.assertEqual(self.builder.call_count, 1)

    def test_other_unfinished_versions_are_not_deleted_or_blocking(self):
        self.api.tags.add('20250228-bbbbbbbb')
        self.retry()
        self.assertFalse(any(method == 'DELETE' for method, _ in self.api.writes))

    def test_unknown_delete_result_is_not_replayed_and_never_builds(self):
        self.partial()
        original = self.api.repo
        def repo(path, method='GET', *args):
            if method == 'DELETE':
                self.api.writes.append((method, path))
                raise OSError('DELETE result unknown')
            return original(path, method, *args)
        self.api.repo = repo
        with self.assertRaisesRegex(OSError, 'DELETE result unknown'):
            self.retry()
        self.assertEqual(self.api.writes, [('DELETE', '/releases/42')])
        self.assertEqual(self.builder.call_count, 1)


class ReleaseVisibilityTests(unittest.TestCase):
    def execute(self, repository):
        self.api = MagicMock()
        self.api.token = 'synthetic-release-token'
        self.api.request.return_value = {'login': 'fixture', 'id': 7}
        self.api.repo.return_value = repository
        with (patch.object(release, 'release_inputs', return_value=('dev', 'a' * 40, '123-1')),
              patch.object(release.os, 'umask'), patch.object(release, 'Forgejo', return_value=self.api),
              patch.object(release, 'require_ancestor') as ancestry,
              patch.object(release.subprocess, 'run') as process,
              patch.object(release, 'continue_release') as publish,
              patch.object(release, 'version_for', return_value='20250301-aaaaaaaa'), redirect_stdout(io.StringIO())):
            self.process, self.publish, self.ancestry = process, publish, ancestry
            release.execute()

    def test_public_primary_with_write_permission_reaches_release(self):
        self.execute({'full_name': release.REPO, 'private': False, 'permissions': {'push': True}})
        self.assertEqual(self.api.actor_id, 7)
        self.publish.assert_called_once()
        self.api.git.assert_called_once_with('fetch', '--no-tags', release.WEB + '.git', 'refs/heads/dev')
        self.ancestry.assert_called_once_with('a' * 40, 'FETCH_HEAD')
        self.process.assert_called_once()

    def test_wrong_visibility_identity_or_permission_stops_before_git_login_or_release(self):
        for changes in ({'private': True}, {'private': None}, {'private': 0},
                        {'full_name': 'other/sub2api'}, {'permissions': {'push': False}}):
            with self.subTest(changes=changes), self.assertRaisesRegex(ValueError, 'public Forgejo primary'):
                try:
                    self.execute({'full_name': release.REPO, 'private': False, 'permissions': {'push': True}} | changes)
                finally:
                    self.api.git.assert_not_called()
                    self.process.assert_not_called()
                    self.publish.assert_not_called()
                    self.ancestry.assert_not_called()


class WorkflowContractTests(unittest.TestCase):
    def test_only_forgejo_dispatch_three_strings_and_legal_secret(self):
        import yaml
        workflow = yaml.load((ROOT / '.forgejo/workflows/sub2api-manual-release.yml').read_text(), Loader=yaml.BaseLoader)
        self.assertEqual(set(workflow['on']), {'workflow_dispatch'})
        inputs = workflow['on']['workflow_dispatch']['inputs']
        self.assertEqual(set(inputs), {'source_branch', 'source_sha', 'request_id'})
        for entry in inputs.values():
            self.assertEqual(entry, {'required': 'true', 'type': 'string'})
        self.assertFalse(list((ROOT / '.gitea/workflows').glob('*.yml')))
        self.assertFalse(list((ROOT / '.github/workflows').glob('*.yml')))
        self.assertEqual(workflow['concurrency']['cancel-in-progress'], 'false')
        job = workflow['jobs']['release']
        self.assertEqual(job['runs-on'], 'docker')
        self.assertFalse(any(key.startswith('SUB2API_') or key.startswith('DOCKER_') for key in job['env']))
        self.assertEqual(job['steps'][-1]['env'], {'FORGEJO_RELEASE_TOKEN': '${{ secrets.RELEASE_TOKEN }}'})
        self.assertEqual(job['steps'][0]['with']['ref'], '${{ forgejo.sha }}')
        self.assertEqual(job['steps'][0]['with']['persist-credentials'], 'false')


if __name__ == '__main__':
    unittest.main()
