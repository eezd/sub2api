"""HTTPS transport for the Gitea edge. Do not follow redirects or replay requests."""

from contextlib import contextmanager
from email.parser import BytesHeaderParser
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import urlsplit


@contextmanager
def request(url, authorization, *, method='GET', data=None, headers=None, timeout=30, limit=None):
    target = urlsplit(url)
    if (target.scheme != 'https' or not target.hostname or target.username is not None or
            target.password is not None):
        raise ValueError('Gitea request requires an HTTPS URL without credentials')
    if authorization is not None and not re.fullmatch(r'token [A-Za-z0-9_.-]+', authorization):
        raise ValueError('Invalid Gitea token format')
    with tempfile.TemporaryDirectory(prefix='sub2api-http-') as scratch, tempfile.TemporaryFile() as body:
        directory = Path(scratch)
        header_path = directory / 'headers'
        command = ['curl', '-q', '--config', '-', '--noproxy', '*', '--globoff', '--proto', '=https',
                   '--silent', '--show-error', '--max-time', str(timeout), '--request', method,
                   '--dump-header', str(header_path)]
        if limit is not None:
            command += ['--max-filesize', str(limit)]
        for name, value in (headers or {}).items():
            command += ['--header', f'{name}: {value}']
        if data is not None:
            payload = directory / 'request'
            payload.write_bytes(data)
            command += ['--data-binary', '@' + str(payload)]
        env = {key: value for key, value in os.environ.items()
               if key not in ('GITEA_TOKEN', 'GITEA_RELEASE_TOKEN', 'GITHUB_TOKEN')}
        config = f'header = {json.dumps("Authorization: " + authorization)}\n' if authorization else ''
        result = subprocess.run([*command, '--url', url], input=config.encode(), stdout=body,
                                stderr=subprocess.PIPE, env=env)
        if result.returncode:
            raise OSError(f'curl exit {result.returncode}')
        blocks = header_path.read_bytes().split(b'\r\n\r\n')
        response = next((block for block in reversed(blocks) if block.startswith(b'HTTP/')), b'')
        status_line, _, fields = response.partition(b'\r\n')
        status = re.match(rb'^HTTP/\S+ ([1-5][0-9]{2})(?: |$)', status_line)
        if status is None:
            raise OSError('Missing HTTP status from curl')
        body.seek(0)
        yield int(status[1]), BytesHeaderParser().parsebytes(fields), body
