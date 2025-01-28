import dagger


def mounted_workdir(src: dagger.Directory):
    """Add directory as a mount on a container, under `/work`."""

    def _workdir(ctr: dagger.Container) -> dagger.Container:
        return ctr.with_mounted_directory("/work", src).with_workdir("/work")

    return _workdir
