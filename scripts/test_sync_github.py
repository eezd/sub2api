"""Offline credential and fixed-User-Agent tests for sysnc-github.sh."""
from contextlib import contextmanager, redirect_stderr, redirect_stdout
import io
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


if __name__ == '__main__':
    unittest.main()
