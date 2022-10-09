import argparse

from attrs import define


@define
class Options:
    schema: bool = False


def parse_args() -> Options:
    args = Options()
    parser = argparse.ArgumentParser()
    parser.add_argument("-schema", action="store_true", help="Write schema file")
    return parser.parse_args(namespace=args)
