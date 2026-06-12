import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_platform(self, platform: dagger.Platform) -> str:
        return str(platform)

    @function
    def to_platform(self, platform: str) -> dagger.Platform:
        return dagger.Platform(platform)

    @function
    def from_platforms(self, platform: list[dagger.Platform]) -> list[str]:
        return [str(p) for p in platform]

    @function
    def to_platforms(self, platform: list[str]) -> list[dagger.Platform]:
        return [dagger.Platform(p) for p in platform]
