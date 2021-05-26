setup() {
    load 'helpers'

    common_setup
}

@test "dagger init" {
    run "$DAGGER" init
    assert_success

    run "$DAGGER" list
    assert_success
    refute_output

    run "$DAGGER" init
    assert_failure
}

@test "dagger new" {
    run "$DAGGER" new "test"
    assert_failure

    run "$DAGGER" init
    assert_success

    run "$DAGGER" list
    assert_success
    refute_output

    run "$DAGGER" new "test"
    assert_success

    run "$DAGGER" list
    assert_success
    assert_output --partial "test"

    run "$DAGGER" new "test"
    assert_failure
}

@test "dagger query" {
    "$DAGGER" init

    dagger_new_with_plan simple "$TESTDIR"/cli/simple

    run "$DAGGER" query -l error -e "simple"
    assert_success
    assert_output '{
  "bar": "another value",
  "foo": "value"
}'
    # concrete should fail at this point since we haven't up'd
    run "$DAGGER" query -e "simple" -c
    assert_failure

    # target
    run "$DAGGER" -l error query -e "simple" foo
    assert_success
    assert_output '"value"'

    # ensure computed values show up
    "$DAGGER" up -e "simple"
    run "$DAGGER" -l error query -e "simple"
    assert_success
    assert_output --partial '"computed": "test"'

    # concrete should now work
    "$DAGGER" query -e "simple" -c

    # --no-computed should yield the same result as before
    run "$DAGGER" query -l error --no-computed -e "simple"
    assert_success
    assert_output '{
  "bar": "another value",
  "foo": "value"
}'

    # --no-plan should give us only the computed values
    run "$DAGGER" query -l error --no-plan -e "simple"
    assert_success
    assert_output '{
  "computed": "test"
}'
}

@test "dagger input text" {
    "$DAGGER" init

    dagger_new_with_plan input "$TESTDIR"/cli/input/simple

    # simple input
    "$DAGGER" input -e "input" text "input" "my input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output '"my input"'

    # unset simple input
    "$DAGGER" input -e "input" unset "input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output 'null'

    # nested input
    "$DAGGER" input -e "input" text "nested.input" "nested input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" nested
    assert_success
    assert_output '{
  "input": "nested input"
}'

    # unset nested input
    "$DAGGER" input -e "input" unset "nested.input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" nested
    assert_success
    assert_output 'null'

    # file input
    "$DAGGER" input -e "input" text "input" -f "$TESTDIR"/cli/input/simple/testdata/input.txt
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output '"from file\n"'

    # unset file input
    "$DAGGER" input -e "input" unset "input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output 'null'

    # invalid file
    run "$DAGGER" input -e "input" text "input" -f "$TESTDIR"/cli/input/simple/testdata/notexist
    assert_failure

    # stdin input
    echo -n "from stdin" | "$DAGGER" input -e "input" text "input" -f -
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output '"from stdin"'

    # unset stdin input
    "$DAGGER" input -e "input" unset "input"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" input
    assert_success
    assert_output 'null'
}

@test "dagger input json" {
    "$DAGGER" init

    dagger_new_with_plan input "$TESTDIR"/cli/input/simple

    # simple json
    "$DAGGER" input -e "input" json "structured" '{"a": "foo", "b": 42}'
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output '{
  "a": "foo",
  "b": 42
}'

    # unset simple json
    "$DAGGER" input -e "input" unset "structured"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output 'null'

    # json from file
    "$DAGGER" input -e "input" json "structured" -f "$TESTDIR"/cli/input/simple/testdata/input.json
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output '{
  "a": "from file",
  "b": 42
}'

    # unset json from file
    "$DAGGER" input -e "input" unset "structured"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output 'null'
}

@test "dagger input yaml" {
    "$DAGGER" init

    dagger_new_with_plan input "$TESTDIR"/cli/input/simple

    # simple yaml
    "$DAGGER" input -e "input" yaml "structured" '{"a": "foo", "b": 42}'
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output '{
  "a": "foo",
  "b": 42
}'

    # unset simple yaml
    "$DAGGER" input -e "input" unset "structured"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output 'null'

    # yaml from file
    "$DAGGER" input -e "input" yaml "structured" -f "$TESTDIR"/cli/input/simple/testdata/input.yaml
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output '{
  "a": "from file",
  "b": 42
}'

    # unset yaml from file
    "$DAGGER" input -e "input" unset "structured"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input" structured
    assert_success
    assert_output 'null'
}

@test "dagger input dir" {
    "$DAGGER" init

    dagger_new_with_plan input "$TESTDIR"/cli/input/artifact

    # input dir outside the workspace
    run "$DAGGER" input -e "input" dir "source" /tmp
    assert_failure

    # input dir inside the workspace
    cp -R "$TESTDIR"/cli/input/artifact/testdata/ "$DAGGER_WORKSPACE"/testdata
    "$DAGGER" input -e "input" dir "source" "$DAGGER_WORKSPACE"/testdata
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input"
    assert_success
    assert_output '{
  "bar": "thisisatest\n",
  "foo": "bar",
  "source": {}
}'

    # unset dir
    "$DAGGER" input -e "input" unset "source"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input"
    assert_success
    assert_output '{
  "foo": "bar"
}'
}

@test "dagger input git" {
    "$DAGGER" init

    dagger_new_with_plan input "$TESTDIR"/cli/input/artifact

    # input git
    "$DAGGER" input -e "input" git "source" https://github.com/samalba/dagger-test-simple.git
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input"
    assert_output '{
  "bar": "testgit\n",
  "foo": "bar",
  "source": {}
}'

    # unset input git
    "$DAGGER" input -e "input" unset "source"
    "$DAGGER" up -e "input"
    run "$DAGGER" -l error query -e "input"
    assert_output '{
  "foo": "bar"
}'
}

@test "dagger input list" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/list "list"
    "$DAGGER" input text cfg.str "foobar" -e "list"

    out="$("$DAGGER" input list -e "list")"
    outAll="$("$DAGGER" input list --all -e "list")"

    #note: this is the recommended way to use pipes with bats
    run bash -c "echo \"$out\" | grep awsConfig.accessKey | grep '#Secret' | grep false"
    assert_success

    run bash -c "echo \"$out\" | grep cfgInline.source | grep '#Artifact' | grep false | grep 'source dir'"
    assert_success

    run bash -c "echo \"$outAll\" | grep cfg2"
    assert_failure

    run bash -c "echo \"$out\" | grep cfgInline.strDef | grep string | grep 'yolo (default)' | grep false"
    assert_success

    run bash -c "echo \"$out\" | grep cfg.num"
    assert_failure

    run bash -c "echo \"$outAll\" | grep cfg.num | grep int"
    assert_success

    run bash -c "echo \"$out\" | grep cfg.strSet"
    assert_failure

    run bash -c "echo \"$outAll\" | grep cfg.strSet | grep string | grep pipo"
    assert_success
}
