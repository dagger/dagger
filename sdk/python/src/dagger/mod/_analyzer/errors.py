"""AST analysis error classes.

All errors include source location for clear error messages.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

from dagger.mod._exceptions import ModuleError

if TYPE_CHECKING:
    from dagger.mod._analyzer.metadata import LocationMetadata


class AnalysisError(ModuleError):
    """Base class for AST analysis errors."""

    def __init__(
        self,
        message: str,
        *,
        location: LocationMetadata | None = None,
        extra: dict | None = None,
    ):
        self.location = location
        if location:
            message = f"{location}: {message}"
        super().__init__(message, extra=extra)


class ParseError(AnalysisError):
    """Error parsing Python source code."""


class DeclarationError(AnalysisError):
    """Error extracting declarations from AST."""


class TypeResolutionError(AnalysisError):
    """Error resolving type annotations."""

    def __init__(
        self,
        message: str,
        *,
        annotation: str | None = None,
        location: LocationMetadata | None = None,
        extra: dict | None = None,
    ):
        if annotation:
            message = f"{message}\n  annotation: {annotation}"
        super().__init__(message, location=location, extra=extra)


class ValidationError(AnalysisError):
    """Error validating module structure or usage patterns."""


class DecoratorError(AnalysisError):
    """Error processing decorator."""

    def __init__(
        self,
        message: str,
        *,
        decorator_name: str | None = None,
        location: LocationMetadata | None = None,
        extra: dict | None = None,
    ):
        if decorator_name:
            message = f"@{decorator_name}: {message}"
        super().__init__(message, location=location, extra=extra)
