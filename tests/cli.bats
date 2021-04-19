setup() {
    load 'helpers'

    common_setup
}

@test "dagger list" {
    run "$DAGGER" list
    assert_success
    assert_output ""

    "$DAGGER" new --plan-dir "$TESTDIR"/cli/simple simple

    run "$DAGGER" list
    assert_success
    assert_output --partial "simple"
}

@test "dagger new --plan-dir" {
    run "$DAGGER" list
    assert_success
    assert_output ""

    "$DAGGER" new --plan-dir "$TESTDIR"/cli/simple simple

    # duplicate name
    run "$DAGGER" new --plan-dir "$TESTDIR"/cli/simple simple
    assert_failure

    # verify the plan works
    "$DAGGER" up -d "simple"

    # verify we have the right plan
    run "$DAGGER" query -f cue -d "simple" -c -f json
    assert_success
    assert_output --partial '{
  "bar": "another value",
  "computed": "test",
  "foo": "value"
}'
}

@test "dagger new --plan-git" {
    "$DAGGER" new --plan-git https://github.com/samalba/dagger-test.git simple
    "$DAGGER" up -d "simple"
    run "$DAGGER" query -f cue -d "simple" -c
    assert_success
    assert_output --partial '{
    foo: "value"
    bar: "another value"
}'
}

@test "dagger query" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/simple simple
    run "$DAGGER" query -l error -d "simple"
    assert_success
    assert_output '{
  "bar": "another value",
  "foo": "value"
}'
    # concrete should fail at this point since we haven't up'd
    run "$DAGGER" query -d "simple" -c
    assert_failure

    # target
    run "$DAGGER" -l error query -d "simple" foo
    assert_success
    assert_output '"value"'

    # ensure computed values show up
    "$DAGGER" up -d "simple"
    run "$DAGGER" -l error query -d "simple"
    assert_success
    assert_output --partial '"computed": "test"'

    # concrete should now work
    "$DAGGER" query -d "simple" -c

    # --no-computed should yield the same result as before
    run "$DAGGER" query -l error --no-computed -d "simple"
    assert_success
    assert_output '{
  "bar": "another value",
  "foo": "value"
}'

    # --no-plan should give us only the computed values
    run "$DAGGER" query -l error --no-plan -d "simple"
    assert_success
    assert_output '{
  "computed": "test"
}'
}

@test "dagger plan" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/simple simple

    # plan dir
    "$DAGGER" -d "simple" plan dir "$TESTDIR"/cli/simple
    run "$DAGGER" -d "simple" query
    assert_success
    assert_output --partial '"foo": "value"'

    # plan git
    "$DAGGER" -d "simple" plan git https://github.com/samalba/dagger-test.git
    run "$DAGGER" -d "simple" query
    assert_success
    assert_output --partial '"foo": "value"'
}

@test "dagger input text" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/simple "input"

    # simple input
    "$DAGGER" input -d "input" text "input" "my input"
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" input
    assert_success
    assert_output '"my input"'

    # nested input
    "$DAGGER" input -d "input" text "nested.input" "nested input"
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" nested
    assert_success
    assert_output '{
  "input": "nested input"
}'

    # file input
    "$DAGGER" input -d "input" text "input" -f "$TESTDIR"/cli/input/simple/testdata/input.txt
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" input
    assert_success
    assert_output '"from file\n"'

    # invalid file
    run "$DAGGER" input -d "input" text "input" -f "$TESTDIR"/cli/input/simple/testdata/notexist
    assert_failure

    # stdin input
    echo -n "from stdin" | "$DAGGER" input -d "input" text "input" -f -
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" input
    assert_success
    assert_output '"from stdin"'
}

@test "dagger input json" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/simple "input"

    "$DAGGER" input -d "input" json "structured" '{"a": "foo", "b": 42}'
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" structured
    assert_success
    assert_output '{
  "a": "foo",
  "b": 42
}'

    "$DAGGER" input -d "input" json "structured" -f "$TESTDIR"/cli/input/simple/testdata/input.json
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" structured
    assert_success
    assert_output '{
  "a": "from file",
  "b": 42
}'
}

@test "dagger input yaml" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/simple "input"

    "$DAGGER" input -d "input" yaml "structured" '{"a": "foo", "b": 42}'
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" structured
    assert_success
    assert_output '{
  "a": "foo",
  "b": 42
}'

    "$DAGGER" input -d "input" yaml "structured" -f "$TESTDIR"/cli/input/simple/testdata/input.yaml
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input" structured
    assert_success
    assert_output '{
  "a": "from file",
  "b": 42
}'
}

@test "dagger input dir" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/artifact "input"

    "$DAGGER" input -d "input" dir "source" "$TESTDIR"/cli/input/artifact/testdata
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input"
    assert_success
    assert_output '{
  "bar": "thisisatest\n",
  "foo": "bar",
  "source": {}
}'
}

@test "dagger input git" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/artifact "input"

    "$DAGGER" input -d "input" git "source" https://github.com/samalba/dagger-test-simple.git
    "$DAGGER" up -d "input"
    run "$DAGGER" -l error query -d "input"
    assert_output '{
  "bar": "testgit\n",
  "foo": "bar",
  "source": {}
}'
}

@test "dagger input scan" {
    "$DAGGER" new --plan-dir "$TESTDIR"/cli/input/scan "scan"
    run "$DAGGER" input scan -d "input"
    assert_success

}
