"""Check schema field spelling and collection boundaries in saved captures."""
import copy
import json
import unittest

from capture import LocalPaths, capture_entries, format_entries


def field(name, returns):
    return {'name': name, 'typeDef': {'asObject': {'name': returns}}}


def path(fields, typ, dimensions=()):
    return {'moduleName': 'app', 'uri': f'dag://?type={fields}',
            'dimensions': ['module', 'type:' + typ, *dimensions]}


def metadata(paths, objects, dimensions=()):
    return {'pathDefinitions': paths, 'dimensionDefinitions': list(dimensions),
            'modules': [{'name': 'app', 'objects': [
                {'asObject': {'name': name, 'fields': fields, 'functions': []}}
                for name, fields in objects.items()]}]}


class LocalPathsTest(unittest.TestCase):
    def test_exact_schema_spelling(self):
        target = path('app/python-310/unit', 'Check')
        captured = metadata([target], {'App': [field('python310', 'Version')],
                                       'Version': [field('unit', 'Check')]})
        self.assertEqual(['python310', 'unit'], LocalPaths(captured).resolve(target))

    def test_collection_field_resets_only_for_item_boundary(self):
        collection = path('app/items/verify', 'Check')
        item = path('app/items/verify', 'Check', ['/app/items'])
        root = path('app/items', 'AppItem', ['/app/items'])
        captured = metadata([collection, item, root], {
            'App': [field('items', 'AppItems')],
            'AppItems': [field('verify', 'Check')],
            'AppItem': [field('verify', 'Check')],
        }, [{'identifier': '/app/items', 'collectionType': 'AppItems',
             'itemType': 'AppItem'}])
        resolver = LocalPaths(captured)
        self.assertEqual(['items', 'verify'], resolver.resolve(collection))
        self.assertEqual(['verify'], resolver.resolve(item))
        self.assertEqual([], resolver.resolve(root))

    def test_generator_projection_preserves_stale_tail(self):
        generator = path('app/generate', 'Generator')
        stale = path('app/generate/stale', 'Check')
        captured = metadata([generator, stale], {'App': [field('generate', 'Changeset')]})
        self.assertEqual(['generate', 'stale'], LocalPaths(captured).resolve(stale))

    def test_ambiguous_spelling_is_rejected(self):
        target = path('app/python-310', 'Check')
        captured = metadata([target], {'App': [field('python310', 'Check'),
                                              field('python_310', 'Check')]})
        with self.assertRaisesRegex(ValueError, 'expected one schema name'):
            LocalPaths(captured).resolve(target)

    def test_bound_sdk_root_requires_explicit_provider(self):
        target = path('app/generate', 'Generator')
        captured = metadata([target], {})
        with self.assertRaisesRegex(ValueError, 'Module app'):
            LocalPaths(captured).resolve(target)
        self.assertEqual(['generate'], LocalPaths(captured, ['app']).resolve(target))


class OutputFormatTest(unittest.TestCase):
    def test_native_input_preserves_roots_keys_and_empty_collections(self):
        captured = [
            {'Module': 'app', 'Path': ['app', 'items'], 'Type': 'AppItem',
             'LocalPath': [], 'Description': 'Item root',
             'Collections': [{'Fields': ['items'], 'Type': 'AppItems',
                              'ItemType': 'AppItem'}],
             'Items': [{'Keys': [''], 'Description': 'Empty key'}]},
            {'Module': 'app', 'Path': ['app', 'items', 'health-check'],
             'Type': 'Check', 'LocalPath': ['healthCheck'], 'Description': '',
             'Collections': [{'Fields': ['items'], 'Type': 'AppItems',
                              'ItemType': 'AppItem'}], 'Items': []},
            {'Module': 'app', 'Path': ['app'], 'Type': 'Container',
             'LocalPath': [], 'Description': 'Root', 'Collections': [],
             'Items': [{'Keys': [], 'Description': 'Root'}]},
        ]
        before = copy.deepcopy(captured)
        native = format_entries(captured, 'flattening')
        self.assertEqual([
            {'module': 'app', 'type': 'AppItem', 'localPath': [],
             'collections': [{'fields': ['items'], 'type': 'AppItems',
                              'itemType': 'AppItem'}], 'items': [['']]},
            {'module': 'app', 'type': 'Check', 'localPath': ['healthCheck'],
             'collections': [{'fields': ['items'], 'type': 'AppItems',
                              'itemType': 'AppItem'}], 'items': []},
            {'module': 'app', 'type': 'Container', 'localPath': [],
             'collections': [], 'items': [[]]},
        ], native)
        self.assertEqual(before, captured)
        # The result owns its lists. Later edits cannot change the capture.
        native[0]['collections'][0]['fields'].append('changed')
        native[0]['items'][0].append('changed')
        native[1]['localPath'].append('changed')
        self.assertEqual(before, captured)

    def test_cli_format_preserves_capture(self):
        captured = [{'Module': 'app', 'Items': []}]
        self.assertEqual(captured, format_entries(captured, 'cli'))


class ModernCaptureTest(unittest.TestCase):
    @staticmethod
    def schema():
        return {
            'dimensionDefinitions': [
                {'identifier': 'AppItems', 'kind': 'COLLECTION', 'name': 'app-item'},
                {'identifier': 'NestedItems', 'kind': 'COLLECTION', 'name': 'nested-item'},
                {'identifier': 'name', 'kind': 'NAME', 'name': 'name'},
            ],
            '__listing': [
                {'uri': 'dag+check://app/items/health-check',
                 'path': ['app', 'items', 'health-check'], 'moduleName': 'app',
                 'projection': {'module': 'app', 'type': 'Check',
                                'localPath': ['healthCheck'], 'collections': [
                                    {'Fields': ['items'], 'Type': 'AppItems',
                                     'ItemType': 'AppItem', 'KeyName': 'name',
                                     'KeyDescription': 'Item name'}]}},
                {'uri': 'dag+container://app/items', 'moduleName': 'app',
                 'projection': {'module': 'app', 'type': 'Container',
                                'localPath': [], 'collections': [
                                    {'Fields': ['items'], 'Type': 'AppItems',
                                     'ItemType': 'AppItem'},
                                    {'Fields': [], 'Type': 'NestedItems',
                                     'ItemType': 'NestedItem'}]}},
            ],
        }

    def test_type_ids_use_exact_projection_and_raw_keys(self):
        metadata = self.schema()
        before = copy.deepcopy(metadata)
        listing = [{'uri': 'dag+check://?app-item=wrong', 'moduleName': 'app',
                    'path': ['app', 'items', 'health-check'],
                    'dimensionKeys': [{'dimension': 'AppItems', 'key': ''},
                                      {'dimension': 'name', 'key': 'healthCheck'}]}]
        entries = capture_entries(metadata, listing)
        self.assertEqual([
            {'module': 'app', 'type': 'Check', 'localPath': ['healthCheck'],
             'collections': [{'fields': ['items'], 'type': 'AppItems',
                              'itemType': 'AppItem', 'keyName': 'name',
                              'keyDescription': 'Item name'}], 'items': [['']]},
            {'module': 'app', 'type': 'Container', 'localPath': [],
             'collections': [{'fields': ['items'], 'type': 'AppItems',
                              'itemType': 'AppItem'},
                             {'fields': [], 'type': 'NestedItems',
                              'itemType': 'NestedItem'}], 'items': []},
        ], format_entries(entries, 'flattening'))
        self.assertEqual(['app', 'items'], entries[1]['Path'])
        self.assertEqual(before, metadata)
        # The same projection works on pathDefinitions-based schema captures.
        metadata['pathDefinitions'] = metadata.pop('__listing')
        self.assertEqual(entries, capture_entries(metadata, listing))

    def test_direct_nested_collections_keep_key_order_and_empty_root(self):
        metadata = self.schema()
        metadata['__listing'] = json.dumps(metadata['__listing'])
        listing = [{'uri': 'dag+container://app/items', 'moduleName': 'app',
                    'dimensionKeys': [{'dimension': 'NestedItems', 'key': ''},
                                      {'dimension': 'AppItems', 'key': 'outer'}]}]
        entries = capture_entries(metadata, listing, selected_types=['Container'])
        self.assertEqual([], entries[0]['Items'])
        self.assertEqual([{'Keys': ['outer', ''], 'Description': ''}], entries[1]['Items'])
        self.assertEqual([], entries[1]['LocalPath'])
        self.assertEqual([], entries[1]['Collections'][1]['Fields'])

    def test_modern_ids_require_projection_metadata(self):
        metadata = self.schema()
        del metadata['__listing'][0]['projection']
        with self.assertRaisesRegex(ValueError, 'require schema projection metadata'):
            capture_entries(metadata, [])

    def test_modern_listing_requires_exact_coordinates(self):
        listing = [{'uri': 'dag+check://?app-item=x', 'moduleName': 'app',
                    'path': ['app', 'items', 'health-check']}]
        with self.assertRaisesRegex(ValueError, 'require dimensionKeys or coordinates'):
            capture_entries(self.schema(), listing)

    def test_old_capture_keeps_object_lookup_and_uri_alias_keys(self):
        target = path('app/items/health-check', 'Check', ['/app/items'])
        captured = metadata([target], {
            'App': [field('items', 'AppItems')],
            'AppItem': [field('healthCheck', 'Check')],
        }, [{'identifier': '/app/items', 'kind': 'COLLECTION', 'name': 'app-item',
             'collectionType': 'AppItems', 'itemType': 'AppItem'}])
        listing = [{'uri': 'dag://?app-item=', 'moduleName': 'app',
                    'path': ['app', 'items', 'health-check']}]
        self.assertEqual([
            {'Module': 'app', 'Path': ['app', 'items', 'health-check'],
             'Type': 'Check', 'LocalPath': ['healthCheck'],
             'Collections': [{'Fields': ['items'], 'Type': 'AppItems',
                              'ItemType': 'AppItem'}], 'Description': '',
             'Items': [{'Keys': [''], 'Description': ''}]},
        ], capture_entries(captured, listing))


class LegacyCollectionSpellingTest(unittest.TestCase):
    @staticmethod
    def capture():
        target = path('app/my-tests/health-check/run', 'Check', ['/app/my-tests'])
        captured = metadata([target], {
            'App': [field('myTests', 'AppTests')],
            'AppTest': [field('healthCheck', 'Health')],
            'Health': [field('run', 'Check')],
        }, [{'identifier': '/app/my-tests', 'kind': 'COLLECTION', 'name': 'test',
             'collectionType': 'AppTests', 'itemType': 'AppTest'}])
        listing = [{'uri': 'dag://?test=unit', 'moduleName': 'app',
                    'path': ['app', 'my-tests', 'health-check', 'run']}]
        return captured, listing

    def test_collection_and_tail_use_literal_schema_fields(self):
        captured, listing = self.capture()
        before = copy.deepcopy(captured)
        entries = capture_entries(captured, listing)
        self.assertEqual([
            {'module': 'app', 'type': 'Check', 'localPath': ['healthCheck', 'run'],
             'collections': [{'fields': ['myTests'], 'type': 'AppTests',
                              'itemType': 'AppTest'}], 'items': [['unit']]},
        ], format_entries(entries, 'flattening'))
        ordinary_fields = (entries[0]['Collections'][0]['Fields']
                           + entries[0]['LocalPath'])
        self.assertEqual('myTests.healthCheck.run', '.'.join(ordinary_fields))
        self.assertEqual(before, captured)

    def test_missing_collection_field_metadata_is_rejected(self):
        captured, listing = self.capture()
        captured['modules'][0]['objects'][0]['asObject']['fields'] = []
        with self.assertRaisesRegex(ValueError, 'Object App: expected one schema name'):
            capture_entries(captured, listing)


if __name__ == '__main__':
    unittest.main()
