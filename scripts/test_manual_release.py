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
from unittest.mock import patch

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
            self.assertIsNone(release.baseline([records[1]], 'dev', side))
            self.assertEqual(release.notes(None, head), 'init')


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
            elif '/dispatches?' in url:
                if isinstance(dispatch_response, OSError):
                    raise dispatch_response
                value = dispatch_response
            elif url.endswith('/actions/runs/123'):
                value = {'conclusion': 'failure'}
            else:
                raise AssertionError(url)
            yield 200, {}, io.BytesIO(json.dumps(value).encode())

        stdout, stderr = io.StringIO(), io.StringIO()
        with (patch.object(gitea_http, 'request', reply), patch.dict(os.environ, {'GITEA_TOKEN': 'fixture'}),
              patch.object(sys, 'argv', ['release', 'feature/example', str(ROOT)]),
              patch.object(sys, 'path', sys.path[:]), redirect_stdout(stdout), redirect_stderr(stderr),
              self.assertRaises(SystemExit) as exit_status):
            exec(compile(source, 'release.sh', 'exec'), {})
        self.assertEqual(exit_status.exception.code, 1)
        self.assertIn('/branches/feature%2Fexample', calls[0][0])
        self.assertIn('版本：20261108-aaaaaaaa', stdout.getvalue())
        dispatch, = [call for call in calls if call[1] == 'POST']
        self.assertTrue(dispatch[0].endswith(
            '/actions/workflows/sub2api-manual-release.yml/dispatches?return_run_details=true'))
        self.assertTrue((ROOT / '.gitea/workflows/sub2api-manual-release.yml').is_file())
        data = json.loads(dispatch[2])
        self.assertEqual(data['ref'], 'refs/heads/main')
        self.assertEqual(data['inputs']['source_sha'], sha)
        self.assertEqual(data['inputs']['source_branch'], 'feature/example')
        self.assertIs(data['inputs']['publish_release'], True)
        self.assertIn(data['inputs']['request_id'], stdout.getvalue())
        return calls, stderr.getvalue()

    def test_lost_dispatch_response_is_not_replayed(self):
        calls, error = self.run_client(OSError('lost response'))
        self.assertEqual(len(calls), 3)
        self.assertIn('按请求 ID 核查', error)

    def test_only_returned_run_id_is_polled(self):
        calls, error = self.run_client({'workflow_run_id': 123})
        self.assertEqual(len(calls), 4)
        self.assertTrue(calls[-1][0].endswith('/actions/runs/123'))
        self.assertIn('发布未成功：failure', error)


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
        }))
        self.env.pop('GITEA_RELEASE_TOKEN', None)
        self.api = self.enterContext(patch.object(release, 'Gitea'))
        self.publish = self.enterContext(patch.object(release, 'publish'))
        self.checkpoint = self.enterContext(patch.object(release, 'write_json'))
        self.engine = self.enterContext(patch.object(release, 'worker_test_engine', return_value='192.168.0.1'))
        self.build = self.enterContext(patch.object(release, 'build'))

    def git(self, *args):
        return release.git(*args)

    def test_builds_original_sha_without_api_or_checkpoint(self):
        self.git('commit', '-q', '--allow-empty', '-m', 'advance')
        self.git('update-ref', 'refs/remotes/origin/feature/demo', 'HEAD')
        with redirect_stdout(io.StringIO()) as stdout:
            release.verify_build()
        self.build.assert_called_once()
        output, row, run_id, engine = self.build.call_args.args
        self.assertEqual(row['source_sha'], self.commit)
        self.assertEqual(row['source_branch'], 'feature/demo')
        self.assertEqual(run_id, '123-1')
        self.assertEqual(engine, '192.168.0.1')
        self.assertFalse(output.exists())
        self.assertIn('nothing published', stdout.getvalue())
        self.api.assert_not_called()
        self.publish.assert_not_called()
        self.checkpoint.assert_not_called()

    def test_unreachable_sha_stops_before_worker_access(self):
        self.git('update-ref', 'refs/remotes/origin/feature/demo', self.commit)
        self.git('commit', '-q', '--allow-empty', '-m', 'not on source branch')
        self.env['SOURCE_SHA'] = self.git('rev-parse', 'HEAD')
        with self.assertRaisesRegex(ValueError, 'no longer reachable'):
            release.verify_build()
        self.engine.assert_not_called()
        self.build.assert_not_called()
        self.api.assert_not_called()


class TransportTests(unittest.TestCase):
    def test_token_only_enters_curl_config_and_post_is_not_replayed(self):
        def curl(args, *, input, stdout, stderr, env):
            self.assertNotIn('fixture-secret', ' '.join(args))
            self.assertNotIn('GITEA_TOKEN', env)
            self.assertNotIn('--location', args)
            self.assertNotIn('--retry', args)
            self.assertEqual(input, b'header = "Authorization: token fixture-secret"\n')
            self.assertEqual(Path(args[args.index('--data-binary') + 1][1:]).read_bytes(), b'{}')
            headers = Path(args[args.index('--dump-header') + 1])
            headers.write_bytes(b'HTTP/1.1 100 Continue\r\n\r\nHTTP/2 302\r\nlocation: /next\r\n\r\n')
            stdout.write(b'unchanged')
            return subprocess.CompletedProcess(args, 0)

        with patch.dict(os.environ, {'GITEA_TOKEN': 'fixture-secret'}), patch.object(gitea_http.subprocess, 'run', side_effect=curl) as send:
            with gitea_http.request('https://gitea.test/dispatch', 'token fixture-secret', method='POST', data=b'{}') as result:
                status, headers, body = result
                self.assertEqual(status, 302)
                self.assertEqual(headers['Location'], '/next')
                self.assertEqual(body.read(), b'unchanged')
        send.assert_called_once()

    def test_draft_lookup_falls_back_to_exact_tag(self):
        api = release.Gitea('fixture')
        version = '20250301-aaaaaaaa'
        draft = {'id': 7, 'tag_name': version, 'draft': True}
        with patch.object(api, 'repo', side_effect=[None, [draft]]) as query:
            self.assertEqual(api.release(version), draft)
        self.assertEqual(query.call_args_list[1].args, ('/releases?draft=true&tag_filter=' + version,))


class FakeGitea:
    def __init__(self, record):
        self.entry = dict(id=42, tag_name=record['version'], name=record['version'], body=record['body'],
                          draft=True, prerelease=False)
        self.attachments = {}
        self.uploads = []
        self.fail_next = False

    def release(self, _version):
        return self.entry

    def assets(self, _release):
        return self.attachments.copy()

    def download(self, asset):
        return asset

    def upload(self, _release, file):
        self.uploads.append(file.name)
        if self.fail_next:
            self.fail_next = False
            raise RuntimeError('Simulated lost response')
        self.attachments[file.name] = file.read_bytes()

    def repo(self, path, method, payload):
        if (path, method, payload) != ('/releases/42', 'PATCH', {'draft': False}):
            raise AssertionError((path, method))
        self.entry['draft'] = False
        return self.entry


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.directory = Path(scratch.name)
        self.record = dict(version='20250301-aaaaaaaa', source_sha='a' * 40, body='init', phase='building')
        self.path = self.directory / 'record.json'
        self.api = FakeGitea(self.record)
        self.files = {'original.tar.gz': b'original', 'build.json': b'{}', 'SHA256SUMS': b'checksums'}
        for name, body in self.files.items():
            (self.directory / name).write_bytes(body)
        proof = {name: release.digest(body) for name, body in self.files.items()}
        proof['image.oci'] = 'unused'
        patches = [patch.object(release, 'tag'), patch.object(release, 'registry'),
                   patch.object(release, 'checkpoint', return_value=({'image': {}}, proof))]
        for mock in patches:
            mock.start()
            self.addCleanup(mock.stop)

    def publish(self):
        release.publish(self.api, 'fake-auth', self.record, self.directory, self.path)

    def test_resume_preserves_existing_bytes_and_uploads_marker_last(self):
        self.api.attachments['original.tar.gz'] = self.files['original.tar.gz']
        self.api.fail_next = True
        with self.assertRaisesRegex(RuntimeError, 'Simulated lost response'):
            self.publish()
        self.assertEqual(json.loads(self.path.read_text())['phase'], 'publishing')
        self.assertNotIn(release.COMPLETE, self.api.attachments)
        self.publish()
        self.assertEqual(self.api.uploads[-1], release.COMPLETE)
        self.assertNotIn('original.tar.gz', self.api.uploads)
        self.assertEqual(json.loads(self.path.read_text())['phase'], 'complete')
        calls = self.api.uploads.copy()
        self.publish()
        self.assertEqual(self.api.uploads, calls)

    def test_mismatched_existing_attachment_writes_nothing_new(self):
        self.api.attachments['original.tar.gz'] = b'other bytes'
        with self.assertRaisesRegex(ValueError, 'Existing Gitea asset conflicts'):
            self.publish()
        self.assertEqual(self.api.uploads, [])
        self.assertTrue(self.api.entry['draft'])
        release.registry.assert_not_called()

    def test_draft_or_tag_without_checkpoint_blocks_rebuild(self):
        target = self.directory / 'missing'
        self.assertTrue(release.pending(self.api, self.record, target))
        self.api.entry = None
        with patch.object(self.api, 'git', create=True, return_value='') as probe:
            self.assertFalse(release.pending(self.api, self.record, target))
            probe.return_value = 'existing tag'
            self.assertTrue(release.pending(self.api, self.record, target))


if __name__ == '__main__':
    unittest.main()
