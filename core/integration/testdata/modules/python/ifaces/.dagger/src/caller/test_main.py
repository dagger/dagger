# ruff: noqa: PLR2004

import pytest
from opentelemetry import trace

import dagger
from dagger import dag

pytestmark = [
    pytest.mark.anyio,
]

tracer = trace.get_tracer(__name__)


@pytest.fixture(scope="session")
def anyio_backend():
    return "asyncio"


@pytest.fixture(scope="session", autouse=True)
async def connect():
    async with await dagger.connect():
        yield


@pytest.fixture(autouse=True)
async def span(request: pytest.FixtureRequest):
    if request.scope == "function":
        with tracer.start_as_current_span(request.function.__name__):
            yield


@pytest.fixture
def dirs():
    return [
        dag.directory().with_new_file("/file1", "file1"),
        dag.directory().with_new_file("/file2", "file2"),
    ]


@pytest.fixture
def impl(dirs):
    return dag.impl(
        str_list=["a", "b"],
        int_list=[1, 2],
        bool_list=[True, False],
        obj_list=dirs,
    )


@pytest.fixture
def iface(impl: dagger.Impl):
    return impl.as_test_custom_iface()


async def test_void(iface: dagger.TestCustomIface):
    await dag.test().void(iface)


async def test_str(iface: dagger.TestCustomIface):
    assert await dag.test().str_(iface) == "a"


async def test_with_str(iface: dagger.TestCustomIface):
    assert await dag.test().with_str(iface, "c").str_() == "c"


async def test_with_optional_str(iface: dagger.TestCustomIface):
    assert await dag.test().with_optional_str(iface, str_arg="d").str_() == "d"
    assert await dag.test().with_optional_str(iface).str_() == "a"


async def test_str_list(iface: dagger.TestCustomIface):
    assert await dag.test().str_list(iface) == ["a", "b"]


async def test_with_str_list(iface: dagger.TestCustomIface):
    assert await dag.test().with_str_list(iface, ["c", "d"]).str_list() == ["c", "d"]


async def test_int(iface: dagger.TestCustomIface):
    assert await dag.test().int_(iface) == 1


async def test_with_int(iface: dagger.TestCustomIface):
    assert await dag.test().with_int(iface, 3).int_() == 3


async def test_int_list(iface: dagger.TestCustomIface):
    assert await dag.test().int_list(iface) == [1, 2]


async def test_with_int_list(iface: dagger.TestCustomIface):
    assert await dag.test().with_int_list(iface, [3, 4]).int_list() == [3, 4]


async def test_bool(iface: dagger.TestCustomIface):
    assert await dag.test().bool_(iface) is True


async def test_with_bool(iface: dagger.TestCustomIface):
    assert await dag.test().with_bool(iface, False).bool_() is False


async def test_bool_list(iface: dagger.TestCustomIface):
    assert await dag.test().bool_list(iface) == [True, False]


async def test_with_bool_list(iface: dagger.TestCustomIface):
    assert await dag.test().with_bool_list(iface, [False, True]).bool_list() == [
        False,
        True,
    ]


async def test_with_many(iface: dagger.TestCustomIface):
    obj = dag.test().with_str(iface, "c").with_int(3).with_bool(True)

    assert await obj.str_() == "c"
    assert await obj.int_() == 3
    assert await obj.bool_() is True


async def test_obj(iface: dagger.TestCustomIface):
    assert await dag.test().obj(iface).entries() == ["file1"]


async def test_with_obj(iface: dagger.TestCustomIface, dirs: list[dagger.Directory]):
    assert await dag.test().with_obj(iface, dirs[1]).obj().entries() == ["file2"]


async def test_obj_list(iface: dagger.TestCustomIface):
    dirs = await dag.test().obj_list(iface)

    assert len(dirs) == 2
    assert await dirs[0].entries() == ["file1"]
    assert await dirs[1].entries() == ["file2"]


async def test_with_obj_list(iface: dagger.TestCustomIface):
    dirs = await (
        dag.test()
        .with_obj_list(
            iface,
            [
                dag.directory().with_new_file("/file3", "file3"),
                dag.directory().with_new_file("/file4", "file4"),
            ],
        )
        .obj_list()
    )

    assert len(dirs) == 2
    assert await dirs[0].entries() == ["file3"]
    assert await dirs[1].entries() == ["file4"]


async def test_self_iface(iface: dagger.TestCustomIface):
    assert await dag.test().self_iface(iface).str_() == "aself"


async def test_self_iface_list(iface: dagger.TestCustomIface):
    ifaces = await dag.test().self_iface_list(iface)

    assert len(ifaces) == 2
    assert await ifaces[0].str_() == "aself1"
    assert await ifaces[1].str_() == "aself2"


async def test_other_iface(iface: dagger.TestCustomIface):
    assert await dag.test().other_iface(iface).foo() == "aother"


async def test_static_other_iface_list(iface: dagger.TestCustomIface):
    ifaces = await dag.test().static_other_iface_list(iface)

    assert len(ifaces) == 2
    assert await ifaces[0].foo() == "aother1"
    assert await ifaces[1].foo() == "aother2"


async def test_dynamic_other_iface_list(impl: dagger.Impl):
    ifaces = await dag.test().dynamic_other_iface_list(impl.as_test_custom_iface())

    assert len(ifaces) == 0

    ifaces = await dag.test().dynamic_other_iface_list(
        dag.test().with_other_iface(
            dag.test().with_other_iface(
                impl.as_test_custom_iface(),
                impl.with_str("arg1").other_iface().as_test_other_iface(),
            ),
            impl.with_str("arg2").other_iface().as_test_other_iface(),
        )
    )

    assert len(ifaces) == 2
    assert await ifaces[0].foo() == "arg1other"
    assert await ifaces[1].foo() == "arg2other"


async def test_dynamic_other_iface_by_iface_list(impl: dagger.Impl):
    ifaces = await dag.test().dynamic_other_iface_by_iface_list(
        impl.as_test_custom_iface()
    )

    assert len(ifaces) == 0

    ifaces = await dag.test().dynamic_other_iface_by_iface_list(
        dag.test().with_other_iface_by_iface(
            dag.test().with_other_iface_by_iface(
                impl.as_test_custom_iface(),
                impl.with_str("arg1").other_iface().as_test_other_iface(),
            ),
            impl.with_str("arg2").other_iface().as_test_other_iface(),
        )
    )

    assert len(ifaces) == 2
    assert await ifaces[0].foo() == "arg1other"
    assert await ifaces[1].foo() == "arg2other"


async def test_iface_list_args(impl: dagger.Impl):
    strs = await dag.test().iface_list_args(
        [
            impl.as_test_custom_iface(),
            impl.self_iface().as_test_custom_iface(),
        ],
        [
            impl.other_iface().as_test_other_iface(),
            impl.self_iface().other_iface().as_test_other_iface(),
        ],
    )

    assert strs == ["a", "aself", "aother", "aselfother"]


async def test_parent_iface_fields_basic(impl: dagger.Impl):
    strs = await (
        dag.test()
        .with_iface(impl.as_test_custom_iface())
        .with_private_iface(
            dag.impl(
                str_list=["private"],
                int_list=[99],
                bool_list=[False],
                obj_list=[dag.directory()],
            ).as_test_custom_iface()
        )
        .with_iface_list(
            [
                impl.as_test_custom_iface(),
                impl.self_iface().as_test_custom_iface(),
            ]
        )
        .with_other_iface_list(
            [
                impl.other_iface().as_test_other_iface(),
                impl.self_iface().other_iface().as_test_other_iface(),
            ]
        )
        .parent_iface_fields()
    )
    assert strs == ["a", "private", "a", "aself", "aother", "aselfother"]


async def test_parent_iface_fields_optionals(iface: dagger.TestCustomIface):
    strs = await (
        dag.test()
        .with_optional_iface()
        .with_optional_iface(iface=iface)
        .with_optional_iface()
        .parent_iface_fields()
    )
    assert strs == ["a"]


async def test_parent_iface_fields_return_custom_obj(impl: dagger.Impl):
    custom_obj = dag.test().return_custom_obj(
        [
            impl.as_test_custom_iface(),
            impl.self_iface().as_test_custom_iface(),
        ],
        [
            impl.other_iface().as_test_other_iface(),
            impl.self_iface().other_iface().as_test_other_iface(),
        ],
    )

    assert await custom_obj.iface().str_() == "a"

    ifaces = await custom_obj.iface_list()

    assert len(ifaces) == 2
    assert await ifaces[0].str_() == "a"
    assert await ifaces[1].str_() == "aself"
