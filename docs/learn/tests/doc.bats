setup() {
    load 'helpers'

    common_setup
}

@test "doc-102" {
    dagger -e 102 up
}

@test "doc-106" {
    dagger -e 106 up
}
