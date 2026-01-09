"""Tests for AST-based module analysis."""

import textwrap
from pathlib import Path

import pytest

from dagger.mod._analyzer import analyze_source
from dagger.mod._ast_visitor import TypeAnnotationParser
from dagger.mod._exceptions import ModuleLoadError
from dagger.mod._ir import (
    SourceLocation,
    TypeAnnotation,
)

# =============================================================================
# TypeAnnotation Tests
# =============================================================================


class TestTypeAnnotation:
    """Tests for TypeAnnotation IR dataclass."""

    def test_basic_annotation(self):
        """Test creating a basic type annotation."""
        annotation = TypeAnnotation(raw="str")
        assert annotation.raw == "str"
        assert not annotation.is_optional
        assert not annotation.is_list
        assert annotation.element_type is None

    def test_optional_annotation(self):
        """Test optional type annotation."""
        annotation = TypeAnnotation(raw="str | None", is_optional=True)
        assert annotation.is_optional

    def test_list_annotation(self):
        """Test list type annotation."""
        annotation = TypeAnnotation(
            raw="list[str]",
            is_list=True,
            element_type="str",
        )
        assert annotation.is_list
        assert annotation.element_type == "str"

    def test_annotation_with_metadata(self):
        """Test annotation with Annotated metadata."""
        annotation = TypeAnnotation(
            raw='Annotated[str, Doc("A string")]',
            doc="A string",
            name="customName",
            deprecated="Use other field",
        )
        assert annotation.doc == "A string"
        assert annotation.name == "customName"
        assert annotation.deprecated == "Use other field"


# =============================================================================
# TypeAnnotationParser Tests
# =============================================================================


class TestTypeAnnotationParser:
    """Tests for TypeAnnotationParser AST parsing."""

    @pytest.fixture
    def parser(self, tmp_path: Path) -> TypeAnnotationParser:
        return TypeAnnotationParser(tmp_path / "test.py")

    @pytest.fixture
    def location(self, tmp_path: Path) -> SourceLocation:
        return SourceLocation(file=tmp_path / "test.py", line=1, column=0)

    def _parse_annotation(
        self,
        parser: TypeAnnotationParser,
        annotation_str: str,
        location: SourceLocation,
    ) -> TypeAnnotation:
        """Helper to parse an annotation string."""
        import ast

        node = ast.parse(annotation_str, mode="eval").body
        return parser.parse(node, location)

    def test_parse_simple_types(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing simple built-in types."""
        for type_str in ["str", "int", "float", "bool"]:
            annotation = self._parse_annotation(parser, type_str, location)
            assert annotation.raw == type_str
            assert not annotation.is_optional
            assert not annotation.is_list

    def test_parse_optional_union_syntax(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Optional using union syntax."""
        annotation = self._parse_annotation(parser, "str | None", location)
        assert annotation.is_optional

    def test_parse_optional_type(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Optional[T] type."""
        annotation = self._parse_annotation(parser, "Optional[str]", location)
        assert annotation.is_optional

    def test_parse_list_type(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing list[T] type."""
        annotation = self._parse_annotation(parser, "list[str]", location)
        assert annotation.is_list
        assert annotation.element_type == "str"

    def test_parse_annotated_with_doc(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Annotated with Doc."""
        annotation = self._parse_annotation(
            parser,
            'Annotated[str, Doc("A description")]',
            location,
        )
        assert annotation.doc == "A description"

    def test_parse_annotated_with_name(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Annotated with Name."""
        annotation = self._parse_annotation(
            parser,
            'Annotated[str, Name("customName")]',
            location,
        )
        assert annotation.name == "customName"

    def test_parse_annotated_with_default_path(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Annotated with DefaultPath."""
        annotation = self._parse_annotation(
            parser,
            'Annotated[Directory, DefaultPath("./src")]',
            location,
        )
        assert annotation.default_path == "./src"

    def test_parse_annotated_with_ignore(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Annotated with Ignore."""
        annotation = self._parse_annotation(
            parser,
            'Annotated[Directory, Ignore([".git", "node_modules"])]',
            location,
        )
        assert annotation.ignore == (".git", "node_modules")

    def test_parse_annotated_with_deprecated(
        self, parser: TypeAnnotationParser, location: SourceLocation
    ):
        """Test parsing Annotated with Deprecated."""
        annotation = self._parse_annotation(
            parser,
            'Annotated[str, Deprecated("Use other param")]',
            location,
        )
        assert annotation.deprecated == "Use other param"


# =============================================================================
# Module Analysis Tests
# =============================================================================


class TestAnalyzeSource:
    """Tests for analyze_source function."""

    def test_simple_object_type(self):
        """Test analyzing a simple object type."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class Main:
                """Main module description."""
                pass
        ''')

        module_ir = analyze_source(source, "test.py")

        assert module_ir.main_object_name == "Main"
        assert len(module_ir.objects) == 1

        obj = module_ir.objects[0]
        assert obj.name == "Main"
        assert obj.doc == "Main module description."
        assert not obj.is_interface
        assert not obj.is_enum

    def test_object_with_function(self):
        """Test analyzing object type with a function."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def hello(self, name: str) -> str:
                    """Say hello."""
                    return f"Hello, {name}!"
        ''')

        module_ir = analyze_source(source, "test.py")
        obj = module_ir.objects[0]

        assert len(obj.functions) == 1
        func = obj.functions[0]

        assert func.python_name == "hello"
        assert func.api_name == "hello"
        assert func.doc == "Say hello."
        assert len(func.parameters) == 1

        param = func.parameters[0]
        assert param.python_name == "name"
        assert param.api_name == "name"
        assert param.annotation.raw == "str"

        assert func.return_annotation is not None
        assert func.return_annotation.raw == "str"

    def test_function_with_default_parameter(self):
        """Test analyzing function with default parameter."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def greet(self, name: str = "World") -> str:
                    return f"Hello, {name}!"
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]
        param = func.parameters[0]

        assert param.has_default
        assert param.default_value == "World"
        # AST unparse uses single quotes
        assert param.default_repr == "'World'"

    def test_function_with_optional_type(self):
        """Test analyzing function with optional type."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def process(self, data: str | None) -> str:
                    return data or ""
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]
        param = func.parameters[0]

        assert param.annotation.is_optional

    def test_object_with_field(self):
        """Test analyzing object type with a field."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                name: str = dagger.field(default="default")
        """)

        module_ir = analyze_source(source, "test.py")
        obj = module_ir.objects[0]

        assert len(obj.fields) == 1
        field = obj.fields[0]

        assert field.python_name == "name"
        assert field.api_name == "name"
        assert field.has_default
        assert field.default_value == "default"

    def test_field_with_deprecation(self):
        """Test analyzing deprecated field."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                old_name: str = dagger.field(default="", deprecated="Use new_name")
        """)

        module_ir = analyze_source(source, "test.py")
        field = module_ir.objects[0].fields[0]

        assert field.deprecated == "Use new_name"

    def test_function_with_check_decorator(self):
        """Test analyzing function with @check decorator."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                @dagger.check
                def lint(self) -> str:
                    return "OK"
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.is_check

    def test_function_with_cache_policy(self):
        """Test analyzing function with cache policy."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function(cache="never")
                def no_cache(self) -> str:
                    return "fresh"
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.cache_policy == "never"

    def test_function_deprecated(self):
        """Test analyzing deprecated function."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function(deprecated="Use new_method instead")
                def old_method(self) -> str:
                    return ""
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.deprecated == "Use new_method instead"

    def test_async_function(self):
        """Test analyzing async function."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                async def fetch(self) -> str:
                    return ""
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.is_async

    def test_interface_type(self):
        """Test analyzing interface type."""
        source = textwrap.dedent('''
            import dagger
            import typing

            @dagger.interface
            class Buildable(typing.Protocol):
                """Interface for buildable objects."""

                @dagger.function
                def build(self) -> str:
                    ...
        ''')

        module_ir = analyze_source(source, "test.py", main_object_name="Buildable")
        obj = module_ir.objects[0]

        assert obj.is_interface
        assert obj.doc == "Interface for buildable objects."

    def test_enum_type(self):
        """Test analyzing enum type."""
        source = textwrap.dedent('''
            import enum
            import dagger

            @dagger.enum_type
            class Status(enum.Enum):
                """Status enumeration."""

                PENDING = "pending"
                """Waiting to start."""

                DONE = "done"
                """Completed."""

            @dagger.object_type
            class Main:
                pass
        ''')

        module_ir = analyze_source(source, "test.py")

        # Find enum
        enum_obj = module_ir.get_object("Status")
        assert enum_obj is not None
        assert enum_obj.is_enum
        assert enum_obj.doc == "Status enumeration."

        assert len(enum_obj.enum_members) == 2

        pending = enum_obj.enum_members[0]
        assert pending.name == "PENDING"
        # Value is the string content without enclosing quotes
        assert pending.value == "pending"
        assert pending.doc == "Waiting to start."

        done = enum_obj.enum_members[1]
        assert done.name == "DONE"
        assert done.value == "done"
        assert done.doc == "Completed."

    def test_name_conversion(self):
        """Test Python to API name conversion."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                my_field: str = dagger.field(default="")

                @dagger.function
                def my_function(self, some_param: str) -> str:
                    return some_param
        """)

        module_ir = analyze_source(source, "test.py")
        obj = module_ir.objects[0]

        # snake_case preserved for API (camelCase conversion happens in TypeDef)
        assert obj.fields[0].api_name == "myField"
        assert obj.functions[0].api_name == "myFunction"
        assert obj.functions[0].parameters[0].api_name == "someParam"

    def test_trailing_underscore_normalization(self):
        """Test that trailing underscores are normalized."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def from_(self, path: str) -> str:
                    return path
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.api_name == "from"

    def test_custom_api_name(self):
        """Test custom API name via decorator."""
        source = textwrap.dedent("""
            import dagger
            from typing import Annotated

            @dagger.object_type
            class Main:
                @dagger.function(name="customName")
                def original_name(self) -> str:
                    return ""
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.python_name == "original_name"
        assert func.api_name == "customName"

    def test_explicit_constructor_empty_name(self):
        """Test that @function(name="") creates a constructor."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function(name="")
                def create(self) -> "Main":
                    return Main()
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.python_name == "create"
        assert func.api_name == ""  # Empty name = constructor

    def test_multiple_objects(self):
        """Test analyzing multiple object types."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def get_helper(self) -> "Helper":
                    pass

            @dagger.object_type
            class Helper:
                @dagger.function
                def help(self) -> str:
                    return "Helping!"
        """)

        module_ir = analyze_source(source, "test.py")

        assert len(module_ir.objects) == 2
        assert {obj.name for obj in module_ir.objects} == {"Main", "Helper"}

    def test_list_return_type(self):
        """Test analyzing function with list return type."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                @dagger.function
                def get_names(self) -> list[str]:
                    return []
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]

        assert func.return_annotation is not None
        assert func.return_annotation.is_list
        assert func.return_annotation.element_type == "str"

    def test_annotated_parameter(self):
        """Test analyzing parameter with Annotated metadata."""
        source = textwrap.dedent("""
            import dagger
            from typing import Annotated
            from typing_extensions import Doc

            @dagger.object_type
            class Main:
                @dagger.function
                def process(
                    self,
                    data: Annotated[str, Doc("Input data")],
                ) -> str:
                    return data
        """)

        module_ir = analyze_source(source, "test.py")
        func = module_ir.objects[0].functions[0]
        param = func.parameters[0]

        assert param.annotation.doc == "Input data"

    def test_missing_main_object_raises_error(self):
        """Test that missing main object raises ModuleLoadError."""
        source = textwrap.dedent("""
            # No decorated classes, just a plain function
            def helper():
                pass
        """)

        with pytest.raises(ModuleLoadError, match="Main.*not found"):
            analyze_source(source, "test.py", main_object_name="Main")

    def test_object_deprecated(self):
        """Test analyzing deprecated object type."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type(deprecated="Use NewMain instead")
            class Main:
                pass
        """)

        module_ir = analyze_source(source, "test.py")
        obj = module_ir.objects[0]

        assert obj.deprecated == "Use NewMain instead"


class TestModuleIRHelpers:
    """Tests for ModuleIR helper methods."""

    def test_get_main_object(self):
        """Test get_main_object method."""
        source = textwrap.dedent("""
            import dagger

            @dagger.object_type
            class Main:
                pass

            @dagger.object_type
            class Helper:
                pass
        """)

        module_ir = analyze_source(source, "test.py")
        main = module_ir.get_main_object()

        assert main is not None
        assert main.name == "Main"

    def test_get_enums(self):
        """Test get_enums method."""
        source = textwrap.dedent("""
            import enum
            import dagger

            @dagger.enum_type
            class Status(enum.Enum):
                OK = "ok"

            @dagger.object_type
            class Main:
                pass
        """)

        module_ir = analyze_source(source, "test.py")
        enums = module_ir.get_enums()

        assert len(enums) == 1
        assert enums[0].name == "Status"

    def test_get_interfaces(self):
        """Test get_interfaces method."""
        source = textwrap.dedent("""
            import dagger
            import typing

            @dagger.interface
            class Runnable(typing.Protocol):
                pass

            @dagger.object_type
            class Main:
                pass
        """)

        module_ir = analyze_source(source, "test.py")
        interfaces = module_ir.get_interfaces()

        assert len(interfaces) == 1
        assert interfaces[0].name == "Runnable"

    def test_get_object_types(self):
        """Test get_object_types method excludes enums and interfaces."""
        source = textwrap.dedent("""
            import enum
            import typing
            import dagger

            @dagger.enum_type
            class Status(enum.Enum):
                OK = "ok"

            @dagger.interface
            class Runnable(typing.Protocol):
                pass

            @dagger.object_type
            class Main:
                pass

            @dagger.object_type
            class Helper:
                pass
        """)

        module_ir = analyze_source(source, "test.py")
        objects = module_ir.get_object_types()

        assert len(objects) == 2
        assert {obj.name for obj in objects} == {"Main", "Helper"}
