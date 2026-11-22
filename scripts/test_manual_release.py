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
        with tempfile.TemporaryDirectory() as directory, patch.object(release, 'ROOT', Path(directory)), \
             patch.dict(os.environ, HOME=directory, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull):
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


class CustomVersionTests(unittest.TestCase):
    def test_counter_is_numeric_and_only_uses_pinned_upstream_base(self):
        self.assertEqual(release.UPSTREAM_TAG, 'v2.24.1')
        self.assertEqual(release.UPSTREAM_COMMIT, '0755d5ef037f64d203828d0edcc513764b69e1d2')
        self.assertEqual(release.version_for([]), 'v2.24.1-custom.1')
        self.assertEqual(release.version_for({'v2.24.1-custom.2', 'v2.24.1-custom.9',
                         'v2.24.1-custom.10', 'v2.23.1-custom.999', 'v2.24.0-custom.999',
                         '20261121-aaaaaaaa', 'v2.24.1'}), 'v2.24.1-custom.11')

    def test_remote_tags_and_markerless_legacy_releases_both_occupy_numbers(self):
        api = MagicMock()
        api.git.return_value = ('a' * 40 + '\trefs/tags/v2.24.1-custom.9\n' +
                                'b' * 40 + '\trefs/tags/v2.23.1-custom.999\n')
        api.pages.return_value = [{'tag_name': 'v2.24.1-custom.10'}]
        api.assets.return_value = {'checksums.txt': b'legacy'}
        history, unfinished = release.release_history(api)
        self.assertEqual(history, {})  # Historical imports are never completed baselines.
        self.assertEqual(release.version_for(unfinished), 'v2.24.1-custom.11')
        api.download.assert_not_called()


class VerifyBuildTests(unittest.TestCase):
    def setUp(self):
        directory = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.dict(os.environ, {'HOME': str(directory), 'GIT_CONFIG_NOSYSTEM': '1',
                                                 'GIT_CONFIG_GLOBAL': os.devnull}))
        self.enterContext(patch.object(release, 'ROOT', directory))
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.test')
        self.git('commit', '-q', '--allow-empty', '-m', 'initial')
        self.commit = self.git('rev-parse', 'HEAD')
        self.enterContext(patch.object(release, 'UPSTREAM_COMMIT', self.commit))
        self.env = self.enterContext(patch.dict(os.environ, {
            'RELEASE_REF': 'refs/heads/main', 'SOURCE_BRANCH': 'feature/demo',
            'SOURCE_SHA': self.commit, 'RELEASE_RUN_ID': '123-1',
            'TRUSTED_SHA': 'c' * 40, 'REQUEST_ID': '00000000-0000-4000-8000-000000000001',
        }))
        self.context_path = directory / 'context.json'
        self.env.update(CONTROL_REF='refs/heads/main', CONTROL_SHA='c' * 40, CI_RUN_ID='123', CI_RUN_ATTEMPT='1',
                        RELEASE_CONTEXT=str(self.context_path))
        self.write_context(self.commit)
        self.env.pop('FORGEJO_RELEASE_TOKEN', None)
        self.api = self.enterContext(patch.object(release, 'Forgejo'))
        self.publish = self.enterContext(patch.object(release, 'publish'))
        self.engine = self.enterContext(patch.object(release, 'worker_build_engine'))
        self.build = self.enterContext(patch.object(release, 'build'))

    def write_context(self, commit):
        self.context_path.write_bytes(release.packed(dict(schema=1, source_branch='feature/demo', source_sha=commit,
            source_branch_head=commit, release_inputs={'source_branch': 'feature/demo', 'source_sha': commit},
            version='', control_sha='c'*40, release_run_id='123-1',
            release_request_id='00000000-0000-4000-8000-000000000001')))

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
        self.assertEqual(row['version'], 'v2.24.1-custom.1')
        self.assertIn('verification-only label, not allocated', stdout.getvalue())
        self.assertEqual(run_id, '123-1')
        self.engine.assert_called_once_with()
        self.assertFalse(output.exists())
        self.assertIn('nothing published', stdout.getvalue())
        self.api.assert_not_called()
        self.publish.assert_not_called()

    def test_unreachable_sha_stops_before_worker_access(self):
        self.git('update-ref', 'refs/remotes/origin/feature/demo', self.commit)
        self.git('commit', '-q', '--allow-empty', '-m', 'not on source branch')
        self.write_context(self.git('rev-parse', 'HEAD'))
        with self.assertRaisesRegex(ValueError, 'no longer reachable'):
            release.verify_build()
        self.engine.assert_not_called()
        self.build.assert_not_called()
        self.api.assert_not_called()

    def test_source_without_pinned_upstream_stops_before_worker_access(self):
        self.git('checkout', '-q', '--orphan', 'unrelated')
        self.git('commit', '-q', '--allow-empty', '-m', 'unrelated root')
        self.write_context(self.git('rev-parse', 'HEAD'))
        self.git('update-ref', 'refs/remotes/origin/feature/demo', 'HEAD')
        with self.assertRaisesRegex(ValueError, 'no longer reachable'):
            release.verify_build()
        self.engine.assert_not_called()
        self.build.assert_not_called()
        self.api.assert_not_called()

    def test_current_context_rejects_stale_run_control_and_non_normalized_inputs(self):
        for field in ('release_run_id', 'control_sha', 'release_inputs', 'source_branch_head'):
            with self.subTest(field=field):
                self.write_context(self.commit)
                value = json.loads(self.context_path.read_bytes())
                value[field] = ({'source_branch': ' feature/demo ', 'source_sha': self.commit}
                                if field == 'release_inputs' else 'invalid')
                self.context_path.write_bytes(release.packed(value))
                with self.assertRaises(ValueError):
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
        script = ROOT / 'scripts/sync-github.py'
        source = script.read_text()
        scope = {'__name__': 'test_sync_registry'}
        exec(compile(source, str(script), 'exec'), scope)
        self.read_manifest = scope['read_manifest']
        self.ref = 'ghcr.io/eezd/sub2api:v2.22.0-custom.1'

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
        version = 'v2.22.0-custom.1'
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
        self.annotations = {}
        self.fail_upload = None
        self.old_run = dict(id=100, workflow_id=release.LEGACY_WORKFLOW, repository={'full_name': release.REPO},
                            event='workflow_dispatch', commit_sha='c' * 40, status='failure', stopped='2025-03-02',
                            event_payload=json.dumps({'ref': 'refs/heads/main', 'inputs': {
                                'source_branch': row['source_branch'], 'source_sha': row['source_sha'],
                                'request_id': row['request_id']}}))
        self.runs = [self.old_run, dict(id=200, workflow_id=release.WORKFLOW,
                                       repository={'full_name': release.REPO}, status='running', stopped=None)]

    @property
    def annotation(self):
        return self.annotations[self.row['version']]

    def git(self, *args):
        return 'stable tag refs'

    def release(self, version):
        return self.entry if self.entry and self.entry['tag_name'] == version else None

    def pages(self, _path):
        return [self.entry] if self.entry else []

    def assets(self, _entry):
        return {name: {'name': name, 'size': len(content)} for name, content in self.attachments.items()}

    def download(self, asset):
        return self.attachments[asset['name']]

    def upload(self, _entry, file):
        self.writes.append(('upload', file.name))
        self.attachments[file.name] = file.read_bytes()
        if self.fail_upload == file.name:
            self.fail_upload = None
            raise RuntimeError('Upload committed but response lost')

    def request(self, path, method='GET', **_kwargs):
        if not path.startswith('/packages/eezd/container/sub2api/'):
            raise AssertionError(path)
        if method == 'DELETE':
            self.writes.append(('DELETE', path))
            self.package = None
            return None
        return self.package if self.package and path.endswith('/' + self.package['version']) else None

    def repo(self, path, method='GET', payload=None):
        if path.startswith('/tags/'):
            return self.annotations[path.removeprefix('/tags/')]
        if path == '/actions/runs/100':
            return self.old_run
        if path.startswith('/actions/runs?'):
            return {'total_count': len(self.runs), 'workflow_runs': self.runs if path.endswith('page=1') else []}
        self.writes.append((method, path))
        if path == '/releases' and method == 'POST':
            self.attachments = {}
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
        # Keep recovery fixtures on their original base independently of the current upstream pin.
        self.enterContext(patch.object(release, 'UPSTREAM_TAG', 'v2.23.1'))
        self.row = dict(version='v2.23.1-custom.1', source_sha='a' * 40, source_branch='dev', previous_tag=None,
                        built_at='2025-03-01T00:00:00Z', body='init', run_id='100-1', actor_id=7,
                        request_id='00000000-0000-4000-8000-000000000001', control_sha='c' * 40)
        self.api = FakeForgejo(self.row)
        self.image = dict(ref=release.IMAGE + ':' + self.row['version'], digest='sha256:' + 'd' * 64,
                          platforms={'linux/amd64': 'sha256:' + 'a' * 64, 'linux/arm64': 'sha256:' + 'b' * 64})
        self.enterContext(patch.dict(os.environ, {'REQUEST_ID': self.row['request_id'], 'TRUSTED_SHA': 'c' * 40}))
        self.enterContext(patch.object(release, 'utc_now', return_value=self.row['built_at']))
        self.enterContext(patch.object(release, 'baseline', return_value=None))
        self.enterContext(patch.object(release, 'oci_image', side_effect=lambda _path, version:
                                     dict(self.image, ref=release.IMAGE + ':' + version)))
        self.enterContext(patch.object(release, 'inspect_registry', return_value=None))
        self.enterContext(patch.object(release, 'native_tags', side_effect=lambda _: self.api.tags.copy()))
        self.enterContext(patch.object(release, 'tag', side_effect=self.tag))
        self.engine = self.enterContext(patch.object(release, 'worker_build_engine'))
        self.builder = self.enterContext(patch.object(release, 'build', side_effect=self.build))
        self.registry = self.enterContext(patch.object(release, 'registry'))
        self.enterContext(patch.object(release, 'public_registry'))
        self.enterContext(patch.object(release, 'latest'))
        self.context_file = Path(self.enterContext(tempfile.TemporaryDirectory())) / 'release.json'
        self.enterContext(patch.dict(os.environ, RELEASE_CONTEXT=str(self.context_file), CONTROL_REF='refs/heads/main',
                                    CONTROL_SHA='c'*40, CI_RUN_ID='200', CI_RUN_ATTEMPT='1'))
        self.workspaces = []

    def tag(self, _api, row, *, create=False):
        version = row['version']
        if create and version not in self.api.tags:
            self.api.tags.add(version)
            self.api.annotations[version] = dict(name=version, commit={'sha': row['source_sha']},
                                       message=release.packed(release.attempt_identity(row)).decode())
            if row.get('schema') == 5 and row['run_id'] == '100-1':
                self.api.old_run['workflow_id'] = release.WORKFLOW
                self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': row['release_inputs']})
        if version not in self.api.tags:
            raise ValueError('Missing or conflicting annotated tag')
        if self.api.annotations[version]['commit']['sha'] != row['source_sha']:
            raise ValueError('Missing or conflicting annotated tag')

    def build(self, directory, row, *_args):
        self.workspaces.append(directory)
        packages = []
        for name in release.names(row['version']):
            (directory / name).write_bytes((name + ' build ' + str(self.builder.call_count)).encode())
            packages.append({'name': name, 'sha256': release.sha(directory / name)})
        (directory / 'SHA256SUMS').write_bytes(release.sums_bytes({p['name']: p['sha256'] for p in packages}))
        metadata = {key: row[key] for key in ('version', 'source_sha', 'source_branch', 'previous_tag', 'built_at')}
        if row.get('schema') == 5:
            metadata.update({key: row[key] for key in ('schema', *release.CONTEXT_KEYS)})
        (directory / 'build.json').write_bytes(release.packed(dict(metadata, packages=packages,
                                         image=dict(self.image, ref=release.IMAGE + ':' + row['version']))))
        (directory / 'image.oci').write_bytes(b'OCI fixture')

    def retry(self, branch='dev', run_id='200-1', sha='a' * 40):
        os.environ.update(CI_RUN_ID=run_id.split('-')[0], CI_RUN_ATTEMPT=run_id.split('-')[1])
        self.context_file.write_bytes(release.packed(dict(schema=1, version='', source_sha=sha, source_branch=branch,
            source_branch_head=sha, release_inputs={'source_branch': branch, 'source_sha': sha}, control_sha='c'*40,
            release_run_id=run_id, release_request_id=self.row['request_id'])))
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

    def test_dev_new_publish_never_maintains_latest(self):
        self.retry(branch='dev')
        release.latest.assert_not_called()
        self.assertFalse(self.api.entry['draft'])
        self.assertIn(release.COMPLETE, self.api.attachments)

    def test_dev_completed_reuse_never_maintains_latest(self):
        self.retry(branch='dev')
        release.latest.reset_mock()
        self.api.writes.clear()
        self.builder.reset_mock()
        self.retry(branch='dev')
        release.latest.assert_not_called()
        self.builder.assert_not_called()
        self.assertEqual(self.api.writes, [])

    def test_main_request_reusing_dev_completed_source_never_maintains_latest(self):
        self.retry(branch='dev')
        release.latest.reset_mock()
        self.api.writes.clear()
        self.builder.reset_mock()
        self.retry(branch='main')
        release.latest.assert_not_called()
        self.builder.assert_not_called()
        self.assertEqual(self.api.writes, [])

    def test_main_publish_and_completed_reuse_maintain_latest(self):
        self.retry(branch='main')
        release.latest.assert_called_once_with(self.api, 'auth.json')
        release.latest.reset_mock()
        self.retry(branch='main')
        release.latest.assert_called_once_with(self.api, 'auth.json')

    def test_partial_upload_is_deleted_exactly_then_fully_rebuilt(self):
        self.partial()
        original = self.api.attachments[release.names(self.row['version'])[0]]
        annotation = self.api.annotation.copy()
        self.retry()
        self.assertEqual(self.api.writes[:2], [('DELETE', '/releases/42'),
                                             ('DELETE', release.package_path(self.row['version']))])
        self.assertEqual(self.builder.call_count, 2)
        new_version = 'v2.23.1-custom.2'
        self.assertEqual(self.api.entry['tag_name'], new_version)
        self.assertNotEqual(self.api.attachments[release.names(new_version)[0]], original)
        self.assertEqual(self.api.tags, {self.row['version'], new_version})
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
        with self.assertRaisesRegex(ValueError, 'Invalid source branch'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_foreign_image_owner_stops_before_release_delete(self):
        self.partial()
        self.api.package['creator']['id'] = 999
        with self.assertRaisesRegex(ValueError, 'Unknown unfinished image'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_annotation_source_conflict_stops_before_any_delete(self):
        self.partial()
        owner = json.loads(self.api.annotation['message'])
        owner['source_sha'] = 'a' * 8 + 'b' * 32
        self.api.annotation['message'] = json.dumps(owner)
        with self.assertRaisesRegex(ValueError, 'Unknown unfinished version ownership'):
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
        version = 'v2.22.0-custom.9'
        self.api.tags.add(version)
        self.api.annotations[version] = {'commit': {'sha': 'b' * 40}}
        self.retry()
        self.assertFalse(any(method == 'DELETE' for method, _ in self.api.writes))

    def test_bare_failed_tag_occupies_number_without_cleanup(self):
        self.partial()
        self.api.entry = self.api.package = None
        self.api.attachments.clear()
        self.api.old_run['status'] = 'running'  # No cleanup or revalidation of an artifact-free tag.
        self.retry()
        self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')
        self.assertFalse(any(method == 'DELETE' for method, _ in self.api.writes))

    def test_completed_tag_full_sha_conflict_is_never_downgraded(self):
        self.retry()
        self.api.annotation['commit']['sha'] = 'a' * 8 + 'b' * 32
        self.api.writes.clear()
        with self.assertRaisesRegex(ValueError, 'conflicting annotated tag'):
            self.retry()
        self.assertEqual(self.api.writes, [])
        self.assertEqual(self.builder.call_count, 1)

    def test_image_only_without_metadata_is_unknown_and_never_deleted(self):
        self.partial()
        self.api.entry = None
        self.api.attachments.clear()
        self.api.package = {'owner': {'login': 'eezd'}, 'name': 'sub2api', 'type': 'container',
                            'version': self.row['version'], 'creator': {'id': self.api.actor_id}}
        with self.assertRaisesRegex(ValueError, 'Unknown unfinished image metadata'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_same_sha_failed_attempt_from_old_branch_is_cleaned_before_new_number(self):
        self.partial()
        self.retry(branch='feature/repaired')
        self.assertIn(('DELETE', '/releases/42'), self.api.writes)
        current = json.loads(self.api.attachments['build.json'])
        self.assertEqual(current['source_branch'], 'feature/repaired')
        self.assertEqual(current['version'], 'v2.23.1-custom.2')
        self.assertEqual(json.loads(self.api.annotations[self.row['version']]['message'])['source_branch'], 'dev')

    def test_same_prefix_different_full_sha_does_not_reuse_completed_version(self):
        self.retry()
        old_sha = 'a' * 40
        other_sha = 'a' * 8 + 'b' * 32
        self.retry(sha=other_sha)
        self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')
        self.assertEqual(self.api.annotations[self.row['version']]['commit']['sha'], old_sha)
        self.assertEqual(self.builder.call_args.args[1]['source_sha'], other_sha)

    def test_unknown_delete_result_is_not_replayed_after_replacement_build(self):
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
        self.assertEqual(self.builder.call_count, 2)

    def test_anonymous_failure_keeps_draft_without_marker_or_latest(self):
        with patch.object(release, 'public_registry', side_effect=RuntimeError('anonymous denied')):
            with self.assertRaisesRegex(RuntimeError, 'anonymous denied'):
                self.retry()
        self.assertTrue(self.api.entry['draft'])
        self.assertNotIn(release.COMPLETE, self.api.attachments)
        self.assertFalse(any(method == 'PATCH' for method, _ in self.api.writes))
        release.latest.assert_not_called()

    def test_build_failure_never_deletes_unfinished(self):
        self.partial()
        with patch.object(release, 'build', side_effect=RuntimeError('replacement failed')):
            with self.assertRaisesRegex(RuntimeError, 'replacement failed'):
                self.retry()
        self.assertEqual(self.api.writes, [])
        self.assertIsNotNone(self.api.entry)

    def test_schema5_cannot_use_old_workflow_or_event(self):
        self.partial()
        self.api.old_run['workflow_id'] = release.LEGACY_WORKFLOW
        with self.assertRaisesRegex(ValueError, 'identity differs'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_schema5_recovers_original_event_with_whitespace(self):
        self.partial()
        payload = json.loads(self.api.old_run['event_payload'])
        payload['inputs'] = {'source_branch': ' dev ', 'source_sha': ' ' + self.row['source_sha'] + ' '}
        self.api.old_run['event_payload'] = json.dumps(payload)
        self.retry()
        self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')
        self.assertIn(('DELETE', '/releases/42'), self.api.writes)

    def test_schema5_recovers_original_event_with_empty_or_omitted_optional_sha(self):
        for inputs in ({'source_branch': ' dev ', 'source_sha': ' '}, {'source_branch': 'dev'}):
            with self.subTest(inputs=inputs):
                self.setUp()
                self.partial()
                owner = json.loads(self.api.annotation['message'])
                owner['release_inputs']['source_sha'] = ''
                self.api.annotation['message'] = json.dumps(owner)
                metadata = json.loads(self.api.attachments['build.json'])
                metadata['release_inputs']['source_sha'] = ''
                self.api.attachments['build.json'] = release.packed(metadata)
                self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': inputs})
                self.retry()
                self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')
                self.assertIn(('DELETE', '/releases/42'), self.api.writes)

    def test_schema5_changed_original_event_fields_never_delete(self):
        for inputs in ({'source_branch': 'main', 'source_sha': 'a'*40},
                       {'source_branch': 'dev', 'source_sha': 'b'*40},
                       {'source_branch': 'dev', 'source_sha': 'a'*40, 'request_id': self.row['request_id']}):
            with self.subTest(inputs=inputs):
                self.setUp()
                self.partial()
                self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': inputs})
                with self.assertRaises(ValueError):
                    self.retry()
                self.assertEqual(self.api.writes, [])
                self.assertEqual(self.builder.call_count, 1)

    def test_known_legacy_three_input_failed_attempt_remains_recoverable(self):
        self.partial()
        owner = json.loads(self.api.annotation['message'])
        for key in ('schema', 'source_branch_head', 'release_inputs', 'release_run_id', 'release_request_id'):
            owner.pop(key)
        self.api.annotation['message'] = json.dumps(owner)
        self.api.old_run['workflow_id'] = release.LEGACY_WORKFLOW
        self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': {
            'source_branch': owner['source_branch'], 'source_sha': owner['source_sha'], 'request_id': owner['request_id']}})
        metadata = json.loads(self.api.attachments['build.json'])
        for key in ('schema', *release.CONTEXT_KEYS):
            metadata.pop(key, None)
        self.api.attachments['build.json'] = release.packed(metadata)
        self.retry()
        self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')

    def test_legacy_three_input_event_whitespace_remains_strict(self):
        self.partial()
        owner = json.loads(self.api.annotation['message'])
        for key in ('schema', 'source_branch_head', 'release_inputs', 'release_run_id', 'release_request_id'):
            owner.pop(key)
        self.api.annotation['message'] = json.dumps(owner)
        self.api.old_run['workflow_id'] = release.LEGACY_WORKFLOW
        self.api.old_run['event_payload'] = json.dumps({'ref': 'refs/heads/main', 'inputs': {
            'source_branch': ' dev ', 'source_sha': owner['source_sha'], 'request_id': owner['request_id']}})
        with self.assertRaisesRegex(ValueError, 'identity differs'):
            self.retry()
        self.assertEqual(self.api.writes, [])

    def test_historical_no_schema_completed_raw_hash_reused_without_build(self):
        self.retry()
        metadata = json.loads(self.api.attachments['build.json'])
        for key in ('schema', *release.CONTEXT_KEYS):
            metadata.pop(key, None)
        raw = json.dumps(metadata, indent=3).encode()  # deliberately noncanonical original bytes
        self.api.attachments['build.json'] = raw
        self.api.attachments[release.COMPLETE] = release.packed(release.marker(metadata, release.digest(raw)))
        self.api.writes.clear()
        self.builder.reset_mock()
        self.retry()
        self.builder.assert_not_called()
        self.assertEqual(self.api.writes, [])
        self.assertEqual(self.api.attachments['build.json'], raw)

    def asset_identities(self):
        identities = {name: dict(id=index+1, uuid='asset-'+str(index), name=name, size=len(data),
                     type='application/octet-stream', browser_download_url=name, download_count=0)
                     for index, (name, data) in enumerate(self.api.attachments.items())}
        self.api.assets = lambda _: identities.copy()
        self.api.download = lambda asset: self.api.attachments[asset['browser_download_url']]
        return identities

    def test_fingerprint_ignores_download_growth_and_asset_order(self):
        self.partial()
        identities = self.asset_identities()
        first = release.unfinished_snapshot(self.api, self.row, '200-1')
        self.api.entry.update(download_count=42, archive_download_count=99)
        self.api.entry['author'].update(followers=40, repository_stats={'views': 100})
        self.api.entry['repository'] = {'archive_download_count': 123, 'owner': {'followers': 50}}
        self.api.package['owner'].update(followers=90, stats={'downloads': 100})
        self.api.package['creator']['followers'] = 80
        self.api.annotation['commit']['author'] = {'stats': {'commits': 42}}
        for asset in identities.values():
            asset['download_count'] += 100
            asset['archive_download_count'] = 200
            asset['uploader'] = {'followers': 20}
        identities = dict(reversed(list(identities.items())))
        self.api.assets = lambda _: identities.copy()
        self.assertEqual(first, release.unfinished_snapshot(self.api, self.row, '200-1'))
        release.clean_unfinished(self.api, self.row, '200-1', self.api.entry, self.api.package, first)
        self.assertEqual(self.api.writes[0], ('DELETE', '/releases/42'))

    def test_archive_download_count_growth_during_build_allows_full_recovery(self):
        self.partial()
        original_build = self.build
        def changed(directory, row, run_id):
            original_build(directory, row, run_id)
            self.api.entry['archive_download_count'] = 123
            self.api.entry['author']['followers'] = 100
            self.api.package['owner']['stats'] = {'downloads': 50}
        self.builder.side_effect = changed
        self.retry()
        self.assertEqual(self.api.entry['tag_name'], 'v2.23.1-custom.2')
        self.assertIn(('DELETE', '/releases/42'), self.api.writes)

    def test_stable_release_asset_package_and_tag_changes_never_delete(self):
        # Each stable field is independently changed *after* the replacement build.
        for field in ('body', 'name', 'draft', 'prerelease', 'release-id', 'release-uuid', 'author-id',
                      'asset-id', 'asset-uuid', 'asset-name', 'asset-size', 'asset-type', 'asset-hash',
                      'metadata-hash', 'package-id', 'package-owner-id', 'tag-id', 'tag-object',
                      'manifest-digest', 'event-source', 'original-run'):
            with self.subTest(field=field):
                self.setUp()
                self.partial()
                identities = self.asset_identities()
                self.api.package['id'] = 1
                original_build = self.build
                def changed(directory, row, run_id):
                    original_build(directory, row, run_id)
                    if field in ('body', 'name', 'draft', 'prerelease'):
                        self.api.entry[field] = 'changed'
                    elif field.startswith('release-'):
                        self.api.entry[field.removeprefix('release-')] = 'changed'
                    elif field == 'author-id':
                        self.api.entry['author']['id'] = 999
                    elif field.startswith('asset-'):
                        key = 'sha256' if field == 'asset-hash' else field.removeprefix('asset-')
                        next(iter(identities.values()))[key] = 'changed'
                    elif field == 'metadata-hash':
                        metadata = json.loads(self.api.attachments['build.json'])
                        metadata['built_at'] = '2025-03-01T01:00:00Z'
                        self.api.attachments['build.json'] = release.packed(metadata)
                    elif field == 'package-id':
                        self.api.package['id'] = 2
                    elif field == 'package-owner-id':
                        self.api.package['owner']['id'] = 999
                    elif field == 'tag-id':
                        self.api.annotation['id'] = 'changed'
                    elif field == 'manifest-digest':
                        release.inspect_registry.return_value = b'changed manifest bytes'
                    elif field == 'tag-object':
                        self.api.git = lambda *args: 'changed tag object'
                    elif field == 'event-source':
                        payload = json.loads(self.api.old_run['event_payload'])
                        payload['inputs']['source_sha'] = 'b'*40
                        self.api.old_run['event_payload'] = json.dumps(payload)
                    else:
                        self.api.old_run['status'] = 'running'
                self.builder.side_effect = changed
                with self.assertRaises(ValueError):
                    self.retry()
                self.assertEqual(self.api.writes, [])

    def test_partial_arch_without_build_digest_identity_is_not_adopted(self):
        self.partial()
        self.api.entry = None
        self.api.attachments.clear()
        self.api.package['version'] += '-arm64'
        with self.assertRaisesRegex(ValueError, 'Unknown unfinished image metadata'):
            self.retry()
        self.assertEqual(self.api.writes, [])


class WorkflowContractTests(unittest.TestCase):
    def test_sync_tool_setup_reads_lfs_flag_from_standalone_script_in_fresh_python(self):
        import yaml
        workflow = yaml.load((ROOT / '.forgejo/workflows/sync-github.yml').read_text(), Loader=yaml.BaseLoader)
        setup = next(step['run'] for step in workflow['jobs']['sync']['steps']
                     if step['name'] == 'Install sync tools')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts, tools = root / 'scripts', root / 'bin'
            scripts.mkdir()
            tools.mkdir()
            (tools / 'python3').symlink_to(sys.executable)
            apt = tools / 'apt-get'
            apt.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$INSTALL_LOG"\n')
            apt.chmod(0o755)
            # Match RawMark: the flag probe must need only this script, not sibling modules.
            source = (ROOT / 'scripts/sync-github.py').read_text()
            self.assertEqual(source.count("USE_LFS = 'false'"), 1)
            for flag in ('false', 'true'):
                with self.subTest(use_lfs=flag):
                    (scripts / 'sync-github.py').write_text(source.replace("USE_LFS = 'false'", f"USE_LFS = '{flag}'"))
                    log = root / f'install-{flag}.log'
                    result = subprocess.run(['/bin/bash', '-e', '-c', setup], cwd=root, capture_output=True,
                        text=True, env={'PATH': str(tools), 'HOME': str(root), 'INSTALL_LOG': str(log),
                                        'PYTHONNOUSERSITE': '1', 'PYTHONDONTWRITEBYTECODE': '1'})
                    self.assertEqual(result.returncode, 0, result.stderr)
                    installs = log.read_text().splitlines()
                    expected = ['update', 'install -y --no-install-recommends python3 git skopeo ca-certificates curl']
                    if flag == 'true':
                        expected.append('install -y --no-install-recommends git-lfs')
                    self.assertEqual(installs, expected)

    def test_web_contract_shared_serial_group_and_exact_cleanup(self):
        import yaml
        for filename, job_name in (('release.yml', 'release'), ('sync-github.yml', 'sync')):
            workflow = yaml.load((ROOT / '.forgejo/workflows' / filename).read_text(), Loader=yaml.BaseLoader)
            self.assertEqual(set(workflow['on']), {'workflow_dispatch'})
            self.assertEqual(workflow['concurrency'], {'group': 'sub2api-delivery', 'cancel-in-progress': 'false'})
            job = workflow['jobs'][job_name]
            self.assertEqual(job['runs-on'], 'docker')
            self.assertNotIn('runner.temp', json.dumps(job.get('env', {})))
            self.assertIn('main', job['if'])
            self.assertEqual(job['steps'][0]['with']['ref'], '${{ forgejo.sha }}')
            self.assertEqual(job['steps'][0]['with']['persist-credentials'], 'false')
            self.assertIn('always()', job['steps'][-1]['if'])
            self.assertIn('--cleanup', job['steps'][-1]['run'])
            if filename == 'release.yml':
                inputs = workflow['on']['workflow_dispatch']['inputs']
                self.assertEqual(set(inputs), {'source_branch', 'source_sha'})
                self.assertEqual(inputs['source_branch']['default'], 'main')
                self.assertEqual(inputs['source_sha']['required'], 'false')
            else:
                self.assertFalse(workflow['on']['workflow_dispatch'])
        for deleted in ('release.sh', 'sysnc-github.sh', '.forgejo/workflows/sub2api-manual-release.yml'):
            self.assertFalse((ROOT / deleted).exists())


if __name__ == '__main__':
    unittest.main()
