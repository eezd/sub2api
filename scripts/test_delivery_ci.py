"""Offline web-contract boundaries: synthetic credentials and Registry bytes only."""
from contextlib import contextmanager
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import MagicMock, patch

import delivery_ci as ci
from test_manual_release import release

spec = importlib.util.spec_from_file_location('resolver', Path(__file__).with_name('resolve-release.py'))
resolver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resolver)


class DeliveryCredentialsTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.enterContext(patch.dict(os.environ, dict(HOME=str(self.root / 'unread-home'), RUNNER_TEMP=str(self.root),
                          CI_RUN_ID='100', CI_RUN_ATTEMPT='2', FORGEJO_RELEASE_TOKEN='synthetic',
                          GITHUB_TOKEN='synthetic-github'), clear=True))

    def test_missing_secrets_zero_remote_calls(self):
        for role, tokens in [('release', {ci.REPOSITORIES[0]: ''}),
                             ('sync', {ci.REPOSITORIES[0]: 'synthetic', ci.REPOSITORIES[1]: ''})]:
            with self.subTest(role=role), patch.object(ci.subprocess, 'run') as remote:
                with self.assertRaisesRegex(ValueError, 'Missing Actions Secrets'):
                    with ci.credentials(role, tokens):
                        self.fail('credential setup must fail before writes')
                remote.assert_not_called()
                self.assertFalse(ci.path_for(role).exists())
        os.environ.pop('FORGEJO_RELEASE_TOKEN')
        with patch.object(release.Forgejo, 'request') as remote:
            with self.assertRaises(ValueError):
                release.Forgejo(os.environ.get('FORGEJO_RELEASE_TOKEN'))
            remote.assert_not_called()

    def test_exact_repo_helper_token_free_git_stdin_registry_and_finally(self):
        other = self.root / 'sub2api-release-101-2'
        other.mkdir()
        old = dict(os.environ)
        path = ci.path_for('release')
        with self.assertRaisesRegex(RuntimeError, 'synthetic cancellation'):
            with ci.credentials('release', {ci.REPOSITORIES[0]: 'synthetic-token'}) as auth:
                self.assertEqual((path / 'credentials.json').stat().st_mode & 0o777, 0o600)
                self.assertEqual(path.stat().st_mode & 0o777, 0o700)
                # Local helper must not override the explicit scoped helper.
                repo = path / 'repo'
                subprocess.run(['git', 'init', '-q', str(repo)], check=True, env=ci.child_env())
                subprocess.run(['git', '-C', str(repo), 'config', 'credential.helper', '!echo password=ambient'], check=True)
                for url, expected in [(ci.REPOSITORIES[0], True), (ci.REPOSITORIES[1], False),
                                      ('https://git.134333.xyz/eezd/other.git', False),
                                      (ci.REPOSITORIES[0] + '/child', False)]:
                    result = subprocess.run(['git', '-C', str(repo), 'credential', 'fill'],
                        input='url='+url+'\n\n', text=True, capture_output=True, env=ci.child_env())
                    self.assertEqual(result.returncode == 0, expected)
                    self.assertEqual('synthetic-token' in result.stdout, expected)
                    self.assertNotIn('ambient', result.stdout)
                with patch.object(ci.subprocess, 'run') as login:
                    ci.login(auth, 'git.134333.xyz', 'eezd', 'synthetic-token')
                self.assertEqual(login.call_args.kwargs['input'], 'synthetic-token')
                self.assertNotIn('synthetic-token', ' '.join(login.call_args.args[0]))
                self.assertFalse(set(ci.TOKENS) & login.call_args.kwargs['env'].keys())
                raise RuntimeError('synthetic cancellation')
        self.assertEqual(dict(os.environ), old)
        self.assertFalse(path.exists())
        ci.cleanup_auth('release')  # always() repeats safely
        self.assertTrue(other.exists())

    def test_anonymous_has_empty_auth_and_independent_home(self):
        with ci.credentials('release', {ci.REPOSITORIES[0]: 'synthetic-token'}) as auth:
            authenticated_home = os.environ['HOME']
            with ci.anonymous() as empty:
                self.assertNotEqual(empty, auth)
                self.assertNotEqual(os.environ['HOME'], authenticated_home)
                self.assertEqual(json.loads(Path(empty).read_text()), {'auths': {}})
                self.assertFalse(set(ci.TOKENS) & ci.child_env().keys())
                self.assertEqual(os.environ['DOCKER_CONFIG'], os.environ['HOME'])
            self.assertFalse(Path(empty).exists())
            self.assertEqual(os.environ['HOME'], authenticated_home)

    def test_always_cleanup_targets_only_the_same_run_attempt_resources(self):
        from release_build import cleanup_run
        with patch('release_build.cleanup') as cleanup:
            cleanup_run('100-2')
            cleanup_run('100-2')
        self.assertEqual(cleanup.call_count, 20)
        cleanup.assert_any_call('docker', 'rm', '-f', '-v', 'sub2api-release-100-2-runtime')
        cleanup.assert_any_call('docker', 'image', 'rm', 'sub2api-release-100-2-runtime:latest')
        for call in cleanup.call_args_list:
            args = call.args
            self.assertTrue(any('sub2api-release-100-2' in item for item in args))
            self.assertFalse(any('sub2api-release-100-1' in item or 'prune' in item for item in args))
        with patch('release_build.cleanup') as cleanup, self.assertRaises(ValueError):
            cleanup_run('../other')
        cleanup.assert_not_called()


class ResolveReleaseTests(unittest.TestCase):
    def test_freezes_head_inputs_uuid_control_run_without_reserving_number(self):
        with patch.dict(os.environ, dict(CONTROL_REF='refs/heads/main', CONTROL_SHA='c'*40,
                        CI_RUN_ID='20', CI_RUN_ATTEMPT='3', SOURCE_BRANCH=' main ', SOURCE_SHA=' ',
                        FORGEJO_RELEASE_TOKEN='synthetic'), clear=True), patch.object(
                        resolver, 'request', return_value={'commit': {'id': 'a'*40}}) as api:
            row = resolver.resolve()
        self.assertEqual(row['source_branch_head'], 'a'*40)
        self.assertEqual(row['source_sha'], 'a'*40)
        self.assertEqual(row['release_inputs'], {'source_branch': 'main', 'source_sha': ''})
        self.assertEqual(row['control_sha'], 'c'*40)
        self.assertEqual(row['release_run_id'], '20-3')
        self.assertEqual(row['version'], '')
        self.assertEqual(len(row['release_request_id']), 36)
        api.assert_called_once()

    def test_rejects_abbreviated_uppercase_and_unknown_inputs(self):
        for inputs in ({'source_branch': 'main', 'source_sha': 'a'*8},
                       {'source_branch': 'main', 'source_sha': 'A'*40},
                       {'source_branch': 'main', 'request_id': 'old'}, {'source_branch': '-bad'}):
            with self.subTest(inputs=inputs), self.assertRaises(ValueError):
                resolver.normalize_inputs(inputs)


class RegistryBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.ref = release.IMAGE + ':v2.23.1-custom.40'
        self.leaves = {arch: json.dumps({'architecture': arch}).encode() for arch in ('amd64', 'arm64')}
        self.platforms = {'linux/'+arch: 'sha256:'+release.digest(raw) for arch, raw in self.leaves.items()}
        self.top = json.dumps({'manifests': [{'platform': {'os': 'linux', 'architecture': arch},
                              'digest': self.platforms['linux/'+arch]} for arch in self.leaves]}).encode()
        self.image = dict(ref=self.ref, digest='sha256:'+release.digest(self.top), platforms=self.platforms)
        self.data = {release.IMAGE+'@'+self.platforms['linux/'+arch]: raw for arch, raw in self.leaves.items()}

    def inspect(self, auth, ref):
        return self.data.get(ref)

    def test_arch_tags_precede_index_only_never_all_and_readback_three_tags(self):
        writes = []
        def copy(args, **kwargs):
            writes.append(args)
            target = args[-1].removeprefix('docker://')
            self.data[target] = self.top if target == self.ref else self.leaves[target.rsplit('-', 1)[1]]
        with tempfile.TemporaryDirectory() as directory, patch.object(release, 'inspect_registry', side_effect=self.inspect), \
             patch.object(release.subprocess, 'run', side_effect=copy):
            release.registry('synthetic-auth', self.image, Path(directory))
            release.public_registry('synthetic-auth', self.image, arch_tags=True)
        self.assertEqual([args[-1] for args in writes], ['docker://'+self.ref+suffix for suffix in ('-amd64', '-arm64', '')])
        self.assertTrue(all('--preserve-digests' in args and '--all' not in args for args in writes))
        self.assertIn('--multi-arch=index-only', writes[-1])

    def test_wrong_arm64_raw_bytes_rejected(self):
        self.data[self.ref] = self.top
        self.data[release.IMAGE+'@'+self.platforms['linux/arm64']] = b'wrong ARM64'
        with patch.object(release, 'inspect_registry', side_effect=self.inspect), self.assertRaisesRegex(
                ValueError, 'platform manifest not readable'):
            release.registry('auth', self.image)

    def test_authenticated_success_anonymous_denied_rejected(self):
        self.data[self.ref] = self.top
        def inspect(auth, ref):
            if auth != 'auth':
                raise RuntimeError('anonymous denied')
            return self.inspect(auth, ref)
        with patch.object(release, 'inspect_registry', side_effect=inspect), self.assertRaisesRegex(
                RuntimeError, 'anonymous denied'):
            release.public_registry('auth', self.image)


class LatestBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.old = dict(version='v2.23.1-custom.9', source_sha='a', source_branch='main', schema=5,
                        image={'digest': 'old'})
        self.new = dict(version='v2.23.1-custom.10', source_sha='b', source_branch='main', schema=5,
                        image={'digest': 'new'})
        self.dev = dict(version='v2.23.1-custom.99', source_sha='c', source_branch='dev', schema=5,
                        image={'digest': 'dev'})
        self.ancestor = lambda a, b: a == b or (a, b) in [('a', 'b'), ('a', 'head'), ('b', 'head')]

    def test_main_ancestry_not_version_or_dev_and_rerun_no_rollback(self):
        records = [self.old, self.new, self.dev]
        self.assertIs(ci.newest_main(records, 'head', self.ancestor), self.new)
        ci.check_alias('old', self.new, records, self.ancestor)
        with self.assertRaisesRegex(ValueError, 'refusing rollback'):
            ci.check_alias('new', self.old, records, self.ancestor)
        self.assertIsNone(ci.newest_main([self.dev], 'head', self.ancestor))

    def test_unknown_alias_or_rewritten_main_stops(self):
        with self.assertRaisesRegex(ValueError, 'Unknown'):
            ci.check_alias('imported-latest', self.new, [self.old, self.new], self.ancestor)
        imported = dict(self.old)
        imported.pop('schema')
        with self.assertRaisesRegex(ValueError, 'Unknown'):
            ci.check_alias('old', self.new, [imported, self.new], self.ancestor)
        with self.assertRaisesRegex(ValueError, 'diverges'):
            ci.newest_main([self.old, self.new], 'rewritten-head', self.ancestor)

    def test_historical_completed_main_never_writes_alias(self):
        row = dict(self.old, source_sha='a'*40)
        row.pop('schema')
        api = MagicMock()
        with patch.object(release, 'release_history', return_value=({'historical': (None, row, {})}, set())), \
             patch.object(release, 'git', return_value='head'), patch.object(release.subprocess, 'run',
             return_value=MagicMock(returncode=0)) as process, patch.object(release, 'public_registry') as public:
            release.latest(api, 'auth')
        self.assertTrue(process.called)
        self.assertTrue(all(call.args[0][0] == 'git' for call in process.call_args_list))
        public.assert_not_called()


class LegacyAliasBootstrapTests(unittest.TestCase):
    def setUp(self):
        self.cutoff = 'a' * 40
        self.first = self.record('b' * 40, 1)
        self.second = self.record('c' * 40, 2)
        self.aliases = {'git.134333.xyz/eezd/sub2api:latest': 'sha256:legacy',
                        'ghcr.io/eezd/sub2api:latest': 'sha256:legacy'}
        self.enterContext(patch.object(ci, 'delivery_history', return_value={'latest_transition': {
            'after_commit': self.cutoff, 'aliases': self.aliases}}))
        self.ancestor = lambda a, b: a == b or (a, b) in (
            (self.cutoff, self.first['source_sha']), (self.cutoff, self.second['source_sha']),
            (self.first['source_sha'], self.second['source_sha']))

    def record(self, sha, number):
        return dict(version='v2.23.1-custom.' + str(number), source_sha=sha, source_branch='main',
                    schema=5, image={'digest': 'new-' + str(number)})

    def test_only_explicit_frozen_endpoints_allow_first_verified_main(self):
        for endpoint, raw in self.aliases.items():
            ci.check_alias(raw, self.first, [self.first], self.ancestor, endpoint=endpoint)
        for endpoint in (None, 'ghcr.io/eezd/other:latest', 'git.134333.xyz/other/sub2api:latest',
                         'docker://ghcr.io/eezd/sub2api:latest', 'ghcr.io/eezd/sub2api:wrong'):
            with self.subTest(endpoint=endpoint), self.assertRaisesRegex(ValueError, 'Unknown'):
                ci.check_alias('sha256:legacy', self.first, [self.first], self.ancestor, endpoint=endpoint)
        with self.assertRaisesRegex(ValueError, 'Unknown'):
            ci.check_alias('sha256:unknown', self.first, [self.first], self.ancestor, endpoint=next(iter(self.aliases)))

    def test_cutoff_ancestor_dev_and_old_schema_cannot_bootstrap(self):
        for row in (dict(self.first, source_sha=self.cutoff), dict(self.first, source_sha='0' * 40),
                    dict(self.first, source_branch='dev'), dict(self.first, schema=4),
                    {k: v for k, v in self.first.items() if k != 'schema'}):
            with self.subTest(row=row), self.assertRaisesRegex(ValueError, 'Unknown'):
                ci.check_alias('sha256:legacy', row, [row], self.ancestor, endpoint=next(iter(self.aliases)))
        with self.assertRaisesRegex(ValueError, 'Unknown'):
            ci.check_alias('sha256:legacy', self.first, [], self.ancestor, endpoint=next(iter(self.aliases)))

    def test_second_completed_main_closes_window_even_on_same_source(self):
        for second in (self.second, dict(self.second, source_sha=self.first['source_sha'])):
            for selected in (self.first, second):
                with self.subTest(second=second, selected=selected), self.assertRaisesRegex(ValueError, 'Unknown'):
                    ci.check_alias('sha256:legacy', selected, [self.first, second], self.ancestor,
                                   endpoint=next(iter(self.aliases)))
        # Closing the bootstrap window does not disable normal forward alias maintenance.
        ci.check_alias(self.first['image']['digest'], self.second, [self.first, self.second], self.ancestor,
                       endpoint=next(iter(self.aliases)))
        ci.check_alias(self.second['image']['digest'], self.second, [self.first, self.second], self.ancestor)

    def test_fork_or_newer_unselected_main_cannot_bootstrap(self):
        for row in (self.second, dict(self.second, source_sha='f' * 40)):
            with self.subTest(row=row), self.assertRaises(ValueError):
                ci.check_alias('sha256:legacy', self.first, [self.first, row], self.ancestor,
                               endpoint=next(iter(self.aliases)))

    def test_release_latest_verifies_before_bootstrap_and_uses_exact_endpoint(self):
        import hashlib
        raw = b'synthetic-legacy-index'
        endpoint = 'git.134333.xyz/eezd/sub2api:latest'
        self.aliases[endpoint] = 'sha256:' + hashlib.sha256(raw).hexdigest()
        row = dict(self.first, image={'ref': release.IMAGE + ':' + self.first['version'], 'digest': 'new'})
        events = []
        api = MagicMock()
        with patch.object(release, 'release_history', return_value=({row['version']: (api, row, {})}, set())), \
             patch.object(release, 'git', return_value=row['source_sha']), \
             patch.object(release, 'verify', side_effect=lambda *a, **kw: events.append('proof')), \
             patch.object(release, 'public_registry', side_effect=lambda *a, **kw: events.append('public')), \
             patch.object(release, 'inspect_registry', return_value=raw), \
             patch.object(release.subprocess, 'run', side_effect=lambda args, **kw:
                          events.append('copy' if args[0] == 'skopeo' else 'ancestor') or MagicMock(returncode=0)):
            release.latest(api, 'auth')
        self.assertIn('copy', events)
        self.assertLess(events.index('proof'), events.index('copy'))
        self.assertLess(events.index('public'), events.index('copy'))


if __name__ == '__main__':
    unittest.main()
