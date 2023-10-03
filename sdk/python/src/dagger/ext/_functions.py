import json
import logging
from typing import Any, TypeAlias

from typing_extensions import override

import dagger

from ._converter import to_typedef
from ._resolver import Resolver

logger = logging.getLogger(__name__)

FunctionReturnType: TypeAlias = Any


class FunctionResolver(Resolver[FunctionReturnType]):
    @override
    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        fn = dagger.new_function(self.graphql_name, to_typedef(self.return_type))

        if self.description:
            fn = fn.with_description(self.description)

        for arg_name, param in self.parameters.items():
            fn = fn.with_arg(
                arg_name,
                to_typedef(param.signature.annotation),
                description=param.description,
                default_value=(
                    dagger.JSON(json.dumps(param.signature.default))
                    if param.is_optional
                    else None
                ),
            )

        return typedef.with_function(fn)
