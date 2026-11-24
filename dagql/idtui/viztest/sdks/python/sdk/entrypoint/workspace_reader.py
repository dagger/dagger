import glob, os, re, sys, tomllib
root = sys.argv[1]
sub = os.path.normpath(sys.argv[2])
vendored = sys.argv[3]
module = os.path.join(root, sub)
def fail(message):
    print('error\t' + message)
    sys.exit(0)
def load(rel, file='pyproject.toml'):
    path = os.path.normpath(os.path.join(rel, file))
    try:
        with open(os.path.join(module, path), 'rb') as f:
            return tomllib.load(f)
    except (tomllib.TOMLDecodeError, UnicodeDecodeError, OSError) as e:
        fail(f'{path} is not valid TOML: {e}')
def table(doc, *keys):
    for key in keys:
        doc = doc.get(key) if isinstance(doc, dict) else None
    return doc if isinstance(doc, dict) else {}
def strings(value, key, where):
    if value is None:
        return []
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        fail(f'{where}: {key} is not an array of strings; write it as one, such as {key} = ["a", "b"]')
    return value
def outside(path):
    joined = os.path.normpath(os.path.join(sub, path))
    return joined == '..' or joined.startswith('../')
def names_parent(value):
    if isinstance(value, str):
        return value == '..' or value.startswith('../') or '/../' in value
    if isinstance(value, dict):
        return any(names_parent(key) or names_parent(item) for key, item in value.items())
    return isinstance(value, list) and any(names_parent(item) for item in value)
def reads_beside(rel, project):
    uv = {key: value for key, value in table(project, 'tool', 'uv').items() if key != 'sources'}
    tool = dict(table(project, 'tool'), uv=uv)
    return names_parent(dict(project, tool=tool)) or os.path.exists(os.path.join(module, rel, 'uv.toml'))
def name(requirement):
    m = re.match(r'\s*([A-Za-z0-9][A-Za-z0-9._-]*)', requirement if isinstance(requirement, str) else '')
    return re.sub(r'[-_.]+', '-', m.group(1).lower()) if m else ''
doc = load('.')
members = strings(table(doc, 'tool', 'uv', 'workspace').get('members'), 'members', 'pyproject.toml')
wanted = {name(r) for r in strings(table(doc, 'project').get('dependencies'), 'dependencies', 'pyproject.toml')}
dirs = []
for member in members:
    print('member\t' + member)
    for found in sorted(glob.glob(os.path.join(module, member, 'pyproject.toml'))):
        rel = os.path.relpath(os.path.dirname(found), module)
        if rel not in dirs:
            dirs.append(rel)
docs = {'.': doc}
projects = {}
for rel in dirs:
    docs[rel] = load(rel)
    project = table(docs[rel], 'project')
    projects[rel] = (name(project.get('name')), {name(r) for r in strings(project.get('dependencies'), 'dependencies', rel + '/pyproject.toml')})
needed = []
while True:
    found = [rel for rel in dirs if rel not in needed and projects[rel][0] in wanted]
    if not found:
        break
    needed += found
    wanted = set().union(*(projects[rel][1] for rel in found))
for rel in dirs:
    if rel in needed:
        print('local\t./' + rel)
inputs = ['pyproject.toml', 'uv.lock', 'uv.toml', '.python-version'] + dirs + ([] if vendored in members else [vendored])
paths = []
sourced = {}
pending = list(docs.items())
while pending:
    rel, member = pending.pop(0)
    for value in table(member, 'tool', 'uv', 'sources').values():
        for source in value if isinstance(value, list) else [value]:
            path = source.get('path') if isinstance(source, dict) else None
            if not isinstance(path, str):
                continue
            path = os.path.normpath(os.path.join(rel, path))
            if path in paths:
                continue
            paths.append(path)
            if path not in dirs and not os.path.isabs(path) and not outside(path) and os.path.exists(os.path.join(module, path, 'pyproject.toml')):
                sourced[path] = load(path)
                pending.append((path, sourced[path]))
inputs += [path for path in paths if path not in dirs + [vendored]]
settings = [table(member, 'tool', 'uv') for member in list(docs.values()) + list(sourced.values())]
if os.path.exists(os.path.join(module, 'uv.toml')):
    settings.append(load('.', 'uv.toml'))
urls = []
for uv in settings + [table(uv, 'pip') for uv in settings]:
    indexes = uv.get('index') if isinstance(uv.get('index'), list) else [uv.get('index')]
    urls += [uv.get('index-url')] + [table(index).get('url') for index in indexes]
    for key in ('extra-index-url', 'find-links'):
        urls += uv.get(key) if isinstance(uv.get(key), list) else []
whole = any(isinstance(url, str) and '://' not in url for url in urls)
whole = whole or any('project' not in member or bool(set(table(member, 'project').get('dynamic') or []) - {'version'}) for member in list(docs.values()) + list(sourced.values()))
whole = whole or os.path.exists(os.path.join(module, 'requirements.lock'))
whole = whole or any(os.path.isabs(path) or outside(path) for path in inputs)
# The module itself installs after the source mount, so members, path sources and the
# SDK-written vendored package are what the dependency step builds.
whole = whole or any('backend-path' in table(member, 'build-system') for member in [docs[rel] for rel in dirs] + list(sourced.values()))
# A path source the walk follows reads only its own directory unless its project names
# a parent directory outside its [tool.uv.sources] or keeps a uv.toml beside it.
whole = whole or any(reads_beside(rel, project) for rel, project in sourced.items())
above = []
parts = [] if sub == '.' else sub.split('/')
for i in range(len(parts)):
    rel = os.path.join(*parts[:i]) if i else '.'
    whole = whole or any(os.path.exists(os.path.join(root, rel, f)) for f in ('pyproject.toml', 'uv.toml'))
    if os.path.exists(os.path.join(root, rel, '.python-version')):
        above.append(os.path.normpath(os.path.join(rel, '.python-version')))
for path in ['.'] if whole else above + [os.path.normpath(os.path.join(sub, p)) for p in inputs]:
    print('input\t' + path)
def text(value):
    return value if isinstance(value, str) and '\t' not in value and '\n' not in value else ''
def flag(value, default):
    return value if isinstance(value, bool) else default
project = table(doc, 'project')
dagger = table(doc, 'tool', 'dagger')
indexes = table(doc, 'tool', 'uv').get('index')
indexes = [index for index in (indexes if isinstance(indexes, list) else [indexes]) if isinstance(index, dict) and text(index.get('name')) == '']
settings = {
    'projectName': text(project.get('name')),
    'requiresPython': text(project.get('requires-python')),
    'baseImage': text(dagger.get('base-image')),
    'useUv': 'true' if flag(dagger.get('use-uv'), True) else 'false',
    'uvVersion': text(dagger.get('uv-version')),
    'vendorPath': text(table(doc, 'tool', 'uv', 'sources', 'dagger-io').get('path')),
    'indexURL': next((text(index.get('url')) for index in indexes if flag(index.get('default'), False)), ''),
    'extraIndexURL': next((text(index.get('url')) for index in indexes if not flag(index.get('default'), False)), ''),
}
for key, value in settings.items():
    print('setting\t' + key + '\t' + value)
