"""Tests for AST-based module analysis."""

import textwrap

import pytest

from dagger.mod._ast_analyzer import ModuleAnalyzer


class TestModuleAnalyzer:
    """Test the ModuleAnalyzer class."""

    def test_simple_object_type(self):
        """Test parsing a simple object type."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                """My module description."""

                @dagger.function
                def hello(self) -> str:
                    """Say hello."""
                    return "hello"
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        assert len(info.objects) == 1
        obj = info.objects[0]
        assert obj.name == "MyModule"
        assert obj.docstring == "My module description."
        assert not obj.is_interface

        assert len(obj.functions) == 1
        func = obj.functions[0]
        assert func.original_name == "hello"
        assert func.name == "hello"
        assert func.docstring == "Say hello."
        assert func.return_annotation is not None
        assert func.return_annotation.base_type == "str"

    def test_function_with_parameters(self):
        """Test parsing function parameters."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def greet(self, name: str, count: int = 1) -> str:
                    return f"Hello {name}" * count
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert len(func.parameters) == 2

        # First param: name
        name_param = func.parameters[0]
        assert name_param.name == "name"
        assert name_param.annotation.base_type == "str"
        assert not name_param.has_default

        # Second param: count with default
        count_param = func.parameters[1]
        assert count_param.name == "count"
        assert count_param.annotation.base_type == "int"
        assert count_param.has_default
        assert count_param.default_value == 1

    def test_annotated_parameters(self):
        """Test parsing Annotated parameters with metadata."""
        source = textwrap.dedent('''
            from typing import Annotated
            import dagger
            from dagger.mod._arguments import Name, Doc, DefaultPath, Ignore

            @dagger.object_type
            class MyModule:
                @dagger.function
                def build(
                    self,
                    src: Annotated[dagger.Directory, Doc("Source directory"), DefaultPath(".")],
                    from_: Annotated[str, Name("from"), Doc("Base image")],
                ) -> dagger.Container:
                    pass
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert len(func.parameters) == 2

        # src param with DefaultPath
        src_param = func.parameters[0]
        assert src_param.annotation.base_type == "Directory"
        assert src_param.doc == "Source directory"
        assert src_param.default_path == "."

        # from_ param with Name alias
        from_param = func.parameters[1]
        assert from_param.alt_name == "from"
        assert from_param.doc == "Base image"

    def test_optional_types(self):
        """Test parsing optional/nullable types."""
        source = textwrap.dedent('''
            import dagger
            from typing import Optional

            @dagger.object_type
            class MyModule:
                @dagger.function
                def maybe(self, value: str | None) -> Optional[str]:
                    return value

                @dagger.function
                def also_maybe(self, value: Optional[int]) -> int | None:
                    return value
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        assert len(info.objects[0].functions) == 2

        # First function with str | None
        func1 = info.objects[0].functions[0]
        assert func1.parameters[0].annotation.is_optional
        assert func1.parameters[0].annotation.base_type == "str"
        assert func1.return_annotation.is_optional

        # Second function with Optional[int]
        func2 = info.objects[0].functions[1]
        assert func2.parameters[0].annotation.is_optional
        assert func2.parameters[0].annotation.base_type == "int"

    def test_list_types(self):
        """Test parsing list types."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def echo_all(self, values: list[str]) -> list[str]:
                    return values
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.parameters[0].annotation.base_type == "list"
        assert len(func.parameters[0].annotation.type_args) == 1
        assert func.parameters[0].annotation.type_args[0].base_type == "str"

    def test_field_definition(self):
        """Test parsing field definitions."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                name: str = dagger.field()
                count: int = dagger.field(default=0)
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        obj = info.objects[0]
        assert len(obj.fields) == 2

        name_field = obj.fields[0]
        assert name_field.original_name == "name"
        assert name_field.annotation.base_type == "str"

        count_field = obj.fields[1]
        assert count_field.original_name == "count"
        assert count_field.has_default

    def test_enum_type(self):
        """Test parsing enum types."""
        source = textwrap.dedent('''
            import enum
            import dagger

            @dagger.enum_type
            class Status(enum.Enum):
                """Status enum."""
                PENDING = "pending"
                """Waiting for processing."""
                DONE = "done"
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        assert len(info.enums) == 1
        enum_def = info.enums[0]
        assert enum_def.name == "Status"
        assert enum_def.docstring == "Status enum."
        assert len(enum_def.members) == 2

        pending = enum_def.members[0]
        assert pending.name == "PENDING"
        assert pending.value == "pending"
        assert pending.docstring == "Waiting for processing."

        done = enum_def.members[1]
        assert done.name == "DONE"
        assert done.value == "done"

    def test_interface_type(self):
        """Test parsing interface types."""
        source = textwrap.dedent('''
            import typing
            import dagger

            @dagger.interface
            class Buildable(typing.Protocol):
                """Something that can be built."""

                @dagger.function
                def build(self) -> dagger.Container:
                    ...
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        assert len(info.objects) == 1
        obj = info.objects[0]
        assert obj.name == "Buildable"
        assert obj.is_interface
        assert obj.docstring == "Something that can be built."

    def test_decorator_with_args(self):
        """Test parsing decorators with arguments."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type(deprecated="Use NewModule instead")
            class OldModule:
                @dagger.function(name="customName", doc="Custom doc", deprecated="Old function")
                def old_func(self) -> str:
                    return "old"
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        obj = info.objects[0]
        assert obj.deprecated == "Use NewModule instead"

        func = obj.functions[0]
        assert func.alt_name == "customName"
        assert func.alt_doc == "Custom doc"
        assert func.deprecated == "Old function"

    def test_check_decorator(self):
        """Test parsing @check decorator."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                @dagger.check
                def lint(self) -> str:
                    return "ok"
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.is_check

    def test_async_function(self):
        """Test parsing async functions."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                async def fetch(self) -> str:
                    return "data"
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.is_async

    def test_dagger_types(self):
        """Test parsing Dagger API types."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def build(self, src: dagger.Directory) -> dagger.Container:
                    pass
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.parameters[0].annotation.base_type == "Directory"
        assert func.return_annotation.base_type == "Container"

    def test_self_return_type(self):
        """Test parsing Self return type."""
        source = textwrap.dedent('''
            import dagger
            from typing import Self

            @dagger.object_type
            class MyModule:
                @dagger.function
                def configure(self) -> Self:
                    return self
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        # Self should be resolved to the class name
        assert func.return_annotation.base_type == "MyModule"

    def test_forward_reference(self):
        """Test parsing forward reference types."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def get_helper(self) -> "Helper":
                    pass

            @dagger.object_type
            class Helper:
                pass
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.return_annotation.base_type == "Helper"

    def test_module_docstring(self):
        """Test extracting module-level docstring."""
        source = textwrap.dedent('''
            """This is the module description."""
            import dagger

            @dagger.object_type
            class MyModule:
                pass
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        assert info.module_doc == "This is the module description."

    def test_camel_case_conversion(self):
        """Test that function names are converted to camelCase."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def my_long_function_name(self, some_param: str) -> str:
                    return some_param
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        assert func.original_name == "my_long_function_name"
        assert func.name == "myLongFunctionName"
        assert func.parameters[0].name == "someParam"

    def test_trailing_underscore_removal(self):
        """Test that trailing underscores are removed from names."""
        source = textwrap.dedent('''
            import dagger

            @dagger.object_type
            class MyModule:
                @dagger.function
                def import_(self, from_: str) -> str:
                    return from_
        ''')

        analyzer = ModuleAnalyzer()
        info = analyzer.analyze_source(source)

        func = info.objects[0].functions[0]
        # import_ -> import (camelCase stays as import since it's single word)
        assert func.name == "import"
        # from_ -> from
        assert func.parameters[0].name == "from"
