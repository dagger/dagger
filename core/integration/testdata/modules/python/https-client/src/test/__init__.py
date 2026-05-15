import urllib.request

import dagger


@dagger.object_type
class Test:
    @dagger.function
    def get_http(self) -> str:
        return urllib.request.urlopen("https://server").read().decode("utf-8")
