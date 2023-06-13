from dataclasses import dataclass

import dagger


@dataclass
class Base:
    src_dir: dagger.Directory
