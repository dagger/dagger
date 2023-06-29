package org.chelonix.dagger.client;

import io.smallrye.graphql.client.core.Argument;
import org.junit.jupiter.api.Test;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

public class ArgumentsTest {

    public static class SimpleId extends Scalar<String> {
        public SimpleId(String value) {
            super(value);
        }
    }

    public static enum SimpleEnum {
        ENUM1, ENUM2, ENUM3
    }

    @Test
    public void testNullArguments() throws Exception {
        List<Argument> args = Arguments.newBuilder().add("foo", (String) null).build().toList();
        assertThat(args.get(0).build()).isEqualTo("foo:null");
    }

    @Test
    public void testEmptyArguments() throws Exception {
        List<Argument> args = Arguments.noArgs().toList();
        assertThat(args).isEmpty();
    }

    @Test
    public void testStringArgument() throws Exception {
        List<Argument> args = Arguments.newBuilder().add("foo", "bar").build().toList();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("foo:\"bar\"");
    }

    @Test
    public void testIntArgument() throws Exception {
        List<Argument> args = Arguments.newBuilder().add("foo", 1234).build().toList();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("foo:1234");
    }

    @Test
    public void testScalarArgument() throws Exception {
        List<Argument> args = Arguments.newBuilder().add("foo", new SimpleId("bar")).build().toList();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("foo:\"bar\"");
    }

    @Test
    public void testEnumArgument() throws Exception {
        List<Argument> args = Arguments.newBuilder().add("foo", SimpleEnum.ENUM2).build().toList();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("foo:\"ENUM2\"");
    }

    @Test
    public void testIdArgument() throws Exception {
        IdProvider<SimpleId> idProvider = mock(IdProvider.class);
        SimpleId id = new SimpleId("baz");
        when(idProvider.id()).thenReturn(id);
        List<Argument> args = Arguments.newBuilder().add("bar", idProvider).build().toList();
        verify(idProvider).id();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("bar:\"baz\"");
    }

    @Test
    public void testInputValueArgument() throws Exception {
        InputValue inputValue = mock(InputValue.class);
        Map map = new HashMap<String, Object>() {{
            put("foo", "bar");
            put("bar", 1234);
            put("baz", true);
        }};
        when(inputValue.toMap()).thenReturn(map);
        List<Argument> args = Arguments.newBuilder().add("obj", inputValue).build().toList();
        verify(inputValue).toMap();
        assertThat(args).hasSize(1);
        assertThat(args.get(0).build()).isEqualTo("obj:{bar:1234, foo:\"bar\", baz:true}");
    }
}

