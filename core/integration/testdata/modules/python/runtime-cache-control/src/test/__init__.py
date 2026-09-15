import random
import string

import dagger


@dagger.object_type
class Test:
    @dagger.function(cache="40s")
    def test_ttl(self) -> str:
        return "".join(random.choices(string.ascii_lowercase + string.digits, k=10))

    @dagger.function(cache="session")
    def test_cache_per_session(self) -> str:
        return "".join(random.choices(string.ascii_lowercase + string.digits, k=10))

    @dagger.function(cache="never")
    def test_never_cache(self) -> str:
        return "".join(random.choices(string.ascii_lowercase + string.digits, k=10))

    @dagger.function
    def test_always_cache(self) -> str:
        return "".join(random.choices(string.ascii_lowercase + string.digits, k=10))
