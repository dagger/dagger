"""Tests for the AST-based module analyzer.

These are the AST-path equivalents of the runtime tests in test_registration.py
and test_future_annotations.py, verifying that analyze_source_string() extracts
the same information the runtime Module() introspection does.
"""

import pytest

from dagger.mod._analyzer.analyze import analyze_module, analyze_source_string
from dagger.mod._analyzer.errors import AnalysisError, ParseError, ValidationError


def _analyze(source: str, main: str = "Foo", **kwargs):
    """Shorthand used throughout this file."""
    return analyze_source_string(source, main, **kwargs)


# -- Mirrors test_registration.py: test_object_type_resolvers ----------------


def test_ast_object_type_resolvers():
    """AST equivalent of test_object_type_resolvers."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    private_field: str
    exposed_field: str = dagger.field()

    def private_method(self) -> str: ...
    @dagger.function
    def exposed_method(self) -> str: ...
""")

    obj = metadata.objects["Foo"]
    fields = [f.python_name for f in obj.fields]
    functions = [f.python_name for f in obj.functions]
    assert fields + functions == ["exposed_field", "exposed_method"]


# -- Mirrors test_registration.py: test_func_doc ----------------------------


def test_ast_func_doc():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn_with_doc(self):
        \"\"\"Foo.\"\"\"
""")
    assert metadata.objects["Foo"].functions[0].doc == "Foo."


# -- Mirrors test_registration.py: test_function_deprecated_metadata ---------


def test_ast_function_deprecated_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function(deprecated="Use new method instead")
    def legacy(self):
        \"\"\"Legacy function.\"\"\"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.deprecated == "Use new method instead"


# -- Mirrors test_registration.py: test_function_check_metadata --------------


def test_ast_function_check_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    @dagger.check
    def lint(self):
        \"\"\"Check function.\"\"\"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_check is True


def test_ast_function_check_default_false():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def regular(self):
        \"\"\"Regular function.\"\"\"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_check is False


def test_ast_check_decorator_order():
    """@check works whether applied before or after @function."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.check
    @dagger.function
    def check_first(self):
        \"\"\"Check applied before function.\"\"\"

    @dagger.function
    @dagger.check
    def function_first(self):
        \"\"\"Check applied after function.\"\"\"
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert fns["check_first"].is_check is True
    assert fns["function_first"].is_check is True


# -- Mirrors test_registration.py: test_field_deprecated_metadata ------------


def test_ast_field_deprecated_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    legacy: str = dagger.field(default="", deprecated="Use new field instead")
""")
    field = metadata.objects["Foo"].fields[0]
    assert field.deprecated == "Use new field instead"


# -- Mirrors test_registration.py: test_object_type_deprecated_metadata ------


def test_ast_object_type_deprecated_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type(deprecated="Use NewFoo instead")
class Foo:
    pass
""")
    assert metadata.objects["Foo"].deprecated == "Use NewFoo instead"


# -- Mirrors test_registration.py: test_void_return_type ---------------------


def test_ast_void_return_type():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def void(self): ...
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.resolved_return_type.kind == "void"
    assert fn.resolved_return_type.is_optional is True


# -- Mirrors test_registration.py: test_self_return_type ---------------------


def test_ast_self_return_type():
    metadata = _analyze("""
import dagger
from typing_extensions import Self

@dagger.object_type
class Foo:
    @dagger.function
    def iden(self) -> Self:
        return self

    @dagger.function
    def seq(self) -> list[Self]:
        return [self]
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}

    iden_ret = fns["iden"].resolved_return_type
    assert iden_ret.kind == "object"
    assert iden_ret.name == "Foo"
    assert iden_ret.is_self is True

    seq_ret = fns["seq"].resolved_return_type
    assert seq_ret.kind == "list"
    assert seq_ret.element_type.kind == "object"
    assert seq_ret.element_type.name == "Foo"
    assert seq_ret.element_type.is_self is True


# -- Mirrors test_forward_reference.py --------------------------------------


def test_ast_forward_reference():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def method1(self) -> "Foo": ...

    @dagger.function
    def get_bar(self) -> "Bar": ...

@dagger.object_type
class Bar:
    pass
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert fns["method1"].resolved_return_type.name == "Foo"
    assert fns["get_bar"].resolved_return_type.name == "Bar"


# -- Mirrors test_future_annotations.py -------------------------------------


def test_ast_default_path_with_future_annotations():
    metadata = _analyze("""
from __future__ import annotations
from typing import Annotated
import dagger
from dagger import DefaultPath

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[dagger.Directory, DefaultPath(".")],
    ) -> str:
        return "ok"
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.default_path == "."


def test_ast_doc_with_future_annotations():
    metadata = _analyze("""
from __future__ import annotations
from typing import Annotated
from typing_extensions import Doc
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[str, Doc("Source directory")],
    ) -> str:
        return "ok"
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.doc == "Source directory"


def test_ast_name_with_future_annotations():
    metadata = _analyze("""
from __future__ import annotations
from typing import Annotated
import dagger
from dagger import Name

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[str, Name("source")],
    ) -> str:
        return "ok"
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.api_name == "source"


def test_ast_ignore_with_future_annotations():
    metadata = _analyze("""
from __future__ import annotations
from typing import Annotated
import dagger
from dagger import Ignore

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[dagger.Directory, Ignore(["*.tmp", ".git"])],
    ) -> str:
        return "ok"
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.ignore == ["*.tmp", ".git"]


def test_ast_deprecated_with_future_annotations():
    metadata = _analyze("""
from __future__ import annotations
from typing import Annotated
import dagger
from dagger import Deprecated

@dagger.object_type
class Foo:
    @dagger.function
    def build(
        self,
        src: Annotated[str, Deprecated("Use new_src instead")] = "",
    ) -> str:
        return "ok"
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.deprecated == "Use new_src instead"


# -- Mirrors test_interfaces.py: test_registered_functions -------------------


def test_ast_interface_registered_functions():
    metadata = _analyze("""
import dagger
from typing_extensions import Self

@dagger.interface
class Duck:
    @dagger.function
    def quack(self) -> str: ...

    @dagger.function
    def get_self(self) -> Self: ...

    @dagger.function
    def get_mob(self) -> list[Self]: ...

    def get_private(self) -> bool: ...

@dagger.object_type
class Foo:
    pass
""")
    duck = metadata.objects["Duck"]
    assert duck.is_interface is True
    func_names = [f.python_name for f in duck.functions]
    assert "quack" in func_names
    assert "get_self" in func_names
    assert "get_mob" in func_names
    assert "get_private" not in func_names
    assert duck.constructor is None


# -- Mirrors test_enum_docstrings.py -----------------------------------------


def test_ast_enum_member_doc():
    metadata = _analyze("""
import enum
import dagger

@dagger.enum_type
class ExampleEnum(enum.Enum):
    FIRST = "first"
    \"\"\"This is the first option\"\"\"

    SECOND = "second"
    \"\"\"This is the second option\"\"\"

    THIRD = "third"

@dagger.object_type
class Foo:
    pass
""")
    members = {m.name: m for m in metadata.enums["ExampleEnum"].members}
    assert members["FIRST"].doc == "This is the first option"
    assert members["SECOND"].doc == "This is the second option"
    assert members["THIRD"].doc is None


def test_ast_enum_no_docs():
    metadata = _analyze("""
import enum
import dagger

@dagger.enum_type
class EmptyEnum(enum.Enum):
    VALUE = "value"

@dagger.object_type
class Foo:
    pass
""")
    assert metadata.enums["EmptyEnum"].members[0].doc is None


# -- @generate decorator (no runtime equivalent yet) -------------------------


def test_ast_function_generate_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    @dagger.generate
    def codegen(self) -> dagger.Directory:
        ...
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_generate is True


def test_ast_function_generate_default_false():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def regular(self) -> str:
        return "ok"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_generate is False


# -- Type resolution ---------------------------------------------------------


@pytest.mark.parametrize(
    ("annotation", "expected_kind", "expected_name"),
    [
        ("str", "primitive", "str"),
        ("int", "primitive", "int"),
        ("float", "primitive", "float"),
        ("bool", "primitive", "bool"),
    ],
)
def test_ast_primitive_type_resolution(annotation, expected_kind, expected_name):
    metadata = _analyze(f"""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn(self, x: {annotation}) -> str:
        return ""
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.kind == expected_kind
    assert param.resolved_type.name == expected_name


def test_ast_optional_type():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn(self, name: str | None = None) -> str:
        return name or ""
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.kind == "primitive"
    assert param.resolved_type.name == "str"
    assert param.resolved_type.is_optional is True
    assert param.is_nullable is True


def test_ast_list_type():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn(self, items: list[str]) -> list[int]:
        return [len(s) for s in items]
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.parameters[0].resolved_type.kind == "list"
    assert fn.parameters[0].resolved_type.element_type.name == "str"
    assert fn.resolved_return_type.kind == "list"
    assert fn.resolved_return_type.element_type.name == "int"


@pytest.mark.parametrize(
    ("type_name", "expected_kind"),
    [
        ("dagger.Container", "object"),
        ("dagger.Directory", "object"),
        ("dagger.File", "object"),
        ("dagger.Secret", "object"),
        ("dagger.Service", "object"),
        ("dagger.Platform", "scalar"),
    ],
)
def test_ast_dagger_type_resolution(type_name, expected_kind):
    metadata = _analyze(f"""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn(self) -> {type_name}:
        ...
""")
    ret = metadata.objects["Foo"].functions[0].resolved_return_type
    assert ret.kind == expected_kind


def test_ast_enum_type_resolution():
    metadata = _analyze("""
import enum
import dagger

@dagger.enum_type
class Color(enum.Enum):
    RED = "RED"

@dagger.object_type
class Foo:
    @dagger.function
    def pick(self, color: Color) -> str:
        return color.value
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.kind == "enum"
    assert param.resolved_type.name == "Color"


def test_ast_interface_type_resolution():
    metadata = _analyze("""
import dagger

@dagger.interface
class Buildable:
    @dagger.function
    def build(self) -> str: ...

@dagger.object_type
class Foo:
    @dagger.function
    def get_buildable(self) -> Buildable:
        ...
""")
    ret = metadata.objects["Foo"].functions[0].resolved_return_type
    assert ret.kind == "interface"
    assert ret.name == "Buildable"


# -- Constructor handling ----------------------------------------------------


def test_ast_default_constructor():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field()
    count: int = dagger.field(default=0)
""")
    ctor = metadata.objects["Foo"].constructor
    assert ctor is not None
    assert ctor.is_constructor is True
    assert len(ctor.parameters) == 2
    assert ctor.parameters[0].python_name == "name"
    assert ctor.parameters[0].has_default is False
    assert ctor.parameters[1].python_name == "count"
    assert ctor.parameters[1].has_default is True


def test_ast_field_init_false_excluded_from_constructor():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field()
    internal: str = dagger.field(init=False)
""")
    ctor = metadata.objects["Foo"].constructor
    assert len(ctor.parameters) == 1
    assert ctor.parameters[0].python_name == "name"


def test_ast_alt_constructor():
    metadata = _analyze("""
import dagger
from typing_extensions import Self

@dagger.object_type
class Foo:
    @classmethod
    def create(cls, bar: str = "bar") -> Self:
        \"\"\"Constructor doc.\"\"\"
        return cls()
""")
    ctor = metadata.objects["Foo"].constructor
    assert ctor is not None
    assert ctor.is_constructor is True
    assert ctor.is_classmethod is True
    assert ctor.doc == "Constructor doc."
    assert len(ctor.parameters) == 1
    assert ctor.parameters[0].python_name == "bar"
    assert ctor.parameters[0].default_value == "bar"


def test_ast_constructor_doc():
    """Constructor inherits class docstring when auto-generated."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    \"\"\"Object doc.\"\"\"
""")
    assert metadata.objects["Foo"].constructor.doc == "Object doc."


# -- Decorator naming conventions --------------------------------------------


@pytest.mark.parametrize(
    "source",
    [
        # @dagger.* prefix
        """
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field()

    @dagger.function
    def hello(self) -> str: ...
""",
        # bare imports
        """
from dagger import object_type, function, field

@object_type
class Foo:
    name: str = field()

    @function
    def hello(self) -> str: ...
""",
        # @mod.* prefix
        """
import dagger
from dagger.mod import Module

mod = Module()

@mod.object_type
class Foo:
    name: str = mod.field()

    @mod.function
    def hello(self) -> str: ...
""",
    ],
    ids=["dagger_prefix", "bare_import", "mod_prefix"],
)
def test_ast_decorator_naming(source):
    metadata = _analyze(source)
    obj = metadata.objects["Foo"]
    assert len(obj.fields) == 1
    assert len(obj.functions) == 1


# -- Name normalization (mirrors test_utils.py: test_normalize_name) ---------


@pytest.mark.parametrize(
    ("python_name", "expected_api_name"),
    [
        ("with_", "with"),
        ("from_", "from"),
    ],
)
def test_ast_trailing_underscore_normalization(python_name, expected_api_name):
    metadata = _analyze(f"""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def {python_name}(self) -> str: ...
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.python_name == python_name
    assert fn.api_name == expected_api_name


# -- Cache policy ------------------------------------------------------------


def test_ast_cache_policy():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function(cache="never")
    def no_cache(self) -> str: ...

    @dagger.function(cache="session")
    def session_cache(self) -> str: ...

    @dagger.function(cache="30s")
    def ttl_cache(self) -> str: ...
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert fns["no_cache"].cache_policy == "never"
    assert fns["session_cache"].cache_policy == "session"
    assert fns["ttl_cache"].cache_policy == "30s"


# -- Async functions ---------------------------------------------------------


def test_ast_async_function():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    async def bar(self) -> str:
        return "bar"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_async is True


# -- Keyword-only arguments --------------------------------------------------


def test_ast_kwonly_parameters():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, *, name: str, count: int = 1) -> str:
        return name * count
""")
    fn = metadata.objects["Foo"].functions[0]
    assert len(fn.parameters) == 2
    assert fn.parameters[0].python_name == "name"
    assert fn.parameters[1].python_name == "count"
    assert fn.parameters[1].has_default is True
    assert fn.parameters[1].default_value == 1


# -- Module metadata ---------------------------------------------------------


def test_ast_module_name():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    pass
""", module_name="my-module")
    assert metadata.module_name == "my-module"
    assert metadata.main_object == "Foo"


def test_ast_module_name_defaults_to_main_object():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    pass
""")
    assert metadata.module_name == "Foo"


def test_ast_metadata_json_roundtrip():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field()

    @dagger.function
    def hello(self, greeting: str = "hi") -> str:
        return greeting
""")
    restored = metadata.from_json(metadata.to_json())

    assert restored.module_name == metadata.module_name
    assert restored.main_object == metadata.main_object
    assert "Foo" in restored.objects
    assert len(restored.objects["Foo"].fields) == 1
    assert len(restored.objects["Foo"].functions) == 1


# -- Error handling ----------------------------------------------------------


def test_ast_empty_source_files():
    with pytest.raises(AnalysisError, match="No source files provided"):
        analyze_module([], "Foo")


def test_ast_main_object_not_found():
    with pytest.raises(ValidationError, match="Main object 'Missing' not found"):
        _analyze("""
import dagger

@dagger.object_type
class Foo:
    pass
""", main="Missing")


def test_ast_syntax_error():
    with pytest.raises(ParseError):
        _analyze("""
import dagger

@dagger.object_type
class Foo
    pass
""")
