#!/usr/bin/env python3
"""Convert saved workspace metadata to input for artifact-preview."""
import argparse
import json
from pathlib import Path
from urllib.parse import parse_qsl, urlsplit


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--schema', type=Path, required=True)
    parser.add_argument('--listing', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--types', nargs='+', help='Keep items of these types. Retain all schema paths.')
    args = parser.parse_args()
    metadata = json.loads(args.schema.read_text())['currentWorkspace']['artifacts']
    definitions = {d['identifier']: d for d in metadata['dimensionDefinitions']}
    aliases = {d['name']: d['identifier'] for d in definitions.values() if d['kind'] == 'COLLECTION'}
    entries = []
    by_path = {}
    schemes = {}
    for path in metadata['pathDefinitions']:
        types = [d[5:] for d in path['dimensions'] if d.startswith('type:')]
        if len(types) != 1:
            raise ValueError(f'Expected one artifact type: {path}')
        typ = types[0]
        address = urlsplit(path['uri'])
        schemes[address.scheme] = typ
        pairs = parse_qsl(address.query, keep_blank_values=True)
        if len(pairs) != 1:
            raise ValueError(f'Expected one field path: {path}')
        fields = pairs[0][1].split('/')
        dimensions = [d for d in path['dimensions'] if d.startswith('/')]
        collections = []
        parent = '/' + path['moduleName']
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
        entry = {'Module': path['moduleName'], 'Path': fields, 'Type': typ,
                 'Collections': collections, 'Description': '', 'Items': []}
        key = (path['moduleName'], tuple(fields), typ, frozenset(dimensions))
        if key in by_path:
            raise ValueError(f'Duplicate schema path: {key}')
        by_path[key] = (entry, dimensions)
        entries.append(entry)
    payload = json.loads(args.listing.read_text())['currentWorkspace']['artifacts']['__listing']
    listing = json.loads(payload) if isinstance(payload, str) else payload
    for item in listing:
        address = urlsplit(item['uri'])
        typ = schemes[address.scheme]
        coordinates = {}
        for name, value in parse_qsl(address.query, keep_blank_values=True):
            if name in aliases:
                identifier = aliases[name]
                if identifier in coordinates:
                    raise ValueError(f'Repeated collection coordinate: {item["uri"]}')
                coordinates[identifier] = value
        key = (item['moduleName'], tuple(item['path']), typ, frozenset(coordinates))
        entry, dimensions = by_path[key]
        description = item.get('description', '')
        if entry['Description'] and entry['Description'] != description:
            raise ValueError(f'Field description varies by item: {key}')
        entry['Description'] = description
        if args.types is None or typ in args.types:
            entry['Items'].append({'Keys': [coordinates[d] for d in dimensions],
                                   'Description': description})
    args.output.write_text(json.dumps(entries, separators=(',', ':')) + '\n')
    print(f'{len(entries)} schema paths; {sum(len(e["Items"]) for e in entries)} items')


if __name__ == '__main__':
    main()
