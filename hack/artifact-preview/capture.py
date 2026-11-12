#!/usr/bin/env python3
"""Convert saved workspace metadata to CLI replay or native flattening input."""
import argparse
import json
from pathlib import Path
from urllib.parse import parse_qsl, unquote, urlsplit



def comparable_name(name):
    """Compare captured CLI spelling without inventing schema field spelling."""
    return ''.join(char for char in name.casefold() if char.isalnum())


def unique_match(values, name, context):
    matches = [value for value in values if comparable_name(value) == comparable_name(name)]
    if len(matches) != 1:
        raise ValueError(f'{context}: expected one schema name for {name!r}, found {matches}')
    return matches[0]


class LocalPaths:
    # Actual engine field names from core/schema/workspace.go and generator.go.
    ENGINE_FIELDS = {'WorkspaceSDK': {'generate': 'Changeset'},
                     'Generator': {'stale': 'Check'}}

    def __init__(self, metadata, sdk_modules=()):
        self.definitions = {d['identifier']: d for d in metadata['dimensionDefinitions']}
        self.modules = {module['name']: module for module in metadata.get('modules', [])}
        self.sdk_modules = set(sdk_modules)
        self.projections = {}
        for path in metadata['pathDefinitions']:
            fields = self.fields(path)
            types = [d[5:] for d in path['dimensions'] if d.startswith('type:')]
            if len(types) != 1:
                raise ValueError(f'Expected one artifact type: {path}')
            key = (path['moduleName'], tuple(fields),
                   frozenset(d for d in path['dimensions'] if d.startswith('/')))
            previous = self.projections.setdefault(key, types[0])
            if previous != types[0]:
                raise ValueError(f'Ambiguous artifact projection at {key}: {previous}, {types[0]}')

    @staticmethod
    def fields(path):
        pairs = parse_qsl(urlsplit(path['uri']).query, keep_blank_values=True)
        if len(pairs) != 1:
            raise ValueError(f'Expected one field path: {path}')
        return pairs[0][1].split('/') if pairs[0][1] else []

    def resolve(self, path):
        module = path['moduleName']
        fields = self.fields(path)
        offset = int(bool(fields) and comparable_name(fields[0]) == comparable_name(module))
        relative = fields[offset:]
        dimensions = [d for d in path['dimensions'] if d.startswith('/')]
        objects = {obj['asObject']['name']: obj['asObject']
                   for obj in self.modules.get(module, {}).get('objects', []) if obj.get('asObject')}
        if module in self.sdk_modules and relative[:1] == ['generate'] and not dimensions:
            current = 'WorkspaceSDK'
        else:
            current = unique_match(objects, module, f'Module {module}')
        boundaries = {}
        for dimension in dimensions:
            definition = self.definitions[dimension]
            route = dimension.lstrip('/').split('/')
            if route[0] != module:
                raise ValueError(f'Collection {dimension} is outside module {module}')
            boundaries[tuple(route[1:])] = definition
        active = []
        local = []
        for index, segment in enumerate(relative):
            if current in self.ENGINE_FIELDS:
                candidates = self.ENGINE_FIELDS[current]
                actual = unique_match(candidates, segment, f'Engine object {current}')
                returns = candidates[actual]
            else:
                if current not in objects:
                    raise ValueError(f'{module}/{"/".join(relative[:index])}: no object metadata for {current}')
                obj = objects[current]
                candidates = obj.get('fields', []) + obj.get('functions', [])
                actual = unique_match([field['name'] for field in candidates], segment,
                                      f'Object {current}')
                field = next(field for field in candidates if field['name'] == actual)
                result = field.get('typeDef') or field.get('returnType') or {}
                returns = (result.get('asObject') or {}).get('name')
            local.append(actual)
            prefix = tuple(fields[:offset + index + 1])
            # @generate changes a Changeset return into Generator. @check can
            # also project a function whose original return is void.
            current = self.projections.get((module, prefix, frozenset(active)), returns)
            boundary = boundaries.get(tuple(relative[:index + 1]))
            if boundary is not None:
                if current != boundary['collectionType']:
                    raise ValueError(f'{module}/{"/".join(relative[:index+1])}: '
                                     f'expected collection {boundary["collectionType"]}, found {current}')
                active.append(boundary['identifier'])
                current = boundary['itemType']
                local = []
        if active != dimensions:
            raise ValueError(f'Collection route was not resolved: {dimensions}; found {active}')
        return local

def format_entries(entries, output_format):
    """Convert resolved CLI entries without changing their schema or keys."""
    if output_format == 'cli':
        return entries
    if output_format != 'flattening':
        raise ValueError(f'Unknown output format: {output_format}')
    return [
        {
            'module': entry['Module'],
            'type': entry['Type'],
            'localPath': list(entry['LocalPath']),
            'collections': [
                {'fields': list(collection['Fields']),
                 'type': collection['Type'],
                 'itemType': collection['ItemType'],
                 **({'keyName': collection['KeyName']} if 'KeyName' in collection else {}),
                 **({'keyDescription': collection['KeyDescription']}
                    if 'KeyDescription' in collection else {})}
                for collection in entry['Collections']
            ],
            'items': [list(item['Keys']) for item in entry['Items']],
        }
        for entry in entries
    ]


def decode_rows(payload):
    return json.loads(payload) if isinstance(payload, str) else payload


def source_path(row):
    """Read an explicit source path, or the path-form DAG URI."""
    if 'path' in row:
        return list(row['path'])
    address = urlsplit(row['uri'])
    path = (address.netloc + address.path).strip('/')
    if not path and address.query:
        raise ValueError('Expected an explicit source path or a path-form URI.')
    return [unquote(segment) for segment in path.split('/')] if path else []


def capture_entries(metadata, listing, sdk_modules=(), selected_types=None):
    """Use exact projections for modern captures and retain the old adapter."""
    paths = (decode_rows(metadata['__listing']) if '__listing' in metadata
             else metadata['pathDefinitions'])
    collection_ids = {d['identifier'] for d in metadata['dimensionDefinitions']
                      if d['kind'] == 'COLLECTION'}
    modern = any(not identifier.startswith('/') for identifier in collection_ids)
    if modern and any(path.get('projection') is None for path in paths):
        raise ValueError('Modern collection IDs require schema projection metadata; '
                         'capture __listing(schema:true,typeAssertion:true).')
    legacy_paths = [path for path in paths if path.get('projection') is None]
    legacy_metadata = dict(metadata, pathDefinitions=legacy_paths)
    local_paths = LocalPaths(legacy_metadata, sdk_modules) if legacy_paths else None
    definitions = {d['identifier']: d for d in metadata['dimensionDefinitions']}
    aliases = {d['name']: d['identifier'] for d in definitions.values() if d['kind'] == 'COLLECTION'}
    entries = []
    by_path = {}
    schemes = {}
    for path in paths:
        projection = path.get('projection')
        if projection is not None:
            typ = projection['type']
            module = projection['module']
            fields = source_path(path)
            local_path = list(projection['localPath'] or [])
            collections = []
            for collection in projection.get('collections') or []:
                # Go's Collection fields retain their exported JSON names.
                copied = dict(collection)
                copied['Fields'] = list(collection.get('Fields') or [])
                collections.append(copied)
            dimensions = [collection['Type'] for collection in collections]
        else:
            if modern:
                raise ValueError('Modern collection IDs require schema projection metadata; '
                                 'capture __listing(schema:true,typeAssertion:true).')
            types = [d[5:] for d in path['dimensions'] if d.startswith('type:')]
            if len(types) != 1:
                raise ValueError(f'Expected one artifact type: {path}')
            typ = types[0]
            module = path['moduleName']
            fields = LocalPaths.fields(path)
            dimensions = [d for d in path['dimensions'] if d.startswith('/')]
            collections = []
            parent = '/' + module
            for dimension in dimensions:
                definition = definitions[dimension]
                if not dimension.startswith(parent + '/'):
                    raise ValueError(f'Invalid collection route: {dimension}')
                collections.append({
                    'Fields': dimension[len(parent) + 1:].split('/'),
                    'Type': definition['collectionType'],
                    'ItemType': definition['itemType'],
                })
                parent = dimension
            local_path = local_paths.resolve(path)
        address = urlsplit(path['uri'])
        schemes[address.scheme] = typ
        entry = {'Module': module, 'Path': fields, 'Type': typ,
                 'Collections': collections, 'LocalPath': local_path,
                 'Description': path.get('description', '') if projection is not None else '',
                 'Items': []}
        key = (module, tuple(fields), typ, frozenset(dimensions))
        if key in by_path:
            raise ValueError(f'Duplicate schema path: {key}')
        by_path[key] = (entry, dimensions)
        entries.append(entry)
    for item in listing:
        address = urlsplit(item['uri'])
        typ = schemes[address.scheme]
        coordinates = {}
        if 'dimensionKeys' in item or 'coordinates' in item:
            # Full metadata remains valid when the URI omits redundant keys.
            pairs = [(key['dimension'], key['key'])
                     for key in item.get('dimensionKeys', item.get('coordinates', []))
                     if key['dimension'] in collection_ids]
        else:
            if modern:
                raise ValueError('Modern listing rows require dimensionKeys or coordinates.')
            # Older captures retain every collection key in their URI.
            pairs = [(aliases[name], value)
                     for name, value in parse_qsl(address.query, keep_blank_values=True)
                     if name in aliases]
        for identifier, value in pairs:
            if identifier in coordinates:
                raise ValueError(f'Repeated collection coordinate: {item["uri"]}')
            coordinates[identifier] = value
        key = (item['moduleName'], tuple(source_path(item)), typ, frozenset(coordinates))
        entry, dimensions = by_path[key]
        description = item.get('description', '')
        if entry['Description'] and entry['Description'] != description:
            raise ValueError(f'Field description varies by item: {key}')
        entry['Description'] = description
        if selected_types is None or typ in selected_types:
            entry['Items'].append({'Keys': [coordinates[d] for d in dimensions],
                                   'Description': description})
    return entries


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--schema', type=Path, required=True)
    parser.add_argument('--listing', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--format', choices=('cli', 'flattening'), default='cli',
                        help='Output format. The default is CLI replay.')
    parser.add_argument('--sdk-module', action='append', default=[],
                        help='Installed SDK provider module with WorkspaceSDK.generate. Repeat as needed.')
    parser.add_argument('--types', nargs='+', help='Keep items of these types. Retain all schema paths.')
    args = parser.parse_args()
    metadata = json.loads(args.schema.read_text())['currentWorkspace']['artifacts']
    payload = json.loads(args.listing.read_text())['currentWorkspace']['artifacts']['__listing']
    entries = capture_entries(metadata, decode_rows(payload), args.sdk_module, args.types)
    args.output.write_text(json.dumps(format_entries(entries, args.format), separators=(',', ':')) + '\n')
    print(f'{len(entries)} schema paths; {sum(len(e["Items"]) for e in entries)} items')


if __name__ == '__main__':
    main()
