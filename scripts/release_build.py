"""Build on the dedicated release worker; candidate stdout and files are untrusted."""

from delivery_ci import child_env
from contextlib import ExitStack
import os
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = tuple(f'{system}/{arch}' for system in ('linux', 'darwin', 'windows') for arch in ('amd64', 'arm64'))
IMAGE = 'git.134333.xyz/eezd/sub2api'
LIMIT = 90_000_000
TEST_DOCKER_IMAGE = 'docker:29.10.0-dind@sha256:4bbc7223f81db790097c6dea42fb93e05b8fead4f0431fbe6ccaba6e8473201c'


def run(*args, **kwargs):
    kwargs.setdefault('env', child_env())
    return subprocess.check_output(args, text=True, **kwargs).strip()


def names(version):
    return [f'sub2api_{version}_{target.replace("/", "_")}' + ('.zip' if target.startswith('windows/') else '.tar.gz')
            for target in PLATFORMS]


def sha(path):
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def oci_image(path, version):
    with tarfile.open(path) as archive:
        index = json.load(archive.extractfile('index.json'))
        if len(index['manifests']) != 1:
            raise ValueError('Expected one version in the OCI archive')
        top = index['manifests'][0]['digest']
        manifest = json.load(archive.extractfile('blobs/' + top.replace(':', '/')))
        platforms = {f'{m["platform"]["os"]}/{m["platform"]["architecture"]}': m['digest'] for m in manifest['manifests']}
        if len(manifest['manifests']) != 2 or set(platforms) != {'linux/amd64', 'linux/arm64'}:
            raise ValueError('Expected exactly two Linux OCI platforms')
        for descriptor in manifest['manifests']:
            digest = descriptor['digest']
            if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
                raise ValueError('Invalid OCI platform digest')
            image = json.load(archive.extractfile('blobs/' + digest.replace(':', '/')))
            if any(blob['size'] >= LIMIT for blob in [image['config'], *image['layers']]):
                raise ValueError('An OCI blob exceeds the upload budget')
    return {'ref': f'{IMAGE}:{version}', 'digest': top, 'platforms': platforms}


def cleanup(*args):
    result = subprocess.run(args, check=False, stdout=subprocess.DEVNULL, env=child_env())
    if result.returncode:
        print('Release worker could not clean up:', ' '.join(args[:3]), flush=True)


def copy_artifact(container, name, output, limit=LIMIT):
    # Copy just one regular file, never extract candidate-controlled tar paths on the worker.
    with subprocess.Popen(['docker', 'cp', f'{container}:/work/output/{name}', '-'], stdout=subprocess.PIPE, env=child_env()) as copy:
        try:
            with tarfile.open(fileobj=copy.stdout, mode='r|') as stream:
                entry = stream.next()
                if entry is None or entry.name != name or not entry.isfile() or not 0 < entry.size < limit:
                    raise ValueError(f'Invalid or oversized candidate artifact: {name}')
                with output.open('xb') as target:
                    shutil.copyfileobj(stream.extractfile(entry), target)
                if stream.next() is not None:
                    raise ValueError(f'Unexpected extra files in {name}')
        finally:
            copy.stdout.close()
        if copy.wait():
            raise ValueError(f'Could not retrieve candidate artifact: {name}')


def wait_for(*command):
    for _ in range(60):
        if not subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=child_env()).returncode:
            return
        time.sleep(1)
    raise RuntimeError(f'Isolated dependency did not become ready: {command[-1]}')


def build(output, record, run_id):
    version, commit = record['version'], record['source_sha']
    prefix = f'sub2api-release-{run_id}'
    with ExitStack() as stack:
        scratch = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix=prefix, dir=output)))
        builder_context = scratch / 'builder'
        builder_context.mkdir()
        shutil.copyfile(ROOT / 'deploy/Dockerfile.release-builder', builder_context / 'Dockerfile')
        builder_image = prefix + '-candidate'
        stack.callback(cleanup, 'docker', 'image', 'rm', builder_image)
        subprocess.run(['docker', 'build', '-t', builder_image, str(builder_context)], check=True, env=child_env())
        stack.callback(cleanup, 'docker', 'network', 'rm', prefix)
        run('docker', 'network', 'create', prefix)

        def sidecar(name, image, *flags):
            container = prefix + '-' + name
            stack.callback(cleanup, 'docker', 'rm', '-f', '-v', container)
            run('docker', 'run', '-d', '--name', container, '--network', prefix, '--network-alias', name, *flags, image)
            return container

        postgres = sidecar('postgres', 'postgres:18-alpine', '-e', 'POSTGRES_USER=sub2api',
                           '-e', 'POSTGRES_PASSWORD=fixture', '-e', 'POSTGRES_DB=sub2api_test')
        redis = sidecar('redis', 'redis:8-alpine')
        wait_for('docker', 'exec', postgres, 'pg_isready', '-h', '127.0.0.1', '-U', 'sub2api')
        wait_for('docker', 'exec', redis, 'redis-cli', 'ping')
        run('docker', 'exec', postgres, 'createdb', '-U', 'sub2api', 'sub2api_smoke')
        run('docker', 'exec', postgres, 'createdb', '-U', 'sub2api', 'sub2api_audit')

        # This daemon and its anonymous data/certificate volumes belong only to this task.
        # The alias resolves here, not to the Runner's parent daemon on its separate network.
        test_docker = sidecar('docker', TEST_DOCKER_IMAGE, '--privileged',
                              '-e', 'DOCKER_TLS_CERTDIR=/certs', '-v', '/certs', '-v', '/var/lib/docker')
        wait_for('docker', 'exec', test_docker, 'docker', '--host', 'unix:///var/run/docker.sock', 'info')
        certificates = scratch / 'test-docker-certs'
        certificates.mkdir(mode=0o700)
        for name in ('ca.pem', 'cert.pem', 'key.pem'):
            target = certificates / name
            run('docker', 'cp', f'{test_docker}:/certs/client/{name}', str(target))
            # Only this disposable copy is made readable for the non-root candidate.
            target.chmod(0o444)
        certificates.chmod(0o755)

        container = prefix + '-build'
        stack.callback(cleanup, 'docker', 'rm', '-f', '-v', container)
        run('docker', 'create', '--name', container, '--network', prefix, '--cap-drop=ALL',
            '--security-opt=no-new-privileges', '-e', f'VERSION={version}', '-e', f'SOURCE_SHA={commit}',
            '-e', f'BUILT_AT={record["built_at"]}', '-e', 'DOCKER_HOST=tcp://docker:2376',
            '-e', 'DOCKER_TLS_VERIFY=1', '-e', 'DOCKER_CERT_PATH=/work/test-docker-certs',
            '-e', 'TESTCONTAINERS_HOST_OVERRIDE=docker',
            builder_image, '/bin/bash', '/work/build.sh')
        run('docker', 'cp', str(certificates), container + ':/work/test-docker-certs')
        run('docker', 'cp', str(ROOT / 'scripts/build-release.sh'), container + ':/work/build.sh')
        run('docker', 'cp', str(ROOT / 'scripts/package-release.sh'), container + ':/work/package.sh')
        # Extract the candidate only inside its container; it sees no .git, runner filesystem or credentials.
        with subprocess.Popen(['git', '-C', str(ROOT), 'archive', commit], stdout=subprocess.PIPE, env=child_env()) as archive:
            try:
                subprocess.run(['docker', 'cp', '-', container + ':/work/source'], stdin=archive.stdout, check=True, env=child_env())
            finally:
                archive.stdout.close()
            if archive.wait():
                raise RuntimeError('Could not export fixed source commit')
        # docker cp creates root-owned source files; give the non-root builder its own writable copy.
        # The entrypoint file it receives is trusted and does not change runner files.
        # Candidate output is not trusted workflow-command input.
        with (scratch / 'candidate.log').open('wb') as log:
            status = subprocess.run(['docker', 'start', '-a', container], stdout=log, stderr=subprocess.STDOUT, env=child_env())
        if status.returncode or run('docker', 'inspect', '-f', '{{.State.ExitCode}}', container) != '0':
            # repr keeps all control characters and newlines out of the Actions command stream.
            with (scratch / 'candidate.log').open('rb') as log:
                log.seek(max(0, log.seek(0, 2) - 8192))
                print('Candidate log tail:', repr(log.read()), flush=True)
            raise RuntimeError('Isolated source checks or build failed')
        for name in names(version):
            copy_artifact(container, name, output / name)

        context = scratch / 'image'
        (context / 'backend').mkdir(parents=True)
        for relative in ('Dockerfile.goreleaser', 'deploy/docker-entrypoint.sh', 'backend/resources',
                         'THIRD_PARTY_NOTICES.md', 'LICENSES/BEpusdt-GPL-3.0.txt'):
            source, destination = ROOT / relative, context / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            if source.is_dir():
                shutil.copytree(source, destination)
            else:
                shutil.copyfile(source, destination)
        dockerfile = context / 'Dockerfile.goreleaser'
        content = dockerfile.read_text()
        instruction = 'COPY sub2api /app/sub2api'
        if content.count(instruction) != 1:
            raise ValueError('Trusted image packaging contract changed')
        dockerfile.write_text(content.replace(instruction, 'ARG TARGETARCH\nCOPY --chmod=0755 sub2api-linux-${TARGETARCH} /app/sub2api'))
        for arch in ('amd64', 'arm64'):
            name = 'sub2api-linux-' + arch
            copy_artifact(container, name, context / name, 180_000_000)
            package_name = f'sub2api_{version}_linux_{arch}'
            with tarfile.open(output / (package_name + '.tar.gz')) as package:
                binary = package.getmember(package_name + '/sub2api')
                if not binary.isfile() or hashlib.file_digest(package.extractfile(binary), 'sha256').hexdigest() != sha(context / name):
                    raise ValueError('Image binary differs from the verified package')
        builder = prefix + '-oci'
        docker_context = prefix + '-context'
        stack.callback(cleanup, 'docker', 'context', 'rm', docker_context)
        run('docker', 'context', 'create', docker_context)
        stack.callback(cleanup, 'docker', 'buildx', 'rm', builder)
        run('docker', 'buildx', 'create', '--name', builder, '--driver', 'docker-container', docker_context)
        subprocess.run(['docker', 'buildx', 'build', '--builder', builder, '--platform', 'linux/amd64,linux/arm64',
                        '--provenance=false', '--sbom=false', '--file', str(dockerfile), '--tag', f'{IMAGE}:{version}',
                        '--output', f'type=oci,dest={output / "image.oci"}', str(context)], check=True, env=child_env())
        packages = [{'name': name, 'sha256': sha(output / name)} for name in names(version)]
        (output / 'SHA256SUMS').write_text(''.join(f'{item["sha256"]}  {item["name"]}\n' for item in packages))
        metadata = {key: record[key] for key in ('version', 'source_sha', 'source_branch', 'previous_tag', 'built_at')}
        if record.get('schema') == 5:
            metadata.update({key: record[key] for key in ('schema', 'source_branch_head', 'release_inputs',
                             'control_sha', 'release_run_id', 'release_request_id')})
        metadata.update(packages=packages, image=oci_image(output / 'image.oci', version), verification={
            'linux/amd64': 'native: version, health, setup, login and embedded UI',
            'cross_compiled': sorted(set(PLATFORMS) - {'linux/amd64'}),
            'checks': ['frontend', 'backend-unit', 'backend-integration', 'golangci-lint'],
        })
        (output / 'build.json').write_text(json.dumps(metadata, sort_keys=True, indent=2) + '\n')


def cleanup_run(run_id):
    if not re.fullmatch(r'[1-9][0-9]*-[1-9][0-9]*', run_id):
        raise ValueError('Invalid cleanup run/attempt')
    prefix = 'sub2api-release-' + run_id
    cleanup('docker', 'buildx', 'rm', prefix + '-oci')
    cleanup('docker', 'context', 'rm', prefix + '-context')
    for name in ('build', 'docker', 'redis', 'postgres'):
        cleanup('docker', 'rm', '-f', '-v', prefix + '-' + name)
    cleanup('docker', 'network', 'rm', prefix)
    cleanup('docker', 'image', 'rm', prefix + '-candidate')
