"""Check schema field spelling and collection boundaries in saved captures."""
import unittest

from capture import LocalPaths


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


if __name__ == '__main__':
    unittest.main()
