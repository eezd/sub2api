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


if __name__ == '__main__':
    unittest.main()
