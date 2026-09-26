from copy import copy, deepcopy
from dataclasses import replace

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


@pytest.mark.parametrize("clone", [copy, deepcopy, replace])
@pytest.mark.parametrize("custom_init", [False, True])
def test_base_survives_copies_without_delta(clone, custom_init):
    mod = Module()

    @mod.collection
    @mod.object_type
    class Items:
        names: list[str] = mod.keys(default=list)

        if custom_init:

            def __init__(self, names: list[str]):
                self.names = names

    original = mod._converter.structure(
        {"names": ["a", "b"], "__daggerCollectionBase": "original"}, Items
    )
    changed = clone(original)
    changed.names = ["b", "c"]
    assert mod._converter.unstructure(changed) == {
        "names": ["b", "c"],
        "__daggerCollectionBase": "original",
    }
    assert mod._converter.unstructure(Items(names=["c"])) == {"names": ["c"]}
    assert "_dagger_collection_base" not in mod.get_object("Items").fields
    assert (
        "_dagger_collection_base"
        not in mod.get_object("Items").get_constructor().parameters
    )
