"""Offline lifecycle and TLS isolation tests for scripts/release_build.py."""
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import MagicMock, patch

import release_build


class ReleaseBuildTests(unittest.TestCase):
    PREFIX = 'sub2api-release-123-1'
    RECORD = dict(version='20250301-aaaaaaaa', source_sha='a' * 40,
                  source_branch='dev', previous_tag=None, built_at='2025-03-01T00:00:00Z')

    def setUp(self):
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for relative in ('deploy/Dockerfile.release-builder', 'Dockerfile.goreleaser',
                         'deploy/docker-entrypoint.sh', 'backend/resources/fixture',
                         'THIRD_PARTY_NOTICES.md', 'LICENSES/BEpusdt-GPL-3.0.txt'):
            target = self.root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('COPY sub2api /app/sub2api\n' if relative == 'Dockerfile.goreleaser' else 'fixture\n')
        (self.root / 'deploy/docker-entrypoint.sh').chmod(0o755)
        self.context_modes = None
        self.output = self.root / 'output'
        self.output.mkdir()
        self.events = []
        self.failure = None
        self.archive_output = io.BytesIO(b'fixed source archive')
        self.archive = MagicMock()
        self.archive.stdout = self.archive_output
        self.archive.__enter__.return_value = self.archive
        self.archive.wait.return_value = 0
        self.certificates = None
        self.scratch = None
        self.enterContext(patch.dict(os.environ, {'DOCKER_HOST': 'tcp://docker:2376', 'DOCKER_TLS_VERIFY': '1',
                                                  'DOCKER_CERT_PATH': '/fixture/parent-certs',
                                                  'FORGEJO_RELEASE_TOKEN': 'synthetic-publisher-secret'}, clear=True))
        self.enterContext(patch.object(release_build, 'ROOT', self.root))
        self.enterContext(patch.object(release_build, 'run', side_effect=self.command))
        self.enterContext(patch.object(release_build.subprocess, 'run', side_effect=self.process))
        self.pipe = self.enterContext(patch.object(release_build.subprocess, 'Popen', return_value=self.archive))
        self.enterContext(patch.object(release_build, 'cleanup', side_effect=lambda *args: self.events.append(('cleanup', args))))
        self.ready = self.enterContext(patch.object(release_build, 'wait_for'))
        self.enterContext(patch.object(release_build, 'copy_artifact', side_effect=self.artifact))
        self.enterContext(patch.object(release_build, 'oci_image', return_value={'ref': 'fixture'}))

    def maybe_fail(self, stage, args):
        if self.failure == stage:
            raise subprocess.CalledProcessError(1, args)

    def command(self, *args):
        self.events.append(('run', args))
        if args[:3] == ('docker', 'run', '-d') and release_build.TEST_DOCKER_IMAGE in args:
            self.maybe_fail('daemon-create', args)
        if args[:2] == ('docker', 'create'):
            self.maybe_fail('candidate-create', args)
        if args[:2] == ('docker', 'cp'):
            if args[2].startswith(self.PREFIX + '-docker:/certs/client/'):
                target = Path(args[3])
                self.scratch = target.parent.parent
                self.maybe_fail('daemon-cert-copy', args)
                self.assertEqual(stat.S_IMODE(target.parent.stat().st_mode), 0o700)
                self.assertEqual(stat.S_IMODE(target.parent.parent.stat().st_mode), 0o700)
                target.write_bytes(b'synthetic-task-certificate')
            elif args[3] == self.PREFIX + '-build:/work/test-docker-certs':
                self.maybe_fail('candidate-cert-copy', args)
                self.certificates = Path(args[2])
                self.scratch = self.certificates.parent
                self.assertEqual(stat.S_IMODE(self.certificates.stat().st_mode), 0o755)
                self.assertEqual({p.name for p in self.certificates.iterdir()}, {'ca.pem', 'cert.pem', 'key.pem'})
                for file in self.certificates.iterdir():
                    self.assertEqual(stat.S_IMODE(file.stat().st_mode), 0o444)
        if args[:3] == ('docker', 'context', 'create'):
            self.maybe_fail('context-create', args)
        if args[:2] == ('skopeo', 'copy'):
            self.maybe_fail('runtime-export', args)
        if args[:2] == ('docker', 'load'):
            self.maybe_fail('runtime-load', args)
        if args[:3] == ('docker', 'buildx', 'create'):
            self.maybe_fail('builder-create', args)
        return '0' if args[:2] == ('docker', 'inspect') else 'fixture'

    def process(self, args, **kwargs):
        self.events.append(('process', tuple(args)))
        if args[:3] == ['docker', 'cp', '-']:
            self.assertIs(kwargs['stdin'], self.archive_output)
            self.maybe_fail('source-copy', args)
        if args[:3] == ['docker', 'buildx', 'build']:
            context = Path(args[-1])
            self.context_modes = {name: stat.S_IMODE((context / name).stat().st_mode)
                                  for name in ('deploy/docker-entrypoint.sh', 'THIRD_PARTY_NOTICES.md')}
            self.maybe_fail('oci-build', args)
        if args[:2] == ['docker', 'run'] and '-version' in args:
            output = self.RECORD['version'] + ' ' + self.RECORD['source_sha']
            if self.failure == 'runtime-wrong-version':
                output = 'unexpected-version'
            elif self.failure == 'runtime-wrong-commit':
                output = self.RECORD['version'] + ' ' + 'b' * 40
            elif self.failure == 'runtime-timeout':
                raise subprocess.TimeoutExpired(args, 60)
            self.assertEqual(kwargs['timeout'], 60)
            self.assertNotIn('FORGEJO_RELEASE_TOKEN', kwargs['env'])
            return subprocess.CompletedProcess(args, int(self.failure == 'runtime-start'), stdout=output)
        return subprocess.CompletedProcess(args, int(self.failure == 'candidate-start' and args[:3] == ['docker', 'start', '-a']))

    def artifact(self, _container, name, output, *_args):
        binary = b'fixture-linux-binary'
        if name.endswith('.tar.gz'):
            with tarfile.open(output, 'w:gz') as package:
                member = tarfile.TarInfo(name.removesuffix('.tar.gz') + '/sub2api')
                member.size = len(binary)
                package.addfile(member, io.BytesIO(binary))
        else:
            output.write_bytes(binary)

    def build(self):
        release_build.build(self.output, self.RECORD, '123-1')

    def test_success_uses_task_tls_daemon_and_copies_only_its_certificates(self):
        self.build()
        commands = [args for kind, args in self.events if kind != 'cleanup']
        daemon, = [args for args in commands if release_build.TEST_DOCKER_IMAGE in args]
        self.assertEqual(daemon, ('docker', 'run', '-d', '--name', self.PREFIX + '-docker',
                                 '--network', self.PREFIX, '--network-alias', 'docker', '--privileged',
                                 '-e', 'DOCKER_TLS_CERTDIR=/certs', '-v', '/certs', '-v', '/var/lib/docker',
                                 release_build.TEST_DOCKER_IMAGE))
        self.ready.assert_any_call('docker', 'exec', self.PREFIX + '-docker', 'docker', '--host',
                                   'unix:///var/run/docker.sock', 'info')
        self.ready.assert_any_call('docker', 'exec', self.PREFIX + '-postgres', 'pg_isready',
                                   '-h', '127.0.0.1', '-U', 'sub2api')
        self.ready.assert_any_call('docker', 'exec', self.PREFIX + '-redis', 'redis-cli', 'ping')
        candidate, = [args for args in commands if args[:2] == ('docker', 'create')]
        self.assertIn('--cap-drop=ALL', candidate)
        self.assertIn('--security-opt=no-new-privileges', candidate)
        self.assertNotIn('--privileged', candidate)
        self.assertNotIn('-v', candidate)
        self.assertEqual([candidate[i + 1] for i, arg in enumerate(candidate) if arg == '-e'], [
            'VERSION=' + self.RECORD['version'], 'SOURCE_SHA=' + self.RECORD['source_sha'],
            'BUILT_AT=' + self.RECORD['built_at'], 'DOCKER_HOST=tcp://docker:2376',
            'DOCKER_TLS_VERIFY=1', 'DOCKER_CERT_PATH=/work/test-docker-certs',
            'TESTCONTAINERS_HOST_OVERRIDE=docker'])
        for args in commands:
            self.assertNotIn('/fixture/parent-certs', ' '.join(args))
            self.assertNotIn('synthetic-publisher-secret', ' '.join(args))
            self.assertNotIn(':2375', ' '.join(args))
        self.assertEqual(self.pipe.call_args.args[0], ['git', '-C', str(self.root), 'archive', 'a' * 40])
        self.assertNotIn('FORGEJO_RELEASE_TOKEN', self.pipe.call_args.kwargs['env'])
        self.assertTrue(self.archive_output.closed)
        self.assertFalse(self.scratch.exists())
        metadata = json.loads((self.output / 'build.json').read_text())
        self.assertEqual(len(metadata['packages']), 6)
        self.assertEqual(metadata['verification']['cross_compiled'], ['darwin/amd64', 'darwin/arm64',
                                                                      'linux/arm64', 'windows/amd64', 'windows/arm64'])
        self.assert_cleanup_complete(include_buildx=True, include_runtime=True)

    def test_image_context_preserves_public_modes_under_private_umask(self):
        (self.root / 'THIRD_PARTY_NOTICES.md').chmod(0o644)
        previous = os.umask(0o077)
        try:
            self.build()
        finally:
            os.umask(previous)
        self.assertEqual(self.context_modes, {'deploy/docker-entrypoint.sh': 0o755,
                                              'THIRD_PARTY_NOTICES.md': 0o644})

    def test_entrypoint_mode_is_explicit_in_runtime_dockerfile(self):
        dockerfile = (Path(__file__).resolve().parents[1] / 'Dockerfile.goreleaser').read_text()
        self.assertIn('COPY --chmod=0755 deploy/docker-entrypoint.sh /app/docker-entrypoint.sh', dockerfile)
        self.assertNotIn('RUN chmod +x /app/docker-entrypoint.sh', dockerfile)

    def test_final_oci_image_entrypoint_runs_before_metadata_is_written(self):
        self.build()
        exported, = [args for kind, args in self.events if kind == 'run' and args[:2] == ('skopeo', 'copy')]
        self.assertEqual(exported[2:6], ('--override-os', 'linux', '--override-arch', 'amd64'))
        self.assertIn('oci-archive:' + str(self.output / 'image.oci'), exported)
        runtime, = [args for kind, args in self.events if kind == 'process' and args[:2] == ('docker', 'run')]
        self.assertEqual(runtime[runtime.index('--network') + 1], 'none')
        self.assertNotIn('--entrypoint', runtime)
        self.assertEqual(runtime[-2:], (self.PREFIX + '-runtime:latest', '-version'))
        self.assertNotIn('synthetic-publisher-secret', ' '.join(runtime))
        self.assertTrue((self.output / 'build.json').exists())
        self.assert_cleanup_complete(include_buildx=True, include_runtime=True)

    def test_final_image_export_load_start_or_identity_failure_blocks_metadata_and_cleans_up(self):
        for stage in ('runtime-export', 'runtime-load', 'runtime-start', 'runtime-wrong-version',
                      'runtime-wrong-commit', 'runtime-timeout'):
            with self.subTest(stage=stage):
                self.events.clear()
                self.failure = stage
                self.archive_output = io.BytesIO(b'fixed source archive')
                self.archive.stdout = self.archive_output
                with self.assertRaises((RuntimeError, subprocess.CalledProcessError, subprocess.TimeoutExpired)):
                    self.build()
                self.assertFalse((self.output / 'build.json').exists())
                self.assert_cleanup_complete(include_buildx=True, include_runtime=True)
                self.assertFalse(self.scratch.exists())

    def test_buildx_binds_named_tls_context_and_cleans_builder_before_context(self):
        self.build()
        context = self.PREFIX + '-context'
        builder = self.PREFIX + '-oci'
        self.assertIn(('run', ('docker', 'context', 'create', context)), self.events)
        self.assertIn(('run', ('docker', 'buildx', 'create', '--name', builder, '--driver', 'docker-container', context)), self.events)
        build, = [args for kind, args in self.events if kind == 'process' and args[:3] == ('docker', 'buildx', 'build')]
        self.assertEqual(build[build.index('--builder') + 1], builder)
        cleaned = [args for kind, args in self.events if kind == 'cleanup']
        self.assertLess(cleaned.index(('docker', 'buildx', 'rm', builder)),
                        cleaned.index(('docker', 'context', 'rm', context)))

    def assert_cleanup_complete(self, *, include_buildx=False, candidate=True, include_runtime=False):
        cleaned = [args for kind, args in self.events if kind == 'cleanup']
        expected = [('docker', 'rm', '-f', '-v', self.PREFIX + '-' + name) for name in ('docker', 'redis', 'postgres')]
        if candidate:
            expected.insert(0, ('docker', 'rm', '-f', '-v', self.PREFIX + '-build'))
        expected += [('docker', 'network', 'rm', self.PREFIX), ('docker', 'image', 'rm', self.PREFIX + '-candidate')]
        if include_buildx:
            expected = [('docker', 'buildx', 'rm', self.PREFIX + '-oci'),
                        ('docker', 'context', 'rm', self.PREFIX + '-context')] + expected
        if include_runtime:
            expected = [('docker', 'rm', '-f', '-v', self.PREFIX + '-runtime'),
                        ('docker', 'image', 'rm', self.PREFIX + '-runtime:latest')] + expected
        self.assertEqual(cleaned, expected)
        self.assertFalse(any('prune' in args for _, args in self.events))

    def test_source_copy_and_candidate_start_failure_clean_all_task_resources(self):
        for stage in ('source-copy', 'candidate-start', 'candidate-cert-copy', 'candidate-create'):
            with self.subTest(stage=stage):
                self.events.clear()
                self.archive_output = io.BytesIO(b'fixed source archive')
                self.archive.stdout = self.archive_output
                self.failure = stage
                with self.assertRaises(RuntimeError if stage == 'candidate-start' else subprocess.CalledProcessError):
                    self.build()
                self.assert_cleanup_complete()
                self.assertFalse(self.scratch.exists())
                if stage in ('source-copy', 'candidate-start'):
                    self.assertTrue(self.archive_output.closed)
                self.assertFalse(any(args[:3] == ('docker', 'context', 'create') for _, args in self.events))

    def test_daemon_start_and_certificate_copy_failure_cleanup_anonymous_volumes(self):
        for stage in ('daemon-create', 'daemon-cert-copy'):
            with self.subTest(stage=stage):
                self.events.clear()
                self.failure = stage
                with self.assertRaises(subprocess.CalledProcessError):
                    self.build()
                self.assert_cleanup_complete(candidate=False)
                if stage == 'daemon-cert-copy':
                    self.assertFalse(self.scratch.exists())
                self.assertFalse(any(args[:2] == ('docker', 'create') for _, args in self.events))

    def test_daemon_health_failure_cleans_up_without_copying_certificates(self):
        self.ready.side_effect = [None, None, RuntimeError('daemon not ready')]
        with self.assertRaisesRegex(RuntimeError, 'daemon not ready'):
            self.build()
        self.assert_cleanup_complete(candidate=False)
        self.assertFalse(any(args[:2] == ('docker', 'cp') for _, args in self.events))

    def test_context_builder_and_oci_build_failure_cleanup(self):
        for stage in ('context-create', 'builder-create', 'oci-build'):
            with self.subTest(stage=stage):
                self.events.clear()
                self.failure = stage
                self.archive_output = io.BytesIO(b'fixed source archive')
                self.archive.stdout = self.archive_output
                with self.assertRaises(subprocess.CalledProcessError):
                    self.build()
                if stage == 'context-create':
                    cleaned = [args for kind, args in self.events if kind == 'cleanup']
                    self.assertEqual(cleaned[0], ('docker', 'context', 'rm', self.PREFIX + '-context'))
                    self.assertFalse(any(args[:3] == ('docker', 'buildx', 'create') for _, args in self.events))
                else:
                    self.assert_cleanup_complete(include_buildx=True)
                self.assertFalse(self.scratch.exists())


if __name__ == '__main__':
    unittest.main()
