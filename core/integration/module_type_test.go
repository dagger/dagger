package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

type TypeSuite struct{}

func TestType(t *testing.T) {
	testctx.Run(testCtx, t, TypeSuite{}, Middleware()...)
}

func (TypeSuite) TestCustomTypes(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "strings"

type Test struct{}

func (m *Test) Repeater(msg string, times int) *Repeater {
	return &Repeater{
		Message: msg,
		Times:   times,
	}
}

type Repeater struct {
	Message string
	Times   int
}

func (t *Repeater) Render() string {
	return strings.Repeat(t.Message, t.Times)
}
`,
		},
		{
			sdk: "python",
			source: `import dagger

@dagger.object_type
class Repeater:
    message: str = dagger.field(default="")
    times: int = dagger.field(default=0)

    @dagger.function
    def render(self) -> str:
        return self.message * self.times


@dagger.object_type
class Test:
    @dagger.function
    def repeater(self, msg: str, times: int) -> Repeater:
        return Repeater(message=msg, times=times)
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Repeater {
  @func()
  message: string

  @func()
  times: number

  constructor(message: string, times: number) {
    this.message = message
    this.times = times
  }

  @func()
  render(): string {
    return this.message.repeat(this.times)
  }
}

@object()
export class Test {
  @func()
  repeater(msg: string, times: number): Repeater {
    return new Repeater(msg, times)
  }
}
`,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("custom %s types", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{repeater(msg:"echo!", times: 3){render}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"repeater":{"render":"echo!echo!echo!"}}}`, out)
		})
	}
}

func (TypeSuite) TestReturnTypeDetection(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct {}

type X struct {
	Message string ` + "`json:\"message\"`" + `
}

func (m *Test) MyFunction() X {
	return X{Message: "foo"}
}
`,
		},
		{
			sdk: "python",
			source: `import dagger

@dagger.object_type
class X:
    message: str = dagger.field(default="")

@dagger.object_type
class Test:
    @dagger.function
    def my_function(self) -> X:
        return X(message="foo")
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class X {
  @func()
  message: string

  constructor(message: string) {
    this.message = message;
  }
}

@object()
export class Test {
  @func()
  myFunction(): X {
    return new X("foo");
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{myFunction{message}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"myFunction":{"message":"foo"}}}`, out)
		})
	}
}

func (TypeSuite) TestReturnObject(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct {}

type X struct {
	Message string ` + "`json:\"message\"`" + `
	When string ` + "`json:\"Timestamp\"`" + `
	To string ` + "`json:\"recipient\"`" + `
	From string
}

func (m *Test) MyFunction() X {
	return X{Message: "foo", When: "now", To: "user", From: "admin"}
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class X:
    message: str = field(default="")
    when: str = field(default="", name="Timestamp")
    to: str = field(default="", name="recipient")
    from_: str = field(default="", name="from")

@object_type
class Test:
    @function
    def my_function(self) -> X:
        return X(message="foo", when="now", to="user", from_="admin")
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class X {
  @func()
  message: string

  @func()
  timestamp: string

  @func()
  recipient: string

  @func()
  from: string

  constructor(message: string, timestamp: string, recipient: string, from: string) {
    this.message = message;
    this.timestamp = timestamp;
    this.recipient = recipient;
    this.from = from;
  }
}

@object()
export class Test {
  @func()
  myFunction(): X {
    return new X("foo", "now", "user", "admin");
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{myFunction{message, recipient, from, timestamp}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"myFunction":{"message":"foo", "recipient":"user", "from":"admin", "timestamp":"now"}}}`, out)
		})
	}
}

func (TypeSuite) TestReturnNestedObject(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct{}

type Foo struct {
	MsgContainer Bar
}

type Bar struct {
	Msg string
}

func (m *Test) MyFunction() Foo {
	return Foo{MsgContainer: Bar{Msg: "hello world"}}
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Bar:
    msg: str = field()

@object_type
class Foo:
    msg_container: Bar = field()

@object_type
class Test:
    @function
    def my_function(self) -> Foo:
        return Foo(msg_container=Bar(msg="hello world"))
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Bar {
  @func()
  msg: string;

  constructor(msg: string) {
    this.msg = msg;
  }
}

@object()
export class Foo {
  @func()
  msgContainer: Bar;

  constructor(msgContainer: Bar) {
    this.msgContainer = msgContainer;
  }
}

@object()
export class Test {
  @func()
  myFunction(): Foo {
    return new Foo(new Bar("hello world"));
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{myFunction{msgContainer{msg}}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"myFunction":{"msgContainer":{"msg": "hello world"}}}}`, out)
		})
	}
}

func (TypeSuite) TestReturnCompositeCore(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) MySlice() []*dagger.Container {
	return []*dagger.Container{dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"})}
}

type Foo struct {
	Con *dagger.Container
	// verify fields can remain nil w/out error too
	UnsetFile *dagger.File
}

func (m *Test) MyStruct() *Foo {
	return &Foo{Con: dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"})}
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class Foo:
    con: dagger.Container = dagger.field()
    unset_file: dagger.File | None = dagger.field(default=None)

@dagger.object_type
class Test:
    @dagger.function
    def my_slice(self) -> list[dagger.Container]:
        return [dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"])]

    @dagger.function
    def my_struct(self) -> Foo:
        return Foo(con=dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"]))
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, File, object, func } from "@dagger.io/dagger"

@object()
export class Foo {
  @func()
  con: Container

  @func()
  unsetFile?: File

  constructor(con: Container, unsetFile?: File) {
    this.con = con
    this.unsetFile = unsetFile
  }
}

@object()
export class Test {
  @func()
  mySlice(): Container[] {
    return [
      dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
    ]
  }

  @func()
  myStruct(): Foo {
    return new Foo(
      dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
    )
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{mySlice{stdout}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"mySlice":[{"stdout":"hello world\n"}]}}`, out)

			out, err = modGen.With(daggerQuery(`{test{myStruct{con{stdout}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"myStruct":{"con":{"stdout":"hello world\n"}}}}`, out)
		})
	}
}

func (TypeSuite) TestReturnComplexThing(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

type ScanResult struct {
	Containers	[]*dagger.Container ` + "`json:\"targets\"`" + `
	Report		ScanReport
}

type ScanReport struct {
	Contents string ` + "`json:\"contents\"`" + `
	Authors  []string ` + "`json:\"Authors\"`" + `
}

func (m *Test) Scan() ScanResult {
	return ScanResult{
		Containers: []*dagger.Container{
			dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"}),
		},
		Report: ScanReport{
			Contents: "hello world",
			Authors: []string{"foo", "bar"},
		},
	}
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class ScanReport:
    contents: str = dagger.field()
    authors: list[str] = dagger.field()

@dagger.object_type
class ScanResult:
    containers: list[dagger.Container] = dagger.field(name="targets")
    report: ScanReport = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def scan(self) -> ScanResult:
        return ScanResult(
            containers=[
                dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"]),
            ],
            report=ScanReport(
                contents="hello world",
                authors=["foo", "bar"],
            ),
        )
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
export class ScanReport {
  @func()
  contents: string

  @func()
  authors: string[]

  constructor(contents: string, authors: string[]) {
    this.contents = contents
    this.authors = authors
  }
}

@object()
export class ScanResult {
  @func("targets")
  containers: Container[]

  @func()
  report: ScanReport

  constructor(containers: Container[], report: ScanReport) {
    this.containers = containers
    this.report = report
  }
}

@object()
export class Test {
  @func()
  async scan(): Promise<ScanResult> {
    return new ScanResult(
      [
        dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
      ],
      new ScanReport("hello world", ["foo", "bar"])
    )
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{scan{targets{stdout},report{contents,authors}}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"scan":{"targets":[{"stdout":"hello world\n"}],"report":{"contents":"hello world","authors":["foo","bar"]}}}}`, out)
		})
	}
}

func (TypeSuite) TestIDableType(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct {
	Data string
}

func (m *Test) Set(data string) *Test {
	m.Data = data
	return m
}

func (m *Test) Get() string {
	return m.Data
}
`,
		},
		{
			sdk: "python",
			source: `from typing import Self

import dagger

@dagger.object_type
class Test:
    data: str = ""

    @dagger.function
    def set(self, data: str) -> Self:
        self.data = data
        return self

    @dagger.function
    def get(self) -> str:
        return self.data
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  data: string = ""

  @func()
  set(data: string): Test {
    this.data = data
    return this
  }

  @func()
  get(): string {
    return this.data
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, tc.sdk, tc.source)

			// sanity check
			out, err := modGen.With(daggerQuery(`{test{set(data: "abc"){get}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"set":{"get": "abc"}}}`, out)

			out, err = modGen.With(daggerQuery(`{test{set(data: "abc"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "test.set.id").String()

			var idp call.ID
			err = idp.Decode(id)
			require.NoError(t, err)
			require.Equal(t, idp.Display(), `test.set(data: "abc"): Test!`)

			out, err = modGen.With(daggerQuery(`{loadTestFromID(id: "%s"){get}}`, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"loadTestFromID":{"get": "abc"}}`, out)
		})
	}
}

func (TypeSuite) TestArgOwnType(ctx context.Context, t *testctx.T) {
	// Verify use of a module's own object as an argument type.
	// The server needs to specifically decode the input type from an ID into
	// the raw JSON, since the module doesn't understand it's own types as IDs

	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "strings"

type Test struct{}

type Message struct {
	Content string
}

func (m *Test) SayHello(name string) Message {
	return Message{Content: "hello " + name}
}

func (m *Test) Upper(msg Message) Message {
	msg.Content = strings.ToUpper(msg.Content)
	return msg
}

func (m *Test) Uppers(msg []Message) []Message {
	for i := range msg {
		msg[i].Content = strings.ToUpper(msg[i].Content)
	}
	return msg
}`,
		},
		{
			sdk: "python",
			source: `import dagger

@dagger.object_type
class Message:
    content: str = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def say_hello(self, name: str) -> Message:
        return Message(content=f"hello {name}")

    @dagger.function
    def upper(self, msg: Message) -> Message:
        msg.content = msg.content.upper()
        return msg

    @dagger.function
    def uppers(self, msg: list[Message]) -> list[Message]:
        for m in msg:
            m.content = m.content.upper()
        return msg
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Message {
  @func()
  content: string

  constructor(content: string) {
    this.content = content
  }
}

@object()
export class Test {
  @func()
  sayHello(name: string): Message {
    return new Message("hello " + name)
  }

  @func()
  upper(msg: Message): Message {
    msg.content = msg.content.toUpperCase()
    return msg
  }

  @func()
  uppers(msg: Message[]): Message[] {
    for (let i = 0; i < msg.length; i++) {
      msg[i].content = msg[i].content.toUpperCase()
    }
    return msg
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{test{sayHello(name: "world"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "test.sayHello.id").String()
			var idp call.ID
			err = idp.Decode(id)
			require.NoError(t, err)
			require.Equal(t, idp.Display(), `test.sayHello(name: "world"): TestMessage!`)

			out, err = modGen.With(daggerQuery(`{test{upper(msg:"%s"){content}}}`, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"upper":{"content": "HELLO WORLD"}}}`, out)

			out, err = modGen.With(daggerQuery(`{test{uppers(msg:["%s", "%s"]){content}}}`, id, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"uppers":[{"content": "HELLO WORLD"}, {"content": "HELLO WORLD"}]}}`, out)
		})
	}
}

func (TypeSuite) TestArgNull(ctx context.Context, t *testctx.T) {
	src := `package main

import "strings"

type Test struct {}

func (m *Test) UpperOpt(
	a string, // +optional
) string {
	return strings.ToUpper(a)
}

func (m *Test) UpperReq(
	a string,
) string {
	return strings.ToUpper(a)
}
`

	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))
	modGen := modInit(t, c, "go", src)

	out, err := modGen.With(daggerQuery(`{test{upperOpt(a: null)}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"upperOpt":""}}`, out)

	_, err = modGen.With(daggerQuery(`{test{upperReq(a: null)}}`)).Stdout(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Contains(t, logs.String(), "cannot create String from <nil>")
}

func (TypeSuite) TestScalarType(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatform(platform dagger.Platform) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) dagger.Platform {
	return dagger.Platform(platform)
}

func (m *Test) FromPlatforms(platform []dagger.Platform) []string {
	result := []string{}
	for _, p := range platform {
		result = append(result, string(p))
	}
	return result
}

func (m *Test) ToPlatforms(platform []string) []dagger.Platform {
	result := []dagger.Platform{}
	for _, p := range platform {
		result = append(result, dagger.Platform(p))
	}
	return result
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
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
`,
		},
		{
			sdk: "typescript",
			source: `import { object, func, Platform } from "@dagger.io/dagger"

@object()
export class Test {
	@func()
	fromPlatform(platform: Platform): string {
		return platform as string
	}

	@func()
	toPlatform(platform: string): Platform {
		return platform as Platform
	}

	@func()
	fromPlatforms(platform: Platform[]): string[] {
		return platform.map(p => p as string)
	}

	@func()
	toPlatforms(platform: string[]): Platform[] {
		return platform.map(p => p as Platform)
	}
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{fromPlatform(platform: "linux/amd64")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.fromPlatform").String())
			_, err = modGen.With(daggerQuery(`{test{fromPlatform(platform: "invalid")}}`)).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{toPlatform(platform: "linux/amd64")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.toPlatform").String())
			_, err = modGen.With(daggerQuery(`{test{toPlatform(platform: "invalid")}}`)).Sync(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{fromPlatforms(platform: ["linux/amd64"])}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, len(gjson.Get(out, "test.fromPlatforms").Array()))
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.fromPlatforms.0").String())
			_, err = modGen.With(daggerQuery(`{test{fromPlatforms(platform: ["invalid"])}}`)).Stdout(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{toPlatforms(platform: ["linux/amd64"])}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, len(gjson.Get(out, "test.toPlatforms.0").Array()))
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.toPlatforms.0").String())
			_, err = modGen.With(daggerQuery(`{test{toPlatforms(platform: ["invalid"])}}`)).Sync(ctx)
			requireErrOut(t, err, "unknown operating system or architecture")
		})
	}
}

func (TypeSuite) TestEnumType(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromProto(proto dagger.NetworkProtocol) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) dagger.NetworkProtocol {
	return dagger.NetworkProtocol(proto)
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_proto(self, proto: dagger.NetworkProtocol) -> str:
        return str(proto)

    @function
    def to_proto(self, proto: str) -> dagger.NetworkProtocol:
        # Doing "dagger.NetworkProtocol(proto)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        from dagger.client.base import Enum

        class MockEnum(Enum):
            TCP = "TCP"
            INVALID = "INVALID"

        return MockEnum(proto)
`,
		},
		{
			sdk: "typescript",
			source: `import { object, func, NetworkProtocol } from "@dagger.io/dagger";

@object()
export class Test {
  @func()
  fromProto(Proto: NetworkProtocol): string {
    return Proto as string;
  }

  @func()
  toProto(Proto: string): NetworkProtocol {
    return Proto as NetworkProtocol;
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{fromProto(proto: "TCP")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", gjson.Get(out, "test.fromProto").String())

			_, err = modGen.With(daggerQuery(`{test{fromProto(proto: "INVALID")}}`)).Stdout(ctx)
			requireErrOut(t, err, "invalid enum value")

			out, err = modGen.With(daggerQuery(`{test{toProto(proto: "TCP")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", gjson.Get(out, "test.toProto").String())

			_, err = modGen.With(daggerQuery(`{test{toProto(proto: "INVALID")}}`)).Sync(ctx)
			requireErrOut(t, err, "invalid enum value")
		})
	}
}

func (TypeSuite) TestCustomEnumType(ctx context.Context, t *testctx.T) {
	t.Run("custom enum type", func(ctx context.Context, t *testctx.T) {
		type testCase struct {
			sdk    string
			source string
		}
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

// Enum for Status
type Status string

const (
	// Active status
	Active Status = "ACTIVE"

	// Inactive status
	Inactive Status = "INACTIVE"
)

func New(
	// +default="INACTIVE"
	status Status,
) *Test {
	return &Test{Status: status}
}

type Test struct {
	Status Status
}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) FromStatusOpt(
	// +optional
	status Status,
) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
`,
			},
			{
				sdk: "python",
				source: `import dagger

@dagger.enum_type
class Status(dagger.Enum):
    """Enum for Status"""

    ACTIVE = "ACTIVE", "Active status"
    INACTIVE = "INACTIVE", "Inactive status"


@dagger.object_type
class Test:
    status: Status = dagger.field(default=Status.INACTIVE)

    @dagger.function
    def from_status(self, status: Status) -> str:
        return str(status)

    @dagger.function
    def from_status_opt(self, status: Status | None) -> str:
        return str(status) if status else ""

    @dagger.function
    def to_status(self, status: str) -> Status:
        # Doing "Status(proto)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        class MockEnum(dagger.Enum):
            INACTIVE = "INACTIVE"
            INVALID = "INVALID"

        return MockEnum(status)
`,
			},
			{
				sdk: "typescript",
				source: `import { func, object, enumType } from "@dagger.io/dagger"

/**
 * Enum for Status
 */
@enumType()
class Status {
  /**
   * Active status
   */
  static readonly Active: string = "ACTIVE"

  /**
   * Inactive status
   */
  static readonly Inactive: string = "INACTIVE"
}

@object()
export class Test {
  @func()
  status: Status

  // FIXME: this should be Status.Inactive instead of "INACTIVE"
  constructor(status: Status = "INACTIVE") {
    this.status = status
  }

  @func()
  fromStatus(status: Status): string {
    return status as string
  }

  @func()
  fromStatusOpt(status?: Status): string {
	if (status) {
		return status as string
	}
    return ""
  }

  @func()
  toStatus(status: string): Status {
    return status as Status
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				modGen := modInit(t, c, tc.sdk, tc.source)

				// fromStatus
				out, err := modGen.With(daggerQuery(`{test{fromStatus(status: "ACTIVE")}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "ACTIVE", gjson.Get(out, "test.fromStatus").String())

				out, err = modGen.With(daggerQuery(`{test{status}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "INACTIVE", gjson.Get(out, "test.status").String())

				_, err = modGen.With(daggerQuery(`{test{fromStatus(status: "INVALID")}}`)).Stdout(ctx)
				requireErrOut(t, err, "invalid enum value")

				// fromStatusOpt
				out, err = modGen.With(daggerQuery(`{test{fromStatusOpt}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "", gjson.Get(out, "test.fromStatusOpt").String())

				out, err = modGen.With(daggerQuery(`{test{fromStatusOpt(status: "ACTIVE")}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "ACTIVE", gjson.Get(out, "test.fromStatusOpt").String())

				_, err = modGen.With(daggerQuery(`{test{fromStatusOpt(status: "INVALID")}}`)).Stdout(ctx)
				requireErrOut(t, err, "invalid enum value")

				// toStatus
				out, err = modGen.With(daggerQuery(`{test{toStatus(status: "INACTIVE")}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "INACTIVE", gjson.Get(out, "test.toStatus").String())

				_, err = modGen.With(daggerQuery(`{test{toStatus(status: "INVALID")}}`)).Sync(ctx)
				requireErrOut(t, err, "invalid enum value")

				// introspection
				mod := inspectModule(ctx, t, modGen)
				statusEnum := mod.Get("enums.#.asEnum|#(name=TestStatus)")
				require.Equal(t, "Enum for Status", statusEnum.Get("description").String())
				require.Len(t, statusEnum.Get("values").Array(), 2)
				require.Equal(t, "ACTIVE", statusEnum.Get("values.0.name").String())
				require.Equal(t, "INACTIVE", statusEnum.Get("values.1.name").String())
				require.Equal(t, "Active status", statusEnum.Get("values.0.description").String())
				require.Equal(t, "Inactive status", statusEnum.Get("values.1.description").String())
			})
		}
	})

	t.Run("custom external enum type", func(ctx context.Context, t *testctx.T) {
		depSrc := `package main

// Enum for Status
type Status string

const (
	// Active status
	Active Status = "ACTIVE"

	// Inactive status
	Inactive Status = "INACTIVE"
)

type Dep struct{}

func (m *Dep) Active() Status {
	return Active
}

func (m *Dep) Inactive() Status {
	return Inactive
}

func (m *Dep) Invert(status Status) Status {
	switch status {
	case Active:
		return Inactive
	case Inactive:
		return Active
	default:
		panic("invalid status")
	}
}
`

		type testCase struct {
			sdk    string
			source string
		}
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Active() (string) {
	return string(dagger.DepStatusActive)
}

func (m *Test) Inactive(ctx context.Context) (string, error) {
	status, err := dag.Dep().Active(ctx)
	if err != nil {
		return "", err
	}
	status, err = dag.Dep().Invert(ctx, status)
	if err != nil {
		return "", err
	}
	return string(status), nil
}
`,
			},
			{
				sdk: "python",
				source: `import dagger
from dagger import dag, DepStatus

@dagger.object_type
class Test:
    @dagger.function
    def active(self) -> str:
        return str(DepStatus.ACTIVE)

    @dagger.function
    async def inactive(self) -> str:
        status = await dag.dep().active()
        status = await dag.dep().invert(status)
        return str(status)
`,
			},
			{
				sdk: "typescript",
				source: `import { dag, func, object, DepStatus } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  active(): string {
    return DepStatus.Active;
  }

  @func()
  async inactive(): Promise<string> {
    let status = await dag.dep().active();
    status = await dag.dep().invert(status);
    return status;
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := modInit(t, c, tc.sdk, tc.source).
					With(withModInitAt("./dep", "go", depSrc)).
					With(daggerExec("install", "./dep"))

				out, err := modGen.With(daggerQuery(`{test{active inactive}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "ACTIVE", gjson.Get(out, "test.active").String())
				require.Equal(t, "INACTIVE", gjson.Get(out, "test.inactive").String())
			})
		}
	})

	t.Run("custom conflicting enum type", func(ctx context.Context, t *testctx.T) {
		depSrc := `package main

type Dep struct{}

type MyEnum string

const (
	MyEnumFalse MyEnum = "false"
	MyEnumTrue  MyEnum = "true"
	MyEnumNull  MyEnum = "null"
)

func (m *Dep) Thing(f MyEnum) MyEnum {
	return f
}
`

		src := `package main

import (
	"context"
	"fmt"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestBool(ctx context.Context) (string, error) {
	f, err := dag.Dep().Thing(ctx, dagger.DepMyEnumFalse)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(f), nil
}

func (m *Test) TestNull(ctx context.Context) (string, error) {
	f, err := dag.Dep().Thing(ctx, dagger.DepMyEnumNull)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(f), nil
}
`

		t.Run("bool", func(ctx context.Context, t *testctx.T) {
			var logs safeBuffer
			c := connect(ctx, t, dagger.WithLogOutput(&logs))

			modGen := modInit(t, c, "go", src).
				With(withModInitAt("./dep", "go", depSrc)).
				With(daggerExec("install", "./dep"))

			_, err := modGen.With(daggerQuery(`{test{testBool}}`)).Stdout(ctx)
			require.Error(t, err)
			require.NoError(t, c.Close())
			require.Contains(t, logs.String(), "invalid enum value false")
		})

		t.Run("null", func(ctx context.Context, t *testctx.T) {
			var logs safeBuffer
			c := connect(ctx, t, dagger.WithLogOutput(&logs))

			modGen := modInit(t, c, "go", src).
				With(withModInitAt("./dep", "go", depSrc)).
				With(daggerExec("install", "./dep"))

			_, err := modGen.With(daggerQuery(`{test{testNull}}`)).Stdout(ctx)
			require.Error(t, err)
			require.NoError(t, c.Close())
			require.Contains(t, logs.String(), "invalid enum value null")
		})
	})
}
