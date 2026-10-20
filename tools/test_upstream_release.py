"""在隔离 Git 仓库中验证同步、发布边界及中断恢复。"""

import argparse
from contextlib import redirect_stdout
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import upstream_release as release


def run(root, *args):
    return subprocess.check_output(['git', *args], cwd=root, text=True, stderr=subprocess.DEVNULL).strip()


class GitHubFixture:
    token = 'fixture'

    def __init__(self, sha):
        self.sha = sha
        self.runs = {}
        self.jobs = {}
        self.variable = 'false'
        self.published = None

    def get(self, path, optional=False):
        if path.endswith('/releases/latest'):
            return {'tag_name': 'v2.13.0', 'draft': False, 'prerelease': False, 'body': '上游修复内容'}
        if path.endswith('/branches/production'):
            return {'commit': {'sha': self.sha}}
        if '/compare/' in path:
            return {'status': 'identical', 'base_commit': {'sha': self.sha}, 'ahead_by': 0}
        if path.endswith('/variables/SIMPLE_RELEASE'):
            return {'value': self.variable}
        if '/actions/workflows/' in path:
            workflow = path.split('/actions/workflows/')[1].split('/')[0]
            return {'workflow_runs': self.runs.get(workflow, [])}
        if '/releases/tags/' in path:
            return self.published
        raise AssertionError(f'未预期的查询：{path}')

    def pages(self, path, key=None):
        if path.endswith('/actions/variables'):
            return [{'name': 'SIMPLE_RELEASE', 'value': self.variable}]
        if path.endswith('/tags') or path.endswith('/releases'):
            return []
        if '/actions/runs/' in path:
            return self.jobs[path.split('/actions/runs/')[1].split('/')[0]]
        raise AssertionError(f'未预期的分页：{path}')

    def successful_ci(self, sha):
        for index, (workflow, jobs) in enumerate(release.CHECKS.items(), 1):
            self.runs[workflow] = [{'id': index, 'head_sha': sha, 'event': 'push', 'head_branch': 'production',
                                    'status': 'completed', 'conclusion': 'success', 'html_url': 'fixture'}]
            self.jobs[str(index)] = [{'name': name, 'conclusion': 'success'} for name in jobs]


class UpstreamReleaseTests(unittest.TestCase):
    def setUp(self):
        self.output = redirect_stdout(io.StringIO())
        self.output.__enter__()
        self.addCleanup(self.output.__exit__, None, None, None)
        self.temporary = tempfile.TemporaryDirectory(prefix='sub2api-sync-test-')
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.origin = self.directory / 'origin.git'
        self.upstream = self.directory / 'upstream'
        self.local = self.directory / 'local'
        self.upstream.mkdir()
        run(self.directory, 'init', '--bare', str(self.origin))
        run(self.upstream, 'init', '--initial-branch=production')
        self.identity(self.upstream)
        files = {
            '.github/workflows/release.yml': "'v*-custom.*'\nsecrets.GHCR_TOKEN\nsync-version-file:\n",
            '.github/release-tools/release_matrix.py': 'CUSTOM_VERSION_RE\ndef is_prerelease(version): pass\n',
            '.github/release-tools/release-images.sh': '-custom\namd64 arm64\n',
            'backend/internal/service/update_service.go': '"eezd/sub2api"\ncustomVersionMarker\n',
            'backend/internal/reauthruntime/runtime.go': '"eezd/sub2api"\n',
            'deploy/install.sh': 'eezd/sub2api\n',
            'deploy/upgrade-k3s.sh': 'eezd/sub2api\n-custom\n',
            'deploy/docker-compose.yml': 'ghcr.io/eezd/sub2api\n',
            release.VERSION: '2.12.0\n',
            'feature.txt': '原始内容\n',
        }
        for filename, content in files.items():
            path = self.upstream / filename
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        run(self.upstream, 'add', '.')
        run(self.upstream, 'commit', '-m', 'initial')
        run(self.upstream, 'remote', 'add', 'origin', str(self.origin))
        run(self.upstream, 'push', 'origin', 'production')
        run(self.directory, 'clone', '--branch', 'production', str(self.origin), str(self.local))
        self.identity(self.local)
        run(self.local, 'config', f'url.{self.upstream}.insteadOf', 'https://github.com/ranxi2001/sub2api.git')
        (self.upstream / 'feature.txt').write_text('上游新功能\n')
        (self.upstream / release.VERSION).write_text('2.13.0\n')
        run(self.upstream, 'commit', '-am', 'upstream feature')
        self.api = GitHubFixture(run(self.upstream, 'rev-parse', 'HEAD'))
        self.args = argparse.Namespace(upstream_sha=self.api.sha)

    @staticmethod
    def identity(root):
        run(root, 'config', 'user.name', 'Fixture')
        run(root, 'config', 'user.email', 'fixture@example.invalid')

    def prepare(self):
        state = release.sync(self.local, self.api, self.args)
        self.api.successful_ci(state['sha'])
        return state

    def test_numeric_version_uses_all_existing_sequences(self):
        self.assertEqual(release.next_tag('v2.13.0', ['v2.13.0-custom.2', 'v2.13.0-custom.10',
                                                     'v2.12.0-custom.99', 'v2.13.0-rc.1']), 'v2.13.0-custom.11')

    def test_initial_sync_pins_target_and_preserves_untracked_files(self):
        untracked = self.local / 'private-data.txt'
        untracked.write_text('需要保留的数据')
        state = self.prepare()
        self.assertTrue(release.ancestor(self.local, self.api.sha, state['sha']))
        self.assertEqual(run(self.origin, 'rev-parse', 'production'), state['sha'])
        self.assertEqual(untracked.read_text(), '需要保留的数据')
        self.assertEqual((self.local / release.VERSION).read_text().strip(), '2.13.0')
        self.assertEqual(release.sync(self.local, self.api, self.args)['sha'], state['sha'])

    def test_plan_makes_no_repository_changes(self):
        before = run(self.local, 'show-ref')
        self.assertEqual(release.plan(self.local, self.api)['tag'], 'v2.13.0-custom.1')
        self.assertFalse(release.state_path(self.local).exists())
        self.assertEqual(run(self.local, 'show-ref'), before)

    def test_dirty_worktree_prevents_sync(self):
        original = run(self.origin, 'rev-parse', 'production')
        (self.local / 'feature.txt').write_text('未提交内容')
        with self.assertRaisesRegex(release.ReleaseError, '未提交修改'):
            release.sync(self.local, self.api, self.args)
        self.assertEqual(run(self.origin, 'rev-parse', 'production'), original)

    def test_conflict_is_preserved_and_resumes_after_resolution(self):
        (self.local / 'feature.txt').write_text('本地功能\n')
        run(self.local, 'commit', '-am', 'local feature')
        run(self.local, 'push', 'origin', 'production')
        with self.assertRaisesRegex(release.ReleaseError, '合并发生冲突'):
            release.sync(self.local, self.api, self.args)
        self.assertIn('feature.txt', run(self.local, 'diff', '--name-only', '--diff-filter=U'))
        self.assertEqual(release.load(self.local)['stage'], 'conflicts')
        (self.local / 'feature.txt').write_text('保留本地功能并合并上游新功能\n')
        run(self.local, 'add', 'feature.txt')
        run(self.local, 'commit', '-m', 'merge: resolve upstream feature conflict')
        state = release.sync(self.local, self.api, self.args)
        self.assertEqual(state['stage'], 'pushed')
        self.assertTrue(release.ancestor(self.local, self.api.sha, state['sha']))

    def test_missing_ci_or_skipped_job_cannot_create_tag(self):
        state = self.prepare()
        args = argparse.Namespace(sha=state['sha'], timeout=0)
        self.api.runs['backend-ci.yml'] = []
        with self.assertRaisesRegex(release.ReleaseError, '等待超时'):
            release.publish(self.local, self.api, args)
        self.assertEqual(run(self.local, 'tag'), '')
        self.api.successful_ci(state['sha'])
        self.api.jobs['1'][0]['conclusion'] = 'skipped'
        with self.assertRaisesRegex(release.ReleaseError, '缺失、跳过或失败'):
            release.publish(self.local, self.api, args)
        self.assertEqual(run(self.local, 'tag'), '')

    def test_ci_for_different_commit_cannot_create_tag(self):
        state = self.prepare()
        self.api.successful_ci('a' * 40)
        with self.assertRaisesRegex(release.ReleaseError, '等待超时'):
            release.publish(self.local, self.api, argparse.Namespace(sha=state['sha'], timeout=0))
        self.assertEqual(run(self.local, 'tag'), '')

    def test_post_sync_fix_gets_new_ci_commit(self):
        original = self.prepare()
        (self.local / 'feature.txt').write_text('发布前修正\n')
        run(self.local, 'commit', '-am', 'fix: preserve fork behavior')
        updated = release.sync(self.local, self.api, self.args)
        self.assertNotEqual(original['sha'], updated['sha'])
        self.assertEqual(run(self.origin, 'rev-parse', 'production'), updated['sha'])
        with self.assertRaisesRegex(release.ReleaseError, '等待超时'):
            release.publish(self.local, self.api, argparse.Namespace(sha=updated['sha'], timeout=0))

    def test_rejected_fix_push_can_resume_without_losing_previous_remote(self):
        original = self.prepare()
        (self.local / 'feature.txt').write_text('发布前修正\n')
        run(self.local, 'commit', '-am', 'fix: preserve fork behavior')
        hook = self.origin / 'hooks/pre-receive'
        hook.write_text('#!/bin/sh\nexit 1\n')
        hook.chmod(0o755)
        with self.assertRaisesRegex(release.ReleaseError, 'git 执行失败'):
            release.sync(self.local, self.api, self.args)
        self.assertEqual(release.load(self.local)['stage'], 'merged')
        self.assertEqual(run(self.origin, 'rev-parse', 'production'), original['sha'])
        hook.unlink()
        resumed = release.sync(self.local, self.api, self.args)
        self.assertEqual(run(self.origin, 'rev-parse', 'production'), resumed['sha'])

    def test_publish_resumes_after_local_tag_creation_and_pushes_once(self):
        state = self.prepare()
        message = f'Sub2API {state["tag"].removeprefix("v")}\n\n{release.tag_notes(state)}'
        run(self.local, 'tag', '-a', state['tag'], state['sha'], '-m', message)
        args = argparse.Namespace(sha=state['sha'], timeout=0)
        release.publish(self.local, self.api, args)
        tag_id = run(self.origin, 'rev-parse', f'refs/tags/{state["tag"]}')
        release.publish(self.local, self.api, args)
        self.assertEqual(run(self.origin, 'rev-parse', f'refs/tags/{state["tag"]}'), tag_id)
        self.assertEqual(release.load(self.local)['stage'], 'tagged')

    def test_remote_movement_prevents_publication(self):
        state = self.prepare()
        clone = self.directory / 'concurrent'
        run(self.directory, 'clone', '--branch', 'production', str(self.origin), str(clone))
        self.identity(clone)
        (clone / 'feature.txt').write_text('并发提交\n')
        run(clone, 'commit', '-am', 'concurrent change')
        run(clone, 'push', 'origin', 'production')
        with self.assertRaisesRegex(release.ReleaseError, '远端 production 已变化'):
            release.publish(self.local, self.api, argparse.Namespace(sha=state['sha'], timeout=0))
        self.assertEqual(run(self.origin, 'tag'), '')

    def test_interrupted_tag_push_is_adopted_after_version_bot_advances(self):
        state = self.prepare()
        args = argparse.Namespace(sha=state['sha'], timeout=0)
        release.publish(self.local, self.api, args)
        state['stage'] = 'pushed'
        release.save(self.local, state)
        (self.local / release.VERSION).write_text(state['tag'].removeprefix('v') + '\n')
        run(self.local, 'commit', '-am', 'chore: sync VERSION')
        run(self.local, 'push', 'origin', 'production')
        self.assertEqual(release.publish(self.local, self.api, args)['stage'], 'tagged')

    def test_verify_downloads_checksums_and_fast_forwards_version(self):
        state = self.prepare()
        release.publish(self.local, self.api, argparse.Namespace(sha=state['sha'], timeout=0))
        binaries, runtimes = release.expected_assets(state['tag'])
        payloads = {name: name.encode() for name in binaries | runtimes}
        payloads['checksums.txt'] = ''.join(hashlib.sha256(payloads[name]).hexdigest() + '  ' + name + '\n'
                                             for name in sorted(binaries)).encode()
        self.api.published = {
            'draft': False, 'prerelease': False, 'html_url': 'https://github.com/eezd/sub2api/releases/fixture',
            'assets': [{'name': name, 'size': len(content), 'state': 'uploaded',
                        'browser_download_url': f'https://github.com/eezd/sub2api/releases/download/{state["tag"]}/{name}'}
                       for name, content in payloads.items()],
        }
        self.api.runs['release.yml'] = [{'id': 3, 'head_sha': state['sha'], 'event': 'push', 'head_branch': state['tag'],
                                        'status': 'completed', 'conclusion': 'success', 'html_url': 'fixture-release'}]
        self.api.jobs['3'] = [{'name': name, 'conclusion': 'success'} for name in
                              ('prepare', 'build-frontend', 'release', 'sync-version-file', 'build-binaries (linux, amd64)')]
        bot = self.directory / 'bot'
        run(self.directory, 'clone', '--branch', 'production', str(self.origin), str(bot))
        self.identity(bot)
        (bot / release.VERSION).write_text(state['tag'].removeprefix('v') + '\n')
        run(bot, 'commit', '-am', 'chore: sync VERSION')
        run(bot, 'push', 'origin', 'production')
        both = json.dumps({'manifests': [{'platform': {'os': 'linux', 'architecture': arch}}
                                         for arch in ('amd64', 'arm64')]})
        original_command = release.command

        def execute(args, **kwargs):
            if args[0] == 'docker':
                return argparse.Namespace(stdout=both)
            return original_command(args, **kwargs)

        with patch.object(release, 'download', side_effect=lambda url, path: path.write_bytes(payloads[path.name])):
            with patch.object(release.shutil, 'which', return_value='/fixture/docker'):
                with patch.object(release, 'command', side_effect=execute):
                    verified = release.verify(self.local, self.api, argparse.Namespace(timeout=0))
        self.assertEqual(verified['stage'], 'verified')
        self.assertEqual((self.local / release.VERSION).read_text().strip(), '2.13.0-custom.1')
        self.assertEqual(run(self.local, 'rev-parse', 'HEAD'), run(bot, 'rev-parse', 'HEAD'))

    def test_tag_notes_preserve_changes_and_remove_upstream_install_commands(self):
        state = release.plan(self.local, self.api)
        state['upstream_notes'] = ('> AI API Gateway Platform - 简介\n\n上游功能说明\n\n---\n\n'
                                   '## 📥 Installation\n\ndocker pull ghcr.io/ranxi2001/sub2api:2.13.0\n')
        notes = release.tag_notes(state)
        self.assertIn('上游功能说明', notes)
        self.assertIn('eezd', notes)
        self.assertNotIn('Installation', notes)
        self.assertNotIn('docker pull', notes)
        self.assertNotIn('---', notes)

    def test_simple_mode_blocks_sync_and_missing_credentials_are_explicit(self):
        self.api.variable = 'true'
        with self.assertRaisesRegex(release.ReleaseError, 'SIMPLE_RELEASE=true'):
            release.sync(self.local, self.api, self.args)
        self.api.token = None
        with self.assertRaisesRegex(release.ReleaseError, 'GH_TOKEN'):
            release.release_settings(self.api)

    def test_repository_validation_rejects_other_push_destination(self):
        run(self.local, 'remote', 'set-url', 'origin', 'git@github.com:eezd/sub2api.git')
        run(self.local, 'remote', 'set-url', '--push', 'origin', 'git@github.com:ranxi2001/sub2api.git')
        with self.assertRaisesRegex(release.ReleaseError, 'origin'):
            release.repository(self.local)

    def test_corrupted_asset_fails_checksum_validation(self):
        payload = self.directory / 'binary.tar.gz'
        payload.write_bytes(b'original')
        (self.directory / 'checksums.txt').write_text(hashlib.sha256(b'original').hexdigest() + '  binary.tar.gz\n')
        release.verify_checksums(self.directory, {'binary.tar.gz'})
        payload.write_bytes(b'corrupted')
        with self.assertRaisesRegex(release.ReleaseError, '资产校验失败'):
            release.verify_checksums(self.directory, {'binary.tar.gz'})

    def test_single_arch_or_mismatched_alias_is_rejected(self):
        single = json.dumps({'manifests': [{'platform': {'os': 'linux', 'architecture': 'amd64'}}]})
        both = json.dumps({'manifests': [{'platform': {'os': 'linux', 'architecture': arch}}
                                         for arch in ('amd64', 'arm64')]})
        other = json.dumps({'manifests': [{'platform': {'os': 'linux', 'architecture': arch}}
                                          for arch in ('amd64', 'arm64')], 'revision': 'different'})
        with patch.object(release.shutil, 'which', return_value='/fixture/docker'):
            with patch.object(release, 'command', return_value=argparse.Namespace(stdout=single)):
                with self.assertRaisesRegex(release.ReleaseError, '缺少 amd64 或 arm64'):
                    release.verify_images('v2.13.0-custom.1')
            with patch.object(release, 'command', side_effect=[argparse.Namespace(stdout=both), argparse.Namespace(stdout=other)]):
                with self.assertRaisesRegex(release.ReleaseError, '未指向本次版本'):
                    release.verify_images('v2.13.0-custom.1')


if __name__ == '__main__':
    unittest.main()
