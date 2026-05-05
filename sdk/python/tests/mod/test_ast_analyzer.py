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


# -- @up decorator -----------------------------------------------------------


def test_ast_function_up_metadata():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    @dagger.up
    def web(self) -> dagger.Service:
        ...
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_service is True


def test_ast_function_up_default_false():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def regular(self) -> str:
        return "ok"
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.is_service is False


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


def test_ast_kwonly_falsey_defaults():
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, *, enabled: bool = False, retries: int = 0) -> bool:
        return enabled
""")
    fn = metadata.objects["Foo"].functions[0]
    assert len(fn.parameters) == 2
    enabled, retries = fn.parameters
    assert enabled.python_name == "enabled"
    assert enabled.has_default is True
    assert enabled.default_value is False
    assert retries.python_name == "retries"
    assert retries.has_default is True
    assert retries.default_value == 0


# -- Module metadata ---------------------------------------------------------


def test_ast_module_name():
    metadata = _analyze(
        """
import dagger

@dagger.object_type
class Foo:
    pass
""",
        module_name="my-module",
    )
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
        _analyze(
            """
import dagger

@dagger.object_type
class Foo:
    pass
""",
            main="Missing",
        )


def test_ast_syntax_error():
    with pytest.raises(ParseError):
        _analyze("""
import dagger

@dagger.object_type
class Foo
    pass
""")


# -- Regression tests for constructor / inheritance / enum fix commits -------


def test_ast_initvar_not_exposed_as_field():
    """``InitVar[T]`` must be a constructor param, never a field."""
    metadata = _analyze("""
import dagger
from dataclasses import InitVar

@dagger.object_type
class Foo:
    name: str = dagger.field()
    secret: InitVar[str]
""")
    obj = metadata.objects["Foo"]
    field_names = [f.python_name for f in obj.fields]
    param_names = [p.python_name for p in obj.constructor.parameters]
    assert field_names == ["name"]
    assert "secret" in param_names


def test_ast_initvar_dotted_form():
    """``dataclasses.InitVar[T]`` (dotted form) is recognized."""
    metadata = _analyze("""
import dagger
import dataclasses

@dagger.object_type
class Foo:
    name: str = dagger.field()
    token: dataclasses.InitVar[str]
""")
    obj = metadata.objects["Foo"]
    assert [f.python_name for f in obj.fields] == ["name"]
    assert "token" in [p.python_name for p in obj.constructor.parameters]


def test_ast_explicit_init_overrides_autoderived():
    """An explicit ``__init__`` supersedes field-derived constructor params."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    name: str = dagger.field()
    count: int = dagger.field(default=0)

    def __init__(self, name: str, *, flag: bool = False) -> None:
        self.name = name
        self.count = 0
""")
    ctor = metadata.objects["Foo"].constructor
    names = [p.python_name for p in ctor.parameters]
    assert names == ["name", "flag"]
    assert ctor.is_constructor is True


def test_ast_inherited_create_from_base_class():
    """A ``create`` classmethod on a base class is used as the constructor."""
    metadata = _analyze("""
import dagger
from typing import Self

class Base:
    @classmethod
    def create(cls, name: str) -> Self:
        return cls()

@dagger.object_type
class Foo(Base):
    pass
""")
    ctor = metadata.objects["Foo"].constructor
    assert [p.python_name for p in ctor.parameters] == ["name"]
    assert ctor.is_constructor is True


def test_ast_inherited_functions_from_base_class():
    """``@function``-decorated methods on a base class are inherited (issue #13089)."""
    metadata = _analyze("""
import dagger
from typing import Self

class Base:
    @dagger.function
    def with_component(self, name: str) -> Self:
        '''Inherited function that should be visible.'''
        return self

    @dagger.function
    async def with_context(self) -> Self:
        '''Another inherited function that should be visible.'''
        return self

@dagger.object_type
class Foo(Base):
    @dagger.function
    def my_own_function(self) -> str:
        '''This function IS visible.'''
        return "ok"
""")
    funcs = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert set(funcs) == {"my_own_function", "with_component", "with_context"}
    assert funcs["with_component"].doc == ("Inherited function that should be visible.")
    assert funcs["with_context"].doc == (
        "Another inherited function that should be visible."
    )
    assert funcs["with_context"].is_async is True


def test_ast_inherited_function_override_wins():
    """A child's ``@function`` override is preferred over the base's."""
    metadata = _analyze("""
import dagger

class Base:
    @dagger.function
    def greet(self) -> str:
        '''base docstring'''
        return "base"

@dagger.object_type
class Foo(Base):
    @dagger.function
    def greet(self) -> str:
        '''child docstring'''
        return "child"
""")
    funcs = [f for f in metadata.objects["Foo"].functions if f.python_name == "greet"]
    assert len(funcs) == 1
    assert funcs[0].doc == "child docstring"


def test_ast_inherited_functions_multilevel():
    """Functions from grandparent classes are also discovered."""
    metadata = _analyze("""
import dagger

class Grandparent:
    @dagger.function
    def from_grandparent(self) -> str:
        return ""

class Parent(Grandparent):
    @dagger.function
    def from_parent(self) -> str:
        return ""

@dagger.object_type
class Foo(Parent):
    @dagger.function
    def from_child(self) -> str:
        return ""
""")
    names = {f.python_name for f in metadata.objects["Foo"].functions}
    assert names == {"from_child", "from_parent", "from_grandparent"}


def test_ast_external_constructor_simple():
    """``alt = function(Other)`` copies the target's constructor signature."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Other:
    name: str = dagger.field(default="other")

@dagger.object_type
class Foo:
    alt = dagger.function(Other)
""")
    funcs = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert "alt" in funcs
    assert funcs["alt"].resolved_return_type.name == "Other"
    assert [p.python_name for p in funcs["alt"].parameters] == ["name"]


def test_ast_external_constructor_with_kwargs():
    """``alt = function(doc=...)(Other)`` propagates kwargs."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Other:
    name: str = dagger.field(default="other")

@dagger.object_type
class Foo:
    alt = dagger.function(doc="custom doc")(Other)
""")
    funcs = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert "alt" in funcs
    assert funcs["alt"].doc == "custom doc"


def test_ast_external_constructor_forward_reference():
    """``alt = function(Later)`` works when Later is declared afterward."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    alt = dagger.function(Later)

@dagger.object_type
class Later:
    name: str = dagger.field(default="later")
""")
    funcs = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert "alt" in funcs
    assert funcs["alt"].resolved_return_type.name == "Later"


def test_ast_legacy_enum_tuple_value_with_doc():
    """``MEMBER = "value", "doc"`` sets both value and member doc."""
    metadata = _analyze("""
import dagger

class Status(dagger.Enum):
    ACTIVE = "A", "Active description"
    INACTIVE = "I", "Inactive description"

@dagger.object_type
class Foo:
    pass
""")
    members = {m.name: m for m in metadata.enums["Status"].members}
    assert members["ACTIVE"].value == "A"
    assert members["ACTIVE"].doc == "Active description"
    assert members["INACTIVE"].value == "I"


def test_ast_legacy_enum_one_tuple_value():
    """``MEMBER = "value",`` (1-tuple) sets value, no doc."""
    metadata = _analyze("""
import dagger

class Status(dagger.Enum):
    ACTIVE = "A",

@dagger.object_type
class Foo:
    pass
""")
    active = metadata.enums["Status"].members[0]
    assert active.value == "A"
    assert active.doc is None


# -- Error paths covered by resolver raise sites -----------------------------


def test_ast_general_union_is_rejected():
    """``int | str`` is not a supported Dagger type."""
    from dagger.mod._analyzer.errors import TypeResolutionError

    with pytest.raises(TypeResolutionError):
        _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def fn(self, x: int | str) -> str:
        return str(x)
""")


# -- Advanced defaults -------------------------------------------------------


def test_ast_default_factory_list():
    """``default_factory=list`` yields an empty list as the static default."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    tags: list[str] = dagger.field(default_factory=list)
""")
    field = metadata.objects["Foo"].fields[0]
    assert field.python_name == "tags"
    assert field.has_default is True
    assert field.default_value == []


def test_ast_module_level_constant_default():
    """A module-level constant used as a default is resolved to its value."""
    metadata = _analyze("""
import dagger

DEFAULT_NAME = "alice"

@dagger.object_type
class Foo:
    @dagger.function
    def greet(self, name: str = DEFAULT_NAME) -> str:
        return name
""")
    fn = metadata.objects["Foo"].functions[0]
    param = fn.parameters[0]
    assert param.python_name == "name"
    assert param.default_value == "alice"


def test_ast_annassigned_constant_default():
    """Annotated module-level constants also resolve."""
    metadata = _analyze("""
import dagger

DEFAULT_COUNT: int = 5

@dagger.object_type
class Foo:
    @dagger.function
    def count(self, n: int = DEFAULT_COUNT) -> int:
        return n
""")
    fn = metadata.objects["Foo"].functions[0]
    assert fn.parameters[0].default_value == 5


# -- New fixes in this review round ------------------------------------------


def test_ast_positional_only_parameter():
    """Parameters before ``/`` must be extracted, not silently dropped."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, name: str, /, count: int = 0) -> str:
        return f"{name}:{count}"
""")
    fn = metadata.objects["Foo"].functions[0]
    names = [p.python_name for p in fn.parameters]
    assert names == ["name", "count"]


def test_ast_classvar_annotation_is_ignored():
    """``ClassVar[...]`` at class scope is a class constant, not a field."""
    metadata = _analyze("""
import dagger
from typing import ClassVar

@dagger.object_type
class Foo:
    VERSION: ClassVar[int] = 1
    name: str = dagger.field()
""")
    obj = metadata.objects["Foo"]
    assert [f.python_name for f in obj.fields] == ["name"]
    ctor_names = [p.python_name for p in obj.constructor.parameters]
    assert "VERSION" not in ctor_names


def test_ast_final_annotation_is_ignored():
    """``Final[...]`` at class scope is skipped as well."""
    metadata = _analyze("""
import dagger
from typing import Final

@dagger.object_type
class Foo:
    MAX: Final[int] = 99
    name: str = dagger.field()
""")
    obj = metadata.objects["Foo"]
    assert [f.python_name for f in obj.fields] == ["name"]
    assert "MAX" not in [p.python_name for p in obj.constructor.parameters]


def test_ast_intenum_is_detected():
    """``IntEnum`` subclasses are registered alongside plain ``Enum``."""
    metadata = _analyze("""
import dagger
from enum import IntEnum

class Priority(IntEnum):
    LOW = 1
    HIGH = 2

@dagger.object_type
class Foo:
    pass
""")
    assert "Priority" in metadata.enums
    names = {m.name for m in metadata.enums["Priority"].members}
    assert names == {"LOW", "HIGH"}


def test_ast_strenum_is_detected():
    """``StrEnum`` subclasses are registered alongside plain ``Enum``."""
    metadata = _analyze("""
import dagger
from enum import StrEnum

class Color(StrEnum):
    RED = "red"
    BLUE = "blue"

@dagger.object_type
class Foo:
    pass
""")
    assert "Color" in metadata.enums


def test_ast_sequence_resolves_as_list():
    """``Sequence[T]`` resolves to list kind, not a bare object."""
    metadata = _analyze("""
import dagger
from collections.abc import Sequence

@dagger.object_type
class Foo:
    @dagger.function
    def sum(self, xs: Sequence[int]) -> int:
        return sum(xs)
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.kind == "list"
    assert param.resolved_type.element_type.name == "int"


def test_ast_dataclasses_field_is_not_dagger_field():
    """Bare ``field()`` from ``dataclasses`` must not register a Dagger field."""
    metadata = _analyze("""
import dagger
from dataclasses import field

@dagger.object_type
class Foo:
    items: list[str] = field(default_factory=list)
    name: str = dagger.field()
""")
    obj = metadata.objects["Foo"]
    field_names = [f.python_name for f in obj.fields]
    assert field_names == ["name"]


def test_ast_first_method_param_skipped_by_position():
    """The receiver is skipped by position, not by name match."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    def call(this, x: int) -> int:
        return x
""")
    fn = metadata.objects["Foo"].functions[0]
    names = [p.python_name for p in fn.parameters]
    assert names == ["x"]


# -- Module-level type aliases ------------------------------------------------


def test_ast_type_alias_with_annotated_default_path():
    """`Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]` resolves.

    Both the underlying ``Directory`` type and the ``DefaultPath`` metadata
    must be picked up when the alias is referenced as a parameter annotation.
    """
    metadata = _analyze("""
import dagger
from typing import Annotated

Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: Source) -> dagger.Container:
        ...
""")
    fn = metadata.objects["Foo"].functions[0]
    param = fn.parameters[0]
    assert param.python_name == "src"
    assert param.resolved_type.kind == "object"
    assert param.resolved_type.name == "Directory"
    assert param.default_path == "."


def test_ast_type_alias_with_annotated_on_field():
    """The same alias used on a field must unwrap to ``Directory``."""
    metadata = _analyze("""
import dagger
from typing import Annotated

Source = Annotated[dagger.Directory, dagger.DefaultPath(".")]

@dagger.object_type
class Foo:
    src: Source = dagger.field()
""")
    field = metadata.objects["Foo"].fields[0]
    assert field.python_name == "src"
    assert field.resolved_type.kind == "object"
    assert field.resolved_type.name == "Directory"


def test_ast_plain_type_alias():
    """``Src = dagger.Directory`` resolves to Directory."""
    metadata = _analyze("""
import dagger

Src = dagger.Directory

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: Src) -> dagger.Container:
        ...
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.kind == "object"
    assert param.resolved_type.name == "Directory"


def test_ast_optional_type_alias():
    """``MaybeDir = dagger.Directory | None`` resolves to optional Directory."""
    metadata = _analyze("""
import dagger

MaybeDir = dagger.Directory | None

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: MaybeDir = None) -> dagger.Container:
        ...
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.name == "Directory"
    assert param.resolved_type.is_optional is True


def test_ast_chained_type_alias():
    """An alias pointing at another alias resolves through both."""
    metadata = _analyze("""
import dagger

A = dagger.Directory
B = A

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: B) -> dagger.Container:
        ...
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    assert param.resolved_type.name == "Directory"


def test_ast_cyclic_type_alias_does_not_recurse():
    """A self-referential alias must not loop; falls back to the warn path."""
    # ``A = B; B = A`` cannot be resolved to a real type. The analyzer should
    # not recurse forever — it falls back to the existing unresolved-name
    # path (warn + assume object), same behavior as a missing import.
    metadata = _analyze("""
import dagger

A = B
B = A

@dagger.object_type
class Foo:
    @dagger.function
    def build(self, src: A) -> dagger.Container:
        ...
""")
    param = metadata.objects["Foo"].functions[0].parameters[0]
    # Expansion stopped at the cycle; the resolver fell back to assuming
    # an object type with the alias name.
    assert param.resolved_type.kind == "object"


# -- Relative imports --------------------------------------------------------


def test_ast_relative_import_module():
    """``from . import sibling`` must not crash the analyzer."""
    metadata = _analyze("""
import dagger
from . import render

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects
    assert [f.python_name for f in metadata.objects["Foo"].functions] == ["hello"]


def test_ast_relative_import_from_submodule():
    """``from .submodule import Name`` must not crash the analyzer."""
    metadata = _analyze("""
import dagger
from .helpers import Helper

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects


def test_ast_relative_import_does_not_resolve_top_level_module():
    """``from .json import X`` must NOT pick up the stdlib ``json`` module.

    A relative import is unambiguously package-internal; resolving it
    against ``sys.path`` would silently bind the wrong object and break
    type analysis in surprising ways.
    """
    metadata = _analyze("""
import dagger
from .json import dumps

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects


def test_ast_relative_import_parent_package():
    """``from ..pkg import Name`` (multi-dot) must not crash the analyzer."""
    metadata = _analyze("""
import dagger
from ..siblings import Helper
from ...root import Other

@dagger.object_type
class Foo:
    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects


def test_ast_relative_import_resolves_decorated_class(tmp_path):
    """A decorated class imported relatively is still discovered cross-file."""
    pkg = tmp_path / "relpkg"
    pkg.mkdir()
    (pkg / "__init__.py").write_text("", encoding="utf-8")
    (pkg / "helpers.py").write_text(
        "import dagger\n"
        "\n"
        "@dagger.object_type\n"
        "class Helper:\n"
        '    name: str = dagger.field(default="h")\n',
        encoding="utf-8",
    )
    (pkg / "main.py").write_text(
        "import dagger\n"
        "from .helpers import Helper\n"
        "\n"
        "@dagger.object_type\n"
        "class Foo:\n"
        "    @dagger.function\n"
        "    def get_helper(self) -> Helper:\n"
        "        return Helper()\n",
        encoding="utf-8",
    )

    metadata = analyze_module(
        source_files=[pkg / "__init__.py", pkg / "helpers.py", pkg / "main.py"],
        main_object_name="Foo",
    )
    assert {"Foo", "Helper"} <= set(metadata.objects)
    fn = metadata.objects["Foo"].functions[0]
    assert fn.python_name == "get_helper"
    assert fn.resolved_return_type.name == "Helper"


# -- Aliased dagger imports --------------------------------------------------


def test_ast_aliased_dagger_module_decorators():
    """``import dagger as d`` then ``@d.object_type`` / ``@d.function``."""
    metadata = _analyze("""
import dagger as d

@d.object_type
class Foo:
    @d.function
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects
    assert [f.python_name for f in metadata.objects["Foo"].functions] == ["hello"]


def test_ast_aliased_decorator_from_import():
    """``from dagger import object_type as ot, function as fn`` is recognised."""
    metadata = _analyze("""
from dagger import object_type as ot, function as fn

@ot
class Foo:
    @fn
    def hello(self) -> str:
        return "hi"
""")
    assert "Foo" in metadata.objects
    assert [f.python_name for f in metadata.objects["Foo"].functions] == ["hello"]


def test_ast_aliased_field_call():
    """``from dagger import field as fld`` then ``x: T = fld(...)`` is a field."""
    metadata = _analyze("""
import dagger
from dagger import field as fld

@dagger.object_type
class Foo:
    name: str = fld(default="x")

    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    obj = metadata.objects["Foo"]
    assert [(f.python_name, f.default_value) for f in obj.fields] == [("name", "x")]


def test_ast_aliased_dagger_keeps_dataclasses_field_rejected():
    """``from dataclasses import field`` is still excluded from dagger fields."""
    metadata = _analyze("""
import dagger
from dataclasses import field

@dagger.object_type
class Foo:
    name: str = field(default="x")

    @dagger.function
    def hello(self) -> str:
        return "hi"
""")
    # dataclasses.field is not a dagger field; the assignment becomes a
    # constructor parameter via the AnnAssign-as-param path but no
    # FieldMetadata is created.
    assert metadata.objects["Foo"].fields == []


def test_ast_aliased_decorator_keeps_check_generate_up():
    """Check/generate/up decorators also resolve via the alias map."""
    metadata = _analyze("""
import dagger as d

@d.object_type
class Foo:
    @d.function
    @d.check
    def smoke(self) -> str: ...

    @d.function
    @d.generate
    def gen(self) -> str: ...

    @d.function
    @d.up
    def serve(self) -> str: ...
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}
    assert fns["smoke"].is_check
    assert fns["gen"].is_generate
    assert fns["serve"].is_service


# -- @staticmethod parameter handling ---------------------------------------


def test_ast_staticmethod_first_param_preserved():
    """``@staticmethod`` has no implicit receiver — first param is real."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @staticmethod
    @dagger.function
    def echo(x: str) -> str:
        return x

    @dagger.function
    def hello(self, y: int) -> str:
        return str(y)
""")
    fns = {f.python_name: f for f in metadata.objects["Foo"].functions}
    # static: ``x`` survives; instance method: ``self`` is skipped, ``y`` kept.
    assert [p.python_name for p in fns["echo"].parameters] == ["x"]
    assert [p.python_name for p in fns["hello"].parameters] == ["y"]


def test_ast_staticmethod_decorator_order_swapped():
    """``@dagger.function`` over ``@staticmethod`` order also works."""
    metadata = _analyze("""
import dagger

@dagger.object_type
class Foo:
    @dagger.function
    @staticmethod
    def echo(x: str) -> str:
        return x
""")
    fn = metadata.objects["Foo"].functions[0]
    assert [p.python_name for p in fn.parameters] == ["x"]
