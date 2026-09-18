import pytest

import dagger
from dagger.mod import Module


@pytest.mark.parametrize("collection_first", [True, False])
@pytest.mark.parametrize("get_first", [True, False])
def test_collection_markers_preserve_fields_and_functions(collection_first, get_first):
    mod = Module()

    @mod.object_type
    class Item:
        name: str = mod.field()

    class Items:
        names: list[str] = mod.keys(default=list, name="paths")
        selection: dagger.CollectionDelta | None = mod.delta()

        def lookup(self, path: str) -> Item:
            return Item(name=path)

        lookup = (
            mod.get(mod.function(lookup))
            if get_first
            else mod.function(mod.get(lookup))
        )

    items_type = (
        mod.collection(mod.object_type(Items))
        if collection_first
        else mod.object_type(mod.collection(Items))
    )
    obj = mod.get_object("Items")
    assert obj.cls.__dagger_collection__
    assert obj.fields["paths"].original_name == "names"
    assert obj.fields["paths"].meta.collection_role == "keys"
    assert obj.fields["selection"].meta.collection_role == "delta"
    assert obj.functions["lookup"].wrapped.__dagger_get__
    assert items_type().names == []
    assert items_type().selection is None
