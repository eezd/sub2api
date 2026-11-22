"""Run-scoped explicit CI credentials; no developer credential discovery."""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys

REPOSITORIES = ('https://git.134333.xyz/eezd/sub2api.git', 'https://github.com/eezd/sub2api.git')
TOKENS = ('FORGEJO_RELEASE_TOKEN', 'FORGEJO_TOKEN', 'GITHUB_TOKEN', 'GITEA_TOKEN',
          'GITEA_RELEASE_TOKEN', 'GH_TOKEN', 'RELEASE_TOKEN', 'MIRROR_TOKEN')


def identity():
    values = [os.environ.get(key, '') for key in ('CI_RUN_ID', 'CI_RUN_ATTEMPT')]
    if not all(re.fullmatch(r'[1-9][0-9]*', value) for value in values):
        raise ValueError('Invalid CI run/attempt')
    return '-'.join(values)


def path_for(role):
    if role not in ('release', 'sync'):
        raise ValueError('Invalid delivery role')
    parent = Path(os.environ['RUNNER_TEMP']).resolve()
    return parent / f'sub2api-{role}-{identity()}'


def cleanup_auth(role):
    path = path_for(role)
    if path.is_symlink():
        raise ValueError('Refusing symlink auth cleanup')
    if path.exists():
        shutil.rmtree(path)


def child_env():
    environment = {key: value for key, value in os.environ.items() if key not in TOKENS and not key.startswith(
        ('GIT_CONFIG_KEY_', 'GIT_CONFIG_VALUE_')) and key not in (
        'GIT_DIR', 'GIT_WORK_TREE', 'GIT_COMMON_DIR', 'GIT_OBJECT_DIRECTORY',
        'GIT_ALTERNATE_OBJECT_DIRECTORIES', 'GIT_NAMESPACE', 'GIT_CONFIG_PARAMETERS', 'GIT_CONFIG_COUNT')}
    if os.environ.get('CI_GIT_HELPER'):
        environment.update(GIT_CONFIG_COUNT='3', GIT_CONFIG_KEY_0='credential.helper', GIT_CONFIG_VALUE_0='',
                           GIT_CONFIG_KEY_1='credential.helper', GIT_CONFIG_VALUE_1=os.environ['CI_GIT_HELPER'],
                           GIT_CONFIG_KEY_2='credential.useHttpPath', GIT_CONFIG_VALUE_2='true')
    return environment


@contextmanager
def credentials(role, tokens):
    # Validate *all* required Secrets before API/Git/Registry operations.
    if not tokens or not all(isinstance(value, str) and value.strip() for value in tokens.values()):
        raise ValueError('Missing Actions Secrets: RELEASE_TOKEN' + (' / MIRROR_TOKEN' if role == 'sync' else ''))
    path = path_for(role)
    path.mkdir(mode=0o700)  # Existing state is never adopted.
    old = dict(os.environ)
    try:
        home, xdg = path / 'home', path / 'xdg'
        home.mkdir(mode=0o700)
        xdg.mkdir(mode=0o700)
        credentials_file = path / 'credentials.json'
        credentials_file.write_text(json.dumps(tokens))
        credentials_file.chmod(0o600)
        helper = path / 'git-credentials.py'
        helper.write_text('''import json, sys
from pathlib import Path
if sys.argv[-1] != 'get':
    sys.exit(0)
fields = dict(line.rstrip('\\n').split('=', 1) for line in sys.stdin if '=' in line)
url = fields.get('protocol', '')+'://'+fields.get('host', '')+'/'+fields.get('path', '')
token = json.loads(Path(sys.argv[1]).read_text()).get(url)
if token:
    print('username=x-access-token\\npassword='+token)
''')
        config = path / 'gitconfig'
        config.write_text('[credential]\n\thelper =\n\tuseHttpPath = true\n\thelper = !' +
                          shlex.join([sys.executable, str(helper), str(credentials_file)]) + '\n' +
                          '[core]\n\thooksPath = /dev/null\n')
        config.chmod(0o600)
        os.environ.update(HOME=str(home), XDG_CONFIG_HOME=str(xdg), XDG_RUNTIME_DIR=str(xdg),
                          GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(config), GIT_TERMINAL_PROMPT='0',
                          CI_GIT_HELPER='!' + shlex.join([sys.executable, str(helper), str(credentials_file)]),
                          DOCKER_CONFIG=str(path / 'docker'), CI_AUTH_ROOT=str(path),
                          GIT_ASKPASS='/bin/false', SSH_ASKPASS='/bin/false')
        for key in list(os.environ):
            if key.startswith(('GIT_CONFIG_KEY_', 'GIT_CONFIG_VALUE_')) or key in (
                    'GIT_CONFIG_COUNT', 'GIT_CONFIG_PARAMETERS', 'REGISTRY_AUTH_FILE'):
                os.environ.pop(key)
        auth = path / 'registry.json'
        auth.write_text('{"auths":{}}\n')
        auth.chmod(0o600)
        yield str(auth)
    finally:
        os.environ.clear()
        os.environ.update(old)
        cleanup_auth(role)


def login(authfile, registry, username, token):
    subprocess.run(['skopeo', 'login', '--authfile', authfile, '--username', username, '--password-stdin', registry],
                   input=token, text=True, check=True, env=child_env())


@contextmanager
def anonymous():
    # No Docker auth, helper, or global registries.conf may influence public checks.
    import tempfile
    with tempfile.TemporaryDirectory(prefix='sub2api-anonymous-', dir=os.environ.get('CI_AUTH_ROOT') or os.environ.get('RUNNER_TEMP')) as temp:
        path = Path(temp)
        auth = path / 'auth.json'
        auth.write_text('{"auths":{}}\n')
        auth.chmod(0o600)
        old = dict(os.environ)
        try:
            os.environ.update(HOME=temp, XDG_CONFIG_HOME=temp, XDG_RUNTIME_DIR=temp,
                              DOCKER_CONFIG=temp, REGISTRY_AUTH_FILE=str(auth))
            for key in (*TOKENS, 'CI_GIT_HELPER'):
                os.environ.pop(key, None)
            yield str(auth)
        finally:
            os.environ.clear()
            os.environ.update(old)


def newest_main(records, head, ancestor):
    main = [row for row in records if row['source_branch'] == 'main']
    if any(not ancestor(row['source_sha'], head) for row in main):
        raise ValueError('Completed main history diverges from current main')
    candidates = main
    if not candidates:
        return None
    for row in candidates:
        if all(ancestor(other['source_sha'], row['source_sha']) for other in candidates):
            return max((other for other in candidates if other['source_sha'] == row['source_sha']),
                       key=lambda other: tuple(map(int, re.findall(r'\d+', other['version']))))
    raise ValueError('Completed main releases diverge; latest is unchanged')


def delivery_history():
    value = json.loads(Path(__file__).with_name('delivery-history.json').read_bytes())
    if value['schema'] != 1:
        raise ValueError('Unknown delivery history schema')
    return value


def check_alias(raw, selected, records, ancestor, *, endpoint=None):
    if raw is None or raw == selected['image']['digest']:
        return
    known = [row for row in records if row.get('schema') == 5 and
             row['source_branch'] == 'main' and row['image']['digest'] == raw]
    if known and all(ancestor(row['source_sha'], selected['source_sha']) for row in known):
        return
    # The frozen legacy digest is a one-release transition, not permanent alias authority.
    if endpoint is not None and selected.get('schema') == 5 and selected['source_branch'] == 'main':
        transition = delivery_history()['latest_transition']
        cutoff = transition['after_commit']
        first_main = [row for row in records if row.get('schema') == 5 and
                      row['source_branch'] == 'main' and row['source_sha'] != cutoff and
                      ancestor(cutoff, row['source_sha'])]
        if (transition['aliases'].get(endpoint) == raw and len(first_main) == 1 and
                first_main[0] == selected and
                newest_main(records, selected['source_sha'], ancestor) == selected):
            return
    raise ValueError('Unknown or divergent latest alias; refusing rollback/adoption')


def validate_provenance(value):
    import uuid
    inputs = value['release_inputs']
    sha = re.compile(r'[0-9a-f]{40}')
    if not (value.get('schema') == 5 and isinstance(inputs, dict) and
            set(inputs) == {'source_branch', 'source_sha'} and
            all(isinstance(v, str) and v == v.strip() for v in inputs.values()) and
            inputs['source_branch'] == value['source_branch'] and inputs['source_branch'] and
            sha.fullmatch(value['source_sha']) and sha.fullmatch(value['source_branch_head']) and
            sha.fullmatch(value['control_sha']) and
            re.fullmatch(r'[1-9][0-9]*-[1-9][0-9]*', value['release_run_id']) and
            str(uuid.UUID(value['release_request_id'])) == value['release_request_id'] and
            (inputs['source_sha'] == value['source_sha'] if inputs['source_sha'] else
             value['source_sha'] == value['source_branch_head'])):
        raise ValueError('Invalid schema5 build provenance')
