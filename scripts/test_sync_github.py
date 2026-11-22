"""Offline credential, distribution and fixed-User-Agent tests for scripts/sync-github.py."""
from contextlib import contextmanager, redirect_stderr, redirect_stdout
import io
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch


ROOT = Path(__file__).resolve().parents[1]
SOURCE = (ROOT / 'scripts/sync-github.py').read_text()


class SyncUserAgentTests(unittest.TestCase):
    def test_forgejo_helper_receives_fixed_user_agent_and_separate_authorization(self):
        module = {'__name__': 'sync_fixture'}
        with (patch.object(sys, 'argv', ['sync', str(ROOT)]), patch.object(sys, 'path', sys.path[:]),
              tempfile.TemporaryDirectory() as home, patch.dict(os.environ, {'HOME': home}, clear=True)):
            exec(compile(SOURCE, 'scripts/sync-github.py', 'exec'), module)
        calls = []

        @contextmanager
        def reply(url, auth, **kwargs):
            calls.append((url, auth, kwargs))
            yield 200, {}, io.BytesIO(b'{"ok": true}')

        with patch.dict(module, forgejo_request=reply):
            api = module['API'](module['FORGEJO_API'], 'token synthetic-forgejo')
            self.assertEqual(api.json('/user', 'fixture'), {'ok': True})
        self.assertEqual(len(calls), 1)
        url, auth, options = calls[0]
        self.assertTrue(url.endswith('/user'))
        self.assertEqual(auth, 'token synthetic-forgejo')
        self.assertEqual(options['headers']['User-Agent'], 'forgejo-init-manual-sync')
        self.assertNotIn('Authorization', options['headers'])


class SyncFixture(unittest.TestCase):
    def setUp(self):
        self.directory = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.dict(os.environ, {
            'HOME': str(self.directory), 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null',
            'GIT_CONFIG_COUNT': '0', 'GIT_TERMINAL_PROMPT': '0',
        }, clear=True))
        self.enterContext(patch.object(sys, 'argv', ['sync', str(ROOT)]))
        self.enterContext(patch.object(sys, 'path', sys.path[:]))
        self.module = {'__name__': 'sync_fixture'}
        exec(compile(SOURCE, 'scripts/sync-github.py', 'exec'), self.module)
        self.history = {'schema': 1, 'repositories': {'forgejo': self.module['FORGEJO_GIT'],
                        'github': self.module['GITHUB_GIT']}, 'archives': [],
                        'latest_transition': {'after_commit': 'a' * 40, 'aliases': {}}}
        self.module['delivery_history'] = lambda: self.history


class StandaloneSyncTests(SyncFixture):
    def test_imports_only_standard_library_modules(self):
        import ast
        tree = ast.parse(SOURCE)
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                names = [alias.name.split('.')[0] for alias in node.names]
            elif isinstance(node, ast.ImportFrom):
                self.assertEqual(node.level, 0)
                names = [node.module.split('.')[0]]
            else:
                continue
            self.assertTrue(set(names) <= sys.stdlib_module_names, names)

    def test_standalone_cleanup_cli_only_removes_matching_run_attempt(self):
        script = self.directory / 'sync-github.py'
        script.write_text(SOURCE)
        current = self.directory / 'sub2api-sync-100-2'
        other = self.directory / 'sub2api-sync-101-2'
        current.mkdir()
        other.mkdir()
        environment = {'PATH': os.defpath, 'HOME': str(self.directory),
                       'RUNNER_TEMP': str(self.directory), 'CI_RUN_ID': '100', 'CI_RUN_ATTEMPT': '2',
                       'PYTHONNOUSERSITE': '1', 'PYTHONDONTWRITEBYTECODE': '1'}
        result = subprocess.run([sys.executable, '-B', str(script), '--cleanup'], cwd=self.directory,
                                capture_output=True, text=True, env=environment)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(current.exists())
        self.assertTrue(other.exists())
        environment['CI_RUN_ATTEMPT'] = '../other'
        result = subprocess.run([sys.executable, '-B', str(script), '--cleanup'], cwd=self.directory,
                                capture_output=True, text=True, env=environment)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(other.exists())

    def test_missing_secrets_fail_before_any_remote_operation(self):
        with patch.object(subprocess, 'run') as process:
            with self.assertRaisesRegex(RuntimeError, 'Missing Actions Secret'):
                self.module['main'](self.directory)
        process.assert_not_called()

    def test_scoped_credentials_anonymous_login_and_finally_cleanup(self):
        os.environ.update(RUNNER_TEMP=str(self.directory), CI_RUN_ID='100', CI_RUN_ATTEMPT='2',
                          FORGEJO_TOKEN='synthetic-forgejo', GITHUB_TOKEN='synthetic-github')
        other = self.directory / 'sub2api-sync-101-2'
        other.mkdir()
        before = dict(os.environ)
        path = self.module['path_for']('sync')
        tokens = {self.module['FORGEJO_GIT']: 'synthetic-forgejo',
                  self.module['GITHUB_GIT']: 'synthetic-github'}
        with self.assertRaisesRegex(RuntimeError, 'synthetic cancellation'):
            with self.module['credentials']('sync', tokens) as auth:
                self.assertEqual(path.stat().st_mode & 0o777, 0o700)
                self.assertEqual((path / 'credentials.json').stat().st_mode & 0o777, 0o600)
                self.assertEqual(Path(auth).stat().st_mode & 0o777, 0o600)
                for url, expected in ((self.module['FORGEJO_GIT'], True), (self.module['GITHUB_GIT'], True),
                                      ('https://git.134333.xyz/eezd/other.git', False),
                                      (self.module['FORGEJO_GIT'] + '/child', False)):
                    result = subprocess.run(['git', 'credential', 'fill'], input='url='+url+'\n\n',
                        text=True, capture_output=True, env=self.module['child_env']())
                    self.assertEqual(result.returncode == 0, expected)
                    self.assertEqual('synthetic-' in result.stdout, expected)
                authenticated_home = os.environ['HOME']
                with self.module['anonymous']() as empty:
                    self.assertNotEqual(os.environ['HOME'], authenticated_home)
                    self.assertEqual(json.loads(Path(empty).read_text()), {'auths': {}})
                    self.assertFalse(set(self.module['TOKENS']) & os.environ.keys())
                    self.assertNotIn('CI_GIT_HELPER', os.environ)
                self.assertFalse(Path(empty).exists())
                self.assertEqual(os.environ['HOME'], authenticated_home)
                with patch.object(subprocess, 'run') as process:
                    self.module['login'](auth, 'ghcr.io', 'eezd', 'synthetic-github')
                self.assertEqual(process.call_args.kwargs['input'], 'synthetic-github')
                self.assertNotIn('synthetic-github', ' '.join(process.call_args.args[0]))
                self.assertFalse(set(self.module['TOKENS']) & process.call_args.kwargs['env'].keys())
                raise RuntimeError('synthetic cancellation')
        self.assertEqual(dict(os.environ), before)
        self.assertFalse(path.exists())
        self.module['cleanup_auth']('sync')  # workflow always() is a safe second cleanup
        self.assertTrue(other.exists())
        os.environ['CI_RUN_ATTEMPT'] = '../other'
        with self.assertRaisesRegex(ValueError, 'Invalid CI run/attempt'):
            self.module['cleanup_auth']('sync')
        self.assertTrue(other.exists())

    def test_embedded_forgejo_transport_keeps_token_on_stdin_and_never_replays(self):
        token = 'synthetic-forgejo'
        os.environ['FORGEJO_TOKEN'] = token
        def reply(args, **kwargs):
            Path(args[args.index('--dump-header') + 1]).write_bytes(b'HTTP/2 403 Forbidden\r\n\r\n')
            kwargs['stdout'].write(b'denied')
            self.assertNotIn(token, ' '.join(args))
            self.assertNotIn('--location', args)
            self.assertIn(b'Authorization: token synthetic-forgejo', kwargs['input'])
            self.assertNotIn('FORGEJO_TOKEN', kwargs['env'])
            return MagicMock(returncode=0)
        with patch.object(subprocess, 'run', side_effect=reply) as process:
            api = self.module['API'](self.module['FORGEJO_API'], 'token ' + token)
            with self.assertRaisesRegex(RuntimeError, 'HTTP 403'):
                api.json('/user', 'fixture')
        self.assertEqual(process.call_count, 1)


class SyncGitAuthTests(SyncFixture):
    def setUp(self):
        super().setUp()
        self.project = self.directory / 'project'
        self.source = self.directory / 'source.git'
        self.git('init', str(self.project))
        self.git('init', '--bare', str(self.source))
        self.git('-C', str(self.project), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 'commit', '--allow-empty', '-m', 'fixture')
        self.git('-C', str(self.project), 'push', str(self.source), 'HEAD:refs/heads/main')
        self.module.update(FORGEJO_GIT=str(self.source), GITHUB_GIT=str(self.source), BRANCHES=['main'])

    def git(self, *args, input=None):
        return subprocess.run(['git', *args], input=input, capture_output=True, text=True, check=True).stdout

    def test_snapshot_never_inherits_project_credentials_or_hooks(self):
        for key, value in (('credential.helper', '/must-not-inherit'), ('core.hooksPath', '/must-not-inherit')):
            self.git('-C', str(self.project), 'config', key, value)
        snapshot = self.module['Snapshot'](self.directory / 'snapshot.git', project=self.project)
        for key in ('credential.helper', 'core.hooksPath'):
            result = subprocess.run(['git', '-C', str(snapshot.directory), 'config', '--local', '--get', key], capture_output=True)
            self.assertEqual(result.returncode, 1)
        self.assertIn('refs/heads/main', snapshot.target())

    def test_no_project_git_config_still_creates_snapshot(self):
        standalone = self.directory / 'standalone'
        standalone.mkdir()
        snapshot = self.module['Snapshot'](self.directory / 'snapshot.git', project=standalone)
        self.assertIn('refs/heads/main', snapshot.heads)


class SyncCustomVersionTests(SyncFixture):
    def test_new_custom_version_validates_and_copies_six_packages_and_oci(self):
        self.validate_custom_version(5)

    def test_legacy_no_schema_completed_version_keeps_original_sync_contract(self):
        self.validate_custom_version(None)

    def validate_custom_version(self, schema):
        tag = 'v2.22.0-custom.20'
        sha = 'a' * 40
        packages = {name: ('synthetic-package-' + name).encode() for name in self.module['package_names'](tag)}
        hashes = {name: hashlib.sha256(data).hexdigest() for name, data in packages.items()}
        sums = ''.join(f'{value}  {name}\n' for name, value in sorted(hashes.items())).encode()
        image = dict(ref=self.module['FORGEJO_IMAGE'] + ':' + tag, digest='sha256:' + 'c' * 64,
                     platforms={platform: 'sha256:' + value * 64
                                for platform, value in zip(self.module['PLATFORMS'], ('d', 'e'))})
        metadata = dict(version=tag, source_sha=sha, source_branch='main', image=image,
                        packages=[dict(name=name, sha256=value) for name, value in hashes.items()])
        if schema == 5:
            metadata.update(schema=5, source_branch_head=sha, control_sha='b' * 40,
                            release_inputs={'source_branch': 'main', 'source_sha': ''},
                            release_request_id='12345678-1234-1234-1234-123456789abc', release_run_id='1-1')
        build = json.dumps(metadata).encode()
        contents = packages | {'SHA256SUMS': sums, 'build.json': build, 'release-complete.json': json.dumps(dict(
            version=tag, source_sha=sha, build_sha256=hashlib.sha256(build).hexdigest())).encode()}
        entry = dict(tag_name=tag, name=tag, body='synthetic release', draft=False, prerelease=False,
                     assets=[{'name': name} for name in contents])
        source, target, snapshot = MagicMock(), MagicMock(), MagicMock()
        snapshot.commit.return_value = sha

        def asset(metadata, path):
            content = contents[metadata['name']]
            if path:
                path.write_bytes(content)
            return hashlib.sha256(content).hexdigest()

        source.forgejo_asset.side_effect = asset
        target.github_asset.side_effect = asset
        target.upload.side_effect = lambda _release, name, _path: {'name': name}
        target.json.side_effect = [dict(entry, id=1, draft=True), dict(entry, id=1)]
        verify_image, process = MagicMock(return_value=False), MagicMock()
        with patch.dict(self.module, auth_file=lambda: str(self.directory / 'auth.json'),
                        verify_image=verify_image, public_image=MagicMock(), run=process), redirect_stdout(io.StringIO()):
            version = self.module['Version'](tag, entry, self.directory, source, target, snapshot, None)
            version.sync(target)
        self.assertEqual(target.upload.call_count, 8)  # six packages + sums + build; no Forgejo-only marker
        self.assertEqual({call.args[1] for call in target.upload.call_args_list}, set(contents) - {'release-complete.json'})
        self.assertIn('ghcr.io/eezd/sub2api:' + tag, version.target_image)
        self.assertEqual(process.call_count, 1)
        self.assertIn('--all', process.call_args.args[0])
        self.assertIn('--preserve-digests', process.call_args.args[0])
        snapshot.commit.assert_called_once_with('refs/tags/' + tag)

    def test_legacy_missing_marker_is_reported_nonzero_and_bare_custom_tag_not_pushed(self):
        legacy, bare = 'v2.22.0-custom.1', 'v2.22.0-custom.2'
        source, target, snapshot = MagicMock(), MagicMock(), MagicMock()
        target.json.side_effect = [dict(full_name='eezd/sub2api', private=False, archived=False,
                                        permissions={'push': True}), {'enabled': False}]
        source.json.return_value = {'permissions': {'push': True}}
        source.pages.return_value = [dict(tag_name=legacy, draft=False, prerelease=False, assets=[])]
        target.pages.return_value = []
        snapshot.refs = {'refs/heads/main': 'a' * 40, 'refs/tags/' + legacy: 'b' * 40,
                         'refs/tags/' + bare: 'c' * 40}
        snapshot.heads = {'refs/heads/main': 'a' * 40}
        snapshot.in_scope.return_value = True
        snapshot.target.return_value = snapshot.refs
        snapshot.changes.return_value = []
        output = io.StringIO()
        version = MagicMock()
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=version), redirect_stdout(output):
            with self.assertRaisesRegex(RuntimeError, '仍有 1 个正式版本未同步'):
                self.module['sync'](self.directory, source, target)
        self.assertIn('缺少适用完成证据', output.getvalue())
        self.assertIn('跳过尚无验收 Release', output.getvalue())
        version.assert_not_called()
        source.forgejo_asset.assert_not_called()
        snapshot.git.assert_not_called()
        self.assertEqual(snapshot.changes.call_args.args[0], snapshot.heads)


class SyncWebBoundaryTests(SyncFixture):
    def test_ghcr_anonymous_failure_leaves_draft_with_specific_public_instructions(self):
        cls = self.module['Version']
        version = cls.__new__(cls)
        version.tag = version.title = 'v2.23.1-custom.1'
        version.body = 'fixture'
        version.target = dict(tag_name=version.tag, name=version.title, body=version.body,
                              draft=True, prerelease=False)
        version.target_path = '/releases/1'
        version.missing = set()
        version.image_exists = True
        version.image = {}
        version.authfile = 'auth'
        version.target_image = 'ghcr.io/eezd/sub2api:' + version.tag
        target = MagicMock()
        def verify(ref, image, authfile, **kwargs):
            if authfile != 'auth':
                raise RuntimeError('anonymous denied')
            return True
        with patch.dict(self.module, verify_image=verify):
            with self.assertRaisesRegex(RuntimeError, 'https://github.com/users/eezd/packages/container/sub2api/settings'):
                version.sync(target)
        self.assertTrue(version.target['draft'])
        target.json.assert_not_called()

    def test_latest_uses_index_only_forgejo_all_ghcr_and_both_public_checks(self):
        old_raw, new_raw = b'old-index', b'new-index'
        def record(sha, version, raw, branch='main'):
            return dict(source_sha=sha, version=version, source_branch=branch, schema=5,
                        image=dict(ref=self.module['FORGEJO_IMAGE']+':'+version,
                                   digest='sha256:'+hashlib.sha256(raw).hexdigest()))
        old = record('a', 'v2.23.1-custom.1', old_raw)
        new = record('b', 'v2.23.1-custom.2', new_raw)
        dev = record('c', 'v2.23.1-custom.99', b'dev', 'dev')
        versions = [MagicMock(metadata=row) for row in (old, new, dev)]
        snapshot = MagicMock(heads={'refs/heads/main': 'b'})
        snapshot.ancestor.side_effect = lambda a, b: a == b or (a, b) == ('a', 'b')
        process, public = MagicMock(), MagicMock()
        with patch.dict(self.module, auth_file=lambda: 'auth', read_manifest=MagicMock(return_value=old_raw),
                        run=process, public_image=public):
            self.module['sync_latest'](versions, snapshot)
        self.assertEqual(process.call_count, 2)
        self.assertIn('--multi-arch=index-only', process.call_args_list[0].args[0])
        self.assertIn('--all', process.call_args_list[1].args[0])
        self.assertTrue(all('--preserve-digests' in call.args[0] for call in process.call_args_list))
        self.assertEqual(public.call_count, 3)  # source + both destinations; function checks auth+anonymous
        # Both aliases are inspected before writes; unknown GHCR stops *all* updates.
        process.reset_mock()
        with patch.dict(self.module, auth_file=lambda: 'auth', read_manifest=MagicMock(side_effect=[old_raw, b'unknown']),
                        run=process, public_image=public):
            with self.assertRaisesRegex(ValueError, 'Unknown'):
                self.module['sync_latest'](versions, snapshot)
        process.assert_not_called()

    def sync_entry_fixture(self, records):
        source, target, snapshot = MagicMock(), MagicMock(), MagicMock()
        target.json.side_effect = [dict(full_name='eezd/sub2api', private=False, archived=False,
                                        permissions={'push': True}), {'enabled': False}]
        source.json.return_value = {'permissions': {'push': True}}
        source.pages.return_value = [dict(tag_name=row['version'], draft=False, prerelease=False,
            assets=[{'name': 'release-complete.json'}]) for row in records]
        target.pages.return_value = []
        snapshot.heads = {'refs/heads/main': 'head'}
        snapshot.refs = snapshot.heads | {'refs/tags/'+row['version']: row['source_sha'] for row in records}
        snapshot.in_scope.return_value = True
        snapshot.target.return_value = snapshot.refs
        snapshot.changes.return_value = ['refs/heads/main:refs/heads/main']
        versions = [MagicMock(tag=row['version'], metadata=row) for row in records]
        constructor = MagicMock(side_effect=versions)
        return source, target, snapshot, versions, constructor

    def latest_record(self, sha, number, raw):
        version = 'v2.23.1-custom.'+str(number)
        return dict(schema=5, source_branch='main', source_sha=sha, version=version,
                    image=dict(ref=self.module['FORGEJO_IMAGE']+':'+version,
                               digest='sha256:'+hashlib.sha256(raw).hexdigest()))

    def assert_no_sync_writes(self, source, target, snapshot, versions, process):
        snapshot.lfs.assert_not_called()
        snapshot.git.assert_not_called()
        target.upload.assert_not_called()
        source.upload.assert_not_called()
        self.assertEqual(target.json.call_count, 2)  # repository and Actions permission reads only
        for call in target.json.call_args_list:
            self.assertEqual(len(call.args), 2)
            self.assertNotIn('method', call.kwargs)
        for version in versions:
            version.sync.assert_not_called()
        process.assert_not_called()  # no Skopeo copy or subprocess writes

    def test_sync_entry_unknown_alias_on_either_registry_stops_before_every_target_write(self):
        record = self.latest_record('head', 1, b'known')
        for aliases in ([b'unknown-forgejo', None], [None, b'unknown-ghcr']):
            with self.subTest(aliases=aliases):
                source, target, snapshot, versions, constructor = self.sync_entry_fixture([record])
                snapshot.ancestor.side_effect = lambda a, b: a == b
                process = MagicMock()
                manifests = MagicMock(side_effect=aliases)
                with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                                USE_LFS='true', auth_file=lambda: 'auth', read_manifest=manifests,
                                run=process), redirect_stdout(io.StringIO()):
                    with self.assertRaisesRegex(ValueError, 'Unknown'):
                        self.module['sync'](self.directory, source, target)
                self.assertEqual(manifests.call_count, 2)
                self.assert_no_sync_writes(source, target, snapshot, versions, process)

    def test_sync_entry_forked_completed_main_stops_before_every_target_write(self):
        records = [self.latest_record('a', 1, b'a'), self.latest_record('b', 2, b'b')]
        source, target, snapshot, versions, constructor = self.sync_entry_fixture(records)
        snapshot.ancestor.side_effect = lambda a, b: a == b or b == 'head'
        process, manifests = MagicMock(), MagicMock()
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        USE_LFS='true', auth_file=lambda: 'auth', read_manifest=manifests,
                        run=process), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'diverge'):
                self.module['sync'](self.directory, source, target)
        manifests.assert_not_called()
        self.assert_no_sync_writes(source, target, snapshot, versions, process)

    def test_sync_entry_rechecks_both_aliases_after_versions_before_latest_write(self):
        record = self.latest_record('head', 1, b'known')
        source, target, snapshot, versions, constructor = self.sync_entry_fixture([record])
        snapshot.ancestor.side_effect = lambda a, b: a == b
        process = MagicMock()
        manifests = MagicMock(side_effect=[None, None, None, b'changed-to-unknown'])
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        USE_LFS='true', auth_file=lambda: 'auth', read_manifest=manifests,
                        run=process), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'Unknown'):
                self.module['sync'](self.directory, source, target)
        self.assertEqual(manifests.call_count, 4)
        snapshot.lfs.assert_called_once()
        snapshot.git.assert_called_once()
        versions[0].sync.assert_called_once_with(target)
        process.assert_not_called()  # recheck failed before either latest copy

    def bootstrap_history(self, raw):
        return {**self.history, 'latest_transition': {'after_commit': 'a' * 40, 'aliases': {
            self.module['FORGEJO_IMAGE'] + ':latest': 'sha256:' + hashlib.sha256(raw).hexdigest(),
            'ghcr.io/eezd/sub2api:latest': 'sha256:' + hashlib.sha256(raw).hexdigest()}}}

    def test_sync_entry_bootstraps_both_frozen_endpoints_then_rechecks_before_copy(self):
        raw = b'synthetic-legacy'
        row = self.latest_record('b' * 40, 1, b'new')
        source, target, snapshot, versions, constructor = self.sync_entry_fixture([row])
        snapshot.ancestor.side_effect = lambda a, b: a == b or a == 'a' * 40 or b == 'head'
        events = []
        versions[0].sync.side_effect = lambda _: events.append('complete')
        manifests = MagicMock(side_effect=lambda *a, **kw: events.append('inspect') or raw)
        process = MagicMock(side_effect=lambda *a, **kw: events.append('copy'))
        with patch.dict(self.module, delivery_history=lambda: self.bootstrap_history(raw)), \
             patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        auth_file=lambda: 'auth', read_manifest=manifests, run=process,
                        public_image=MagicMock()), redirect_stdout(io.StringIO()):
            self.module['sync'](self.directory, source, target)
        self.assertEqual(manifests.call_count, 4)
        self.assertEqual(process.call_count, 2)
        self.assertEqual([call.args[0] for call in manifests.call_args_list],
                         [self.module['FORGEJO_IMAGE'] + ':latest', 'ghcr.io/eezd/sub2api:latest'] * 2)
        self.assertEqual(events, ['inspect', 'inspect', 'complete', 'inspect', 'inspect', 'copy', 'copy'])
        self.assertIn('--multi-arch=index-only', process.call_args_list[0].args[0])
        self.assertIn('--all', process.call_args_list[1].args[0])

    def test_sync_entry_second_main_closes_bootstrap_before_target_writes(self):
        raw = b'synthetic-legacy'
        records = [self.latest_record('b' * 40, 1, b'first'), self.latest_record('c' * 40, 2, b'second')]
        source, target, snapshot, versions, constructor = self.sync_entry_fixture(records)
        snapshot.ancestor.side_effect = lambda a, b: a == b or a == 'a' * 40 or b in ('head', 'c' * 40)
        process = MagicMock()
        with patch.dict(self.module, delivery_history=lambda: self.bootstrap_history(raw)), \
             patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        USE_LFS='true', auth_file=lambda: 'auth', read_manifest=MagicMock(return_value=raw),
                        run=process), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'Unknown'):
                self.module['sync'](self.directory, source, target)
        self.assert_no_sync_writes(source, target, snapshot, versions, process)

    def test_sync_entry_bootstrap_checks_skipped_completions_without_syncing_them(self):
        raw = b'synthetic-legacy'
        for kind in ('second-main', 'fork-main', 'invalid-proof', 'dev', 'old-schema', 'schema4',
                     'old-schema-fork', 'schema4-fork'):
            with self.subTest(kind=kind):
                row = self.latest_record('b' * 40, 1, b'first')
                source, target, snapshot, versions, constructor = self.sync_entry_fixture([row])
                skipped = self.latest_record('c' * 40, 2, b'skipped')
                skipped.update(source_branch='dev' if kind == 'dev' else 'main',
                               source_branch_head='c' * 40, control_sha='d' * 40,
                               release_inputs={'source_branch': 'dev' if kind == 'dev' else 'main', 'source_sha': ''},
                               release_request_id='12345678-1234-1234-1234-123456789abc', release_run_id='1-1')
                if kind in ('old-schema', 'old-schema-fork'):
                    skipped.pop('schema')
                if kind in ('schema4', 'schema4-fork'):
                    skipped['schema'] = 4
                if kind in ('old-schema', 'schema4'):
                    skipped['source_sha'] = 'a' * 40  # Older main ancestor, not another bootstrap candidate.
                build = json.dumps(skipped).encode()
                marker = json.dumps(dict(version=skipped['version'], source_sha=skipped['source_sha'],
                    build_sha256='bad' if kind == 'invalid-proof' else hashlib.sha256(build).hexdigest())).encode()
                payloads = {'build.json': build, 'release-complete.json': marker}
                releases = list(source.pages.return_value)
                releases.append(dict(tag_name=skipped['version'], draft=False, prerelease=False,
                                     assets=[{'name': name} for name in payloads]))
                source.pages.return_value = releases
                skipped_ref = 'refs/tags/' + skipped['version']
                snapshot.refs[skipped_ref] = skipped['source_sha']
                snapshot.in_scope.side_effect = lambda ref: ref != skipped_ref
                snapshot.commit.return_value = skipped['source_sha']
                snapshot.ancestor.side_effect = lambda a, b: a == b or (
                    a == 'a' * 40 and (b != 'c' * 40 or kind != 'fork-main')) or (
                    a == 'b' * 40 and b == 'head')
                def download(asset, path):
                    data = payloads[asset['name']]
                    path.write_bytes(data)
                    return hashlib.sha256(data).hexdigest()
                source.forgejo_asset.side_effect = download
                process = MagicMock()
                with patch.dict(self.module, delivery_history=lambda: self.bootstrap_history(raw)), \
                     patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                                USE_LFS='true', auth_file=lambda: 'auth', read_manifest=MagicMock(return_value=raw),
                                public_image=MagicMock(), run=process), redirect_stdout(io.StringIO()):
                    if kind in ('dev', 'old-schema', 'schema4'):
                        self.module['sync'](self.directory, source, target)
                        versions[0].sync.assert_called_once_with(target)
                        self.assertEqual(process.call_count, 2)
                        self.assertEqual(source.forgejo_asset.call_count, 4)  # preflight and write-time recheck
                    else:
                        with self.assertRaises((ValueError, RuntimeError)):
                            self.module['sync'](self.directory, source, target)
                        self.assert_no_sync_writes(source, target, snapshot, versions, process)
                constructor.assert_called_once()  # skipped proof never becomes a synchronizable Version
                self.assertEqual({call.args[0]['name'] for call in source.forgejo_asset.call_args_list}, set(payloads))
                self.assertEqual(list(self.directory.iterdir()), [])  # temporary metadata only

    def test_sync_entry_all_alias_states_check_skipped_main_forks_before_writes(self):
        for alias in ('known-ancestor', 'selected', 'missing'):
            for schema in (None, 4, 5):
                with self.subTest(alias=alias, schema=schema):
                    old_raw, selected_raw = b'known-old', b'selected'
                    rows = [self.latest_record('a' * 40, 1, old_raw),
                            self.latest_record('b' * 40, 2, selected_raw)]
                    source, target, snapshot, versions, constructor = self.sync_entry_fixture(rows)
                    skipped = self.latest_record('f' * 40, 3, b'fork')
                    if schema is None:
                        skipped.pop('schema')
                    else:
                        skipped['schema'] = schema
                    skipped_tag = skipped['version']
                    source.pages.return_value.append(dict(tag_name=skipped_tag, draft=False, prerelease=False,
                                                         assets=[{'name': 'release-complete.json'}]))
                    skipped_ref = 'refs/tags/' + skipped_tag
                    snapshot.refs[skipped_ref] = skipped['source_sha']
                    snapshot.in_scope.side_effect = lambda ref: ref != skipped_ref
                    snapshot.ancestor.side_effect = lambda a, b: a == b or (
                        a == 'a' * 40 and b in ('b' * 40, 'head')) or (a == 'b' * 40 and b == 'head')
                    raw = {'known-ancestor': old_raw, 'selected': selected_raw, 'missing': None}[alias]
                    process, proof = MagicMock(), MagicMock(return_value=(skipped, 'hash'))
                    with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                                    completed_metadata=proof, USE_LFS='true', auth_file=lambda: 'auth',
                                    read_manifest=MagicMock(return_value=raw), run=process), \
                         redirect_stdout(io.StringIO()):
                        with self.assertRaisesRegex(ValueError, 'diverge'):
                            self.module['sync'](self.directory, source, target)
                    proof.assert_called_once()
                    self.assertEqual(constructor.call_count, 2)  # Never construct a syncable skipped version.
                    self.assert_no_sync_writes(source, target, snapshot, versions, process)

    def test_sync_entry_rechecks_skipped_main_proofs_before_latest_writes(self):
        raw = b'selected'
        row = self.latest_record('b' * 40, 1, raw)
        source, target, snapshot, versions, constructor = self.sync_entry_fixture([row])
        skipped = self.latest_record('f' * 40, 2, b'fork')
        calls = MagicMock(side_effect=[[], [skipped]])
        snapshot.ancestor.side_effect = lambda a, b: a == b or (a == 'b' * 40 and b == 'head')
        process = MagicMock()
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        skipped_completion_records=calls, auth_file=lambda: 'auth',
                        read_manifest=MagicMock(return_value=raw), run=process), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'diverge'):
                self.module['sync'](self.directory, source, target)
        self.assertEqual(calls.call_count, 2)
        versions[0].sync.assert_called_once_with(target)
        process.assert_not_called()

    def test_sync_entry_dev_never_reads_or_updates_latest(self):
        row = dict(self.latest_record('b' * 40, 1, b'dev'), source_branch='dev')
        source, target, snapshot, versions, constructor = self.sync_entry_fixture([row])
        manifests, process = MagicMock(), MagicMock()
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot), Version=constructor,
                        read_manifest=manifests, run=process), redirect_stdout(io.StringIO()):
            self.module['sync'](self.directory, source, target)
        versions[0].sync.assert_called_once_with(target)
        manifests.assert_not_called()
        process.assert_not_called()

    def test_valid_version_syncs_but_legacy_gap_still_nonzero_and_no_latest(self):
        source, target, snapshot = MagicMock(), MagicMock(), MagicMock()
        target.json.side_effect = [dict(full_name='eezd/sub2api', private=False, archived=False,
                                        permissions={'push': True}), {'enabled': False}]
        source.json.return_value = {'permissions': {'push': True}}
        valid, legacy = 'v2.23.1-custom.1', 'v2.22.0-custom.1'
        source.pages.return_value = [dict(tag_name=tag, draft=False, prerelease=False,
            assets=[{'name': 'release-complete.json'}] if tag == valid else []) for tag in (valid, legacy)]
        target.pages.return_value = []
        snapshot.refs = dict({'refs/heads/main': 'a'*40}, **{'refs/tags/'+tag: 'a'*40 for tag in (valid, legacy)})
        snapshot.heads = {'refs/heads/main': 'a'*40}
        snapshot.in_scope.return_value = True
        snapshot.target.return_value = snapshot.refs
        snapshot.changes.return_value = []
        version, latest = MagicMock(), MagicMock()
        with patch.dict(self.module, Snapshot=MagicMock(return_value=snapshot),
                        Version=MagicMock(return_value=version), sync_latest=latest), redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(RuntimeError, '仍有 1 个正式版本未同步'):
                self.module['sync'](self.directory, source, target)
        version.sync.assert_called_once_with(target)
        latest.assert_not_called()


class SyncArchiveTests(SyncFixture):
    def setUp(self):
        super().setUp()
        self.tag = 'v2.12.0-custom.1'
        self.ref = 'refs/tags/' + self.tag
        self.contents = {f'legacy-{n}': f'archive bytes {n}'.encode() for n in range(8)}
        self.archive = dict(tag=self.tag, tag_object='a' * 40, commit='b' * 40, name='Frozen release',
                            body_sha256=hashlib.sha256(b'frozen body').hexdigest(), forgejo_id=1, github_id=2,
                            assets=[dict(name=name, size=len(data), sha256=hashlib.sha256(data).hexdigest(),
                                    forgejo_id=n + 10, forgejo_uuid=f'frozen-uuid-{n}', github_id=n + 20)
                                    for n, (name, data) in enumerate(self.contents.items())])
        self.history['archives'] = [self.archive]
        self.source, self.target, self.snapshot = MagicMock(), MagicMock(), MagicMock()
        self.source.json.return_value = {'permissions': {'push': True}}
        self.target.json.side_effect = lambda path, phase, **kw: (
            {'full_name': 'eezd/sub2api', 'private': False, 'archived': False, 'permissions': {'push': True}}
            if path == '' else {'enabled': False})
        self.source_release = dict(tag_name=self.tag, id=1, name='Frozen release', body='frozen body',
                                   draft=False, prerelease=False, assets=[])
        self.target_release = dict(self.source_release, id=2)
        self.source_assets = [dict(name=a['name'], size=a['size'], id=a['forgejo_id'], uuid=a['forgejo_uuid'])
                              for a in self.archive['assets']]
        self.target_assets = [dict(name=a['name'], size=a['size'], id=a['github_id'], state='uploaded',
                              digest='sha256:' + a['sha256']) for a in self.archive['assets']]
        self.source_release['assets'] = self.source_assets
        self.target_release['assets'] = self.target_assets
        self.source_releases, self.target_releases = [self.source_release], [self.target_release]
        self.source.json.side_effect = lambda path, *a, **kw: (
            {'permissions': {'push': True}} if path == '' else self.source_assets)
        self.source.pages.side_effect = lambda path, *a, **kw: self.source_releases if path == '/releases' else (_ for _ in ()).throw(
            AssertionError('Forgejo attachments are not paginated'))
        self.target.pages.side_effect = lambda path, *a, **kw: self.target_releases if path == '/releases' else self.target_assets
        self.snapshot.heads = {'refs/heads/main': 'head'}
        self.snapshot.refs = self.snapshot.heads | {self.ref: self.archive['tag_object']}
        self.dest_refs = dict(self.snapshot.refs)
        self.snapshot.target.side_effect = lambda: self.dest_refs
        self.snapshot.commit.return_value = self.archive['commit']
        self.snapshot.in_scope.return_value = True
        self.snapshot.changes.return_value = []
        self.version, self.process = MagicMock(), MagicMock()
        self.enterContext(patch.dict(self.module, Snapshot=MagicMock(return_value=self.snapshot),
                                    Version=self.version, run=self.process))
        # Exercise the production streaming hash loop, with no retained archive file.
        @contextmanager
        def opened(url, *args, **kwargs):
            yield io.BytesIO(self.contents[url])
        self.source.open.side_effect = opened
        self.source.forgejo_asset.side_effect = lambda asset, path: self.module['API'].download(
            self.source, asset['name'], path, 'synthetic stream')

    def run_sync(self):
        with redirect_stdout(io.StringIO()) as output:
            self.module['sync'](self.directory, self.source, self.target)
        return output.getvalue()

    def assert_no_target_writes(self):
        self.snapshot.lfs.assert_not_called()
        self.snapshot.git.assert_not_called()
        self.target.upload.assert_not_called()
        self.process.assert_not_called()
        self.version.assert_not_called()
        for call in self.target.json.call_args_list:
            self.assertNotIn('method', call.kwargs)

    def test_archive_passes_read_only_without_version_or_old_release_writes(self):
        output = self.run_sync()
        self.assertIn('1 个版本 / 8 个附件', output)
        self.assertIn('不作为新完成证明', output)
        self.assertIn('已验收 0 个正式版本', output)
        self.assertEqual(self.source.forgejo_asset.call_count, 8)
        self.assertTrue(all(call.args[1] is None for call in self.source.forgejo_asset.call_args_list))
        self.target.github_asset.assert_not_called()  # GitHub API digest is the frozen authority.
        self.assertEqual(list(self.directory.iterdir()), [])
        self.assert_no_target_writes()

    def test_forgejo_unpaginated_assets_are_requested_once(self):
        self.source.root = 'https://forgejo.invalid/api/repos/project'
        @contextmanager
        def opened(url, *args, **kwargs):
            if url.startswith(self.source.root + '/releases/1/assets'):
                # Real Forgejo 17 ignores page/limit and repeats this list on every page.
                data = json.dumps(self.source_assets).encode()
            elif url == self.source.root:
                data = json.dumps({'permissions': {'push': True}}).encode()
            else:
                data = self.contents[url]
            response = io.BytesIO(data)
            response.headers = {}
            yield response
        self.source.open.side_effect = opened
        self.source.json.side_effect = lambda path, phase, **kw: self.module['API'].json(self.source, path, phase, **kw)
        self.source.pages.side_effect = lambda path, phase, **kw: (
            self.source_releases if path == '/releases' else self.module['API'].pages(self.source, path, phase, **kw))
        self.run_sync()
        urls = [call.args[0] for call in self.source.open.call_args_list if '/assets' in call.args[0]]
        self.assertEqual(urls, [self.source.root + '/releases/1/assets'])
        self.target.pages.assert_any_call('/releases/2/assets', '核对历史附件', size_key='per_page')
        self.assert_no_target_writes()

    def test_any_archive_metadata_tag_set_or_bytes_mutation_stops_before_all_writes(self):
        import copy
        # A fresh fixture per mutation keeps failures independent.
        mutations = [
            lambda f: f.source_releases.clear(), lambda f: f.target_releases.clear(),
            lambda f: f.source_release.update(draft=True), lambda f: f.target_release.update(prerelease=True),
            lambda f: f.source_release.update(name='changed'), lambda f: f.target_release.update(body='changed'),
            lambda f: f.source_release.update(id=99), lambda f: f.target_release.update(id=99),
            lambda f: f.snapshot.refs.update({f.ref: 'c' * 40}),
            lambda f: f.dest_refs.update({f.ref: 'c' * 40}), lambda f: f.dest_refs.pop(f.ref),
            lambda f: setattr(f.snapshot.commit, 'return_value', 'c' * 40),
            lambda f: f.source_assets[0].update(size=99), lambda f: f.target_assets[0].update(size=99),
            lambda f: f.source_assets[0].update(uuid='changed'), lambda f: f.target_assets[0].update(id=99),
            lambda f: f.source_assets[0].update(id=99), lambda f: f.source_assets[0].update(name='changed'),
            lambda f: f.target_assets[0].update(digest='sha256:' + '0' * 64),
            lambda f: f.target_assets[0].pop('digest'), lambda f: f.source_assets.pop(),
            lambda f: f.target_assets.append(copy.deepcopy(f.target_assets[0])),
            lambda f: f.source_assets.append({'name': 'release-complete.json'}),
            lambda f: f.target_assets.append({'name': 'release-complete.json'}),
            lambda f: f.contents.update({'legacy-0': b'altered source bytes'}),
        ]
        for number, mutate in enumerate(mutations):
            with self.subTest(mutation=number):
                fixture = SyncArchiveTests('test_archive_passes_read_only_without_version_or_old_release_writes')
                try:
                    fixture.setUp()
                    mutate(fixture)
                    fixture.module['USE_LFS'] = 'true'
                    fixture.snapshot.changes.return_value = ['would-write']
                    with self.assertRaises((RuntimeError, KeyError)):
                        fixture.run_sync()
                    fixture.assert_no_target_writes()
                finally:
                    fixture.doCleanups()

    def test_archive_success_does_not_hide_unknown_gap(self):
        self.source_releases.append(dict(tag_name='v2.99.0-custom.1', draft=False, prerelease=False, assets=[]))
        with self.assertRaisesRegex(RuntimeError, '仍有 1 个正式版本未同步'):
            self.run_sync()
        self.version.assert_not_called()
        self.target.upload.assert_not_called()

    def test_archive_success_allows_new_version_sync_without_touching_archive(self):
        tag = 'v2.23.1-custom.1'
        self.source_releases.append(dict(tag_name=tag, draft=False, prerelease=False,
                                        assets=[{'name': 'release-complete.json'}]))
        self.snapshot.refs['refs/tags/' + tag] = 'c' * 40
        self.dest_refs['refs/tags/' + tag] = 'c' * 40
        version = MagicMock(tag=tag, metadata=dict(source_branch='dev', schema=5))
        self.version.return_value = version
        output = self.run_sync()
        self.version.assert_called_once()
        self.assertEqual(self.version.call_args.args[0], tag)
        version.sync.assert_called_once_with(self.target)
        self.assertIn('已验收 1 个正式版本', output)
        self.target.upload.assert_not_called()
        self.assertTrue(all('method' not in call.kwargs for call in self.target.json.call_args_list))


if __name__ == '__main__':
    unittest.main()
