"""Offline credential and fixed-User-Agent tests for sysnc-github.sh."""
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
SOURCE = (ROOT / 'sysnc-github.sh').read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]


class SyncCredentialTests(unittest.TestCase):
    def setUp(self):
        self.home = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.project = self.home / 'project'
        self.project.mkdir()
        self.enterContext(patch.dict(os.environ, {'HOME': str(self.home)}, clear=True))
        self.enterContext(patch.object(sys, 'argv', ['sync', str(ROOT)]))
        self.enterContext(patch.object(sys, 'path', sys.path[:]))
        self.module = {'__name__': 'sync_fixture'}
        exec(compile(SOURCE, 'sysnc-github.sh', 'exec'), self.module)
        self.api = MagicMock()
        self.sync = MagicMock()
        self.enterContext(patch.dict(self.module, API=self.api, sync=self.sync))

    def token_file(self, service, content):
        path = self.home / '.config' / service / 'token'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        path.chmod(0o600)
        return path

    def main(self, *, expected_error=None):
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            if expected_error:
                with self.assertRaisesRegex(RuntimeError, expected_error) as error:
                    self.module['main'](self.project)
                output = stdout.getvalue() + stderr.getvalue() + str(error.exception)
                self.api.assert_not_called()
                self.sync.assert_not_called()
                self.assertFalse((self.project / 'runtime').exists())
                return output
            self.module['main'](self.project)
        self.sync.assert_called_once()
        self.assertEqual(self.api.call_count, 2)
        return stdout.getvalue() + stderr.getvalue()

    def test_each_nonempty_environment_token_wins_without_reading_any_file(self):
        os.environ.update(FORGEJO_TOKEN='synthetic-env-forgejo', GITHUB_TOKEN='synthetic-env-github')
        with patch.object(Path, 'read_text', side_effect=AssertionError('must not read credentials')):
            output = self.main()
        self.assertEqual([call.args[1] for call in self.api.call_args_list],
                         ['token synthetic-env-forgejo', 'Bearer synthetic-env-github'])
        self.assertNotIn('synthetic-env-', output)

    def test_unset_or_empty_environment_uses_each_calling_users_token_file(self):
        self.token_file('forgejo', b' \nsynthetic-file-forgejo\t\n')
        self.token_file('github', b' \nsynthetic-file-github\t\n')
        for environment in ({}, {'FORGEJO_TOKEN': '', 'GITHUB_TOKEN': ''}):
            with self.subTest(environment=environment), patch.dict(os.environ, environment):
                self.api.reset_mock()
                self.sync.reset_mock()
                output = self.main()
                self.assertEqual([call.args[1] for call in self.api.call_args_list],
                                 ['token synthetic-file-forgejo', 'Bearer synthetic-file-github'])
                self.assertNotIn('synthetic-file-', output)

    def test_each_token_can_independently_mix_environment_and_file_sources(self):
        for env_service, file_service in (('forgejo', 'github'), ('github', 'forgejo')):
            with self.subTest(env_service=env_service), patch.dict(os.environ, {
                    env_service.upper() + '_TOKEN': 'synthetic-env-' + env_service}):
                path = self.token_file(file_service, (' \nsynthetic-file-' + file_service + '\t').encode())
                original = Path.read_text

                def read(selected, *args, **kwargs):
                    self.assertEqual(selected, path)
                    self.assertEqual(kwargs, {'encoding': 'utf-8'})
                    return original(selected, *args, **kwargs)

                self.api.reset_mock()
                self.sync.reset_mock()
                with patch.object(Path, 'read_text', read):
                    output = self.main()
                expected = ['token synthetic-' + ('env-' if env_service == 'forgejo' else 'file-') + 'forgejo',
                            'Bearer synthetic-' + ('env-' if env_service == 'github' else 'file-') + 'github']
                self.assertEqual([call.args[1] for call in self.api.call_args_list], expected)
                self.assertNotIn('synthetic-', output)
                path.unlink()

    def test_missing_empty_and_whitespace_token_files_fail_before_api_lock_or_sync(self):
        for service in ('forgejo', 'github'):
            for content in (None, b'', b' \t\n'):
                with self.subTest(service=service, content=content), patch.dict(os.environ, {
                        ('GITHUB_TOKEN' if service == 'forgejo' else 'FORGEJO_TOKEN'): 'synthetic-other-token'}):
                    path = self.home / '.config' / service / 'token'
                    if content is not None:
                        self.token_file(service, content)
                    output = self.main(expected_error='缺少 ' + service.upper() + '_TOKEN')
                    self.assertIn(str(path), output)
                    self.assertNotIn('synthetic-other-token', output)
                    if path.exists():
                        path.unlink()

    def test_read_permission_error_fails_before_api_lock_or_sync_without_leaking(self):
        for service in ('forgejo', 'github'):
            with self.subTest(service=service), patch.dict(os.environ, {
                    ('GITHUB_TOKEN' if service == 'forgejo' else 'FORGEJO_TOKEN'): 'synthetic-other-token'}):
                with patch.object(Path, 'read_text', side_effect=PermissionError('synthetic-private-content')):
                    output = self.main(expected_error='凭据文件失败')
                self.assertNotIn('synthetic-private-content', output)
                self.assertNotIn('synthetic-other-token', output)

    def test_invalid_utf8_fails_before_api_lock_or_sync_without_leaking(self):
        for service in ('forgejo', 'github'):
            with self.subTest(service=service), patch.dict(os.environ, {
                    ('GITHUB_TOKEN' if service == 'forgejo' else 'FORGEJO_TOKEN'): 'synthetic-other-token'}):
                path = self.token_file(service, b'synthetic-private-content\xff')
                output = self.main(expected_error='凭据文件失败')
                self.assertNotIn('synthetic-private-content', output)
                self.assertNotIn('synthetic-other-token', output)
                path.unlink()

    def test_file_tokens_do_not_populate_environment_or_replace_registry_authentication(self):
        self.token_file('forgejo', b'synthetic-file-forgejo')
        self.token_file('github', b'synthetic-file-github')
        self.main()
        self.assertNotIn('FORGEJO_TOKEN', os.environ)
        self.assertNotIn('GITHUB_TOKEN', os.environ)
        self.assertNotIn('REGISTRY_AUTH_FILE', os.environ)
        with self.assertRaisesRegex(RuntimeError, 'REGISTRY_AUTH_FILE'):
            self.module['auth_file']()

    def test_git_subprocess_environment_still_excludes_api_tokens(self):
        os.environ.update(FORGEJO_TOKEN='synthetic-env-forgejo', GITHUB_TOKEN='synthetic-env-github')
        with patch.object(subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as process:
            self.module['run'](['git', 'ls-remote', 'fixture'], 'fixture')
        environment = process.call_args.kwargs['env']
        self.assertNotIn('FORGEJO_TOKEN', environment)
        self.assertNotIn('GITHUB_TOKEN', environment)
        self.assertEqual(environment['GIT_TERMINAL_PROMPT'], '0')


class SyncUserAgentTests(unittest.TestCase):
    def test_forgejo_helper_receives_fixed_user_agent_and_separate_authorization(self):
        module = {'__name__': 'sync_fixture'}
        with (patch.object(sys, 'argv', ['sync', str(ROOT)]), patch.object(sys, 'path', sys.path[:]),
              tempfile.TemporaryDirectory() as home, patch.dict(os.environ, {'HOME': home}, clear=True)):
            exec(compile(SOURCE, 'sysnc-github.sh', 'exec'), module)
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
        exec(compile(SOURCE, 'sysnc-github.sh', 'exec'), self.module)


class SyncRegistryAuthTests(SyncFixture):
    def setUp(self):
        super().setUp()
        self.enterContext(patch('shutil.which', return_value='/synthetic/skopeo'))
        self.default = self.directory / '.config' / 'containers' / 'auth.json'
        self.default.parent.mkdir(parents=True)
        self.default.write_text('synthetic-private-default')

    def test_unset_and_empty_select_home_default_without_reading_content(self):
        for value in (None, ''):
            with self.subTest(value=value):
                if value is not None:
                    os.environ['REGISTRY_AUTH_FILE'] = value
                with patch.object(Path, 'read_bytes', side_effect=AssertionError('must not read content')):
                    self.assertEqual(self.module['auth_file'](), str(self.default))

    def test_nonempty_explicit_authfile_wins(self):
        explicit = self.directory / 'explicit.json'
        explicit.write_text('synthetic-private-explicit')
        os.environ['REGISTRY_AUTH_FILE'] = str(explicit)
        self.default.unlink()
        self.assertEqual(self.module['auth_file'](), str(explicit))

    def test_missing_or_directory_explicit_path_never_falls_back(self):
        for path in (self.directory / 'missing.json', self.directory):
            with self.subTest(path=path), patch.dict(os.environ, REGISTRY_AUTH_FILE=str(path)):
                with self.assertRaises(RuntimeError) as error:
                    self.module['auth_file']()
                self.assertIn(str(path), str(error.exception))
                self.assertIn('skopeo login --authfile', str(error.exception))
                self.assertIn('REGISTRY_AUTH_FILE', str(error.exception))
                self.assertNotIn('synthetic-private', str(error.exception))

    def test_unreadable_explicit_or_default_reports_only_selected_path_and_guidance(self):
        for explicit in (False, True):
            path = self.directory / 'explicit.json' if explicit else self.default
            environment = {'REGISTRY_AUTH_FILE': str(path)} if explicit else {}
            with self.subTest(explicit=explicit), patch.dict(os.environ, environment):
                with patch.object(Path, 'is_file', return_value=True), patch.object(
                        Path, 'open', side_effect=PermissionError('synthetic-private-content')):
                    with self.assertRaises(RuntimeError) as error:
                        self.module['auth_file']()
                self.assertIn(str(path), str(error.exception))
                self.assertIn('skopeo login --authfile', str(error.exception))
                self.assertNotIn('synthetic-private-content', str(error.exception))

    def test_missing_default_reports_home_path(self):
        self.default.unlink()
        with self.assertRaises(RuntimeError) as error:
            self.module['auth_file']()
        self.assertIn(str(self.default), str(error.exception))


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

    def test_snapshot_inherits_only_local_credentials_preserving_empty_and_multi_helpers(self):
        credentials = self.directory / 'synthetic-credentials'
        credentials.write_text('https://fixture-user:fixture-password@github.test\n')
        credentials.chmod(0o600)
        key = 'credential.https://github.test.helper'
        self.git('-C', str(self.project), 'config', 'credential.helper', '')
        for value in ('', 'store --file=' + str(credentials), ''):
            self.git('-C', str(self.project), 'config', '--add', key, value)
        self.git('-C', str(self.project), 'config', '--add', key, 'store --file=' + str(credentials))
        self.git('-C', str(self.project), 'config', 'credential.https://github.test.useHttpPath', 'true')
        self.git('-C', str(self.project), 'config', 'credential.https://github.test.username', 'fixture-user')
        for name, value in (('core.hooksPath', '/must-not-inherit'), ('remote.private.url', '/must-not-inherit'),
                            ('user.name', 'must-not-inherit'), ('url./untrusted.insteadOf', 'https://github.test/')):
            self.git('-C', str(self.project), 'config', name, value)
        snapshot = self.module['Snapshot'](self.directory / 'snapshot.git', project=self.project)
        self.assertEqual(self.git('-C', str(snapshot.directory), 'config', '--get-all', key),
                         '\nstore --file=' + str(credentials) + '\n\nstore --file=' + str(credentials) + '\n')
        self.assertEqual(self.git('-C', str(snapshot.directory), 'config', '--get-all', 'credential.helper'), '\n')
        output = self.git('-C', str(snapshot.directory), 'credential', 'fill',
                          input='protocol=https\nhost=github.test\n\n')
        self.assertIn('username=fixture-user', output)
        self.assertIn('password=fixture-password', output)
        self.assertEqual(self.git('-C', str(snapshot.directory), 'config', '--get',
                                 'credential.https://github.test.useHttpPath').strip(), 'true')
        for name in ('core.hooksPath', 'remote.private.url', 'user.name', 'url./untrusted.insteadOf'):
            result = subprocess.run(['git', '-C', str(snapshot.directory), 'config', '--local', '--get', name],
                                    capture_output=True)
            self.assertEqual(result.returncode, 1)
        # Exercise the temporary repository's actual transport preflight, not only the project config.
        self.assertIn('refs/heads/main', snapshot.target())

    def test_no_project_git_config_still_creates_snapshot(self):
        standalone = self.directory / 'standalone'
        standalone.mkdir()
        snapshot = self.module['Snapshot'](self.directory / 'snapshot.git', project=standalone)
        self.assertIn('refs/heads/main', snapshot.heads)


class SyncCustomVersionTests(SyncFixture):
    def test_new_custom_version_validates_and_copies_six_packages_and_oci(self):
        tag = 'v2.22.0-custom.20'
        sha = 'a' * 40
        packages = {name: ('synthetic-package-' + name).encode() for name in self.module['package_names'](tag)}
        hashes = {name: hashlib.sha256(data).hexdigest() for name, data in packages.items()}
        sums = ''.join(f'{value}  {name}\n' for name, value in sorted(hashes.items())).encode()
        image = dict(ref=self.module['FORGEJO_IMAGE'] + ':' + tag, digest='sha256:' + 'c' * 64,
                     platforms={platform: 'sha256:' + value * 64
                                for platform, value in zip(self.module['PLATFORMS'], ('d', 'e'))})
        build = json.dumps(dict(version=tag, source_sha=sha, source_branch='main', image=image,
                                packages=[dict(name=name, sha256=value) for name, value in hashes.items()])).encode()
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
                        verify_image=verify_image, run=process), redirect_stdout(io.StringIO()):
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


if __name__ == '__main__':
    unittest.main()
