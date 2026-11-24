package io.dagger.client;

import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.graphql.GraphQLValues;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ExecutionException;
import java.util.stream.Collectors;

public class Arguments {

  private Map<String, Object> args;

  static Arguments noArgs() {
    return new Arguments();
  }

  public static Builder newBuilder() {
    return new Arguments().new Builder();
  }

  private Arguments() {
    this(new HashMap<>());
  }

  private Arguments(Map<String, Object> args) {
    this.args = args;
  }

  private Builder builder() {
    return new Builder();
  }

  public Arguments merge(Arguments other) {
    HashMap<String, Object> newMap = new HashMap<>(this.args);
    newMap.putAll(other.args);
    return new Arguments(newMap);
  }

  String toGraphQL() throws ExecutionException, InterruptedException, DaggerQueryException {
    List<String> rendered = new ArrayList<>();
    for (Map.Entry<String, Object> entry : args.entrySet()) {
      rendered.add(entry.getKey() + ":" + GraphQLValues.format(toArgumentValue(entry.getValue())));
    }
    return rendered.stream().collect(Collectors.joining(","));
  }

  private Object toArgumentValue(Object value)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    if (value == null) {
      return null;
    } else if (value instanceof Scalar<?>) {
      return ((Scalar<?>) value).convert();
    } else if (value instanceof IDAble<?>) {
      Object id = ((IDAble<?>) value).id();
      if (id instanceof Scalar<?>) {
        return ((Scalar<?>) id).convert();
      } else {
        return id;
      }
    } else if (value instanceof InputValue) {
      Map<String, Object> normalized = new LinkedHashMap<>();
      for (Map.Entry<String, Object> e : ((InputValue) value).toMap().entrySet()) {
        normalized.put(e.getKey(), toArgumentValue(e.getValue()));
      }
      return normalized;
    } else if (value instanceof String
        || value instanceof Integer
        || value instanceof Long
        || value instanceof Float
        || value instanceof Boolean) {
      return value;
    } else if (value instanceof List<?>) {
      List list = new ArrayList();
      for (Object o : (List) value) {
        list.add(toArgumentValue(o));
      }
      return list;
    } else if (value instanceof GraphQLEnum) {
      // Constants named apart from their schema values go as their values.
      return ((GraphQLEnum) value).graphQLValue();
    } else if (value instanceof Enum<?>) {
      return ((Enum<?>) value).toString();
    } else {
      throw new IllegalStateException(
          String.format(
              "Argument is not an authorized argument type. Found type is %s", value.getClass()));
    }
  }

  public class Builder {
    private Builder() {}

    public Builder add(String name, String value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, String... value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, boolean value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, int value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, Float value) {
      args.put(name, value);
      return this;
    }

    public <T> Builder add(String name, Scalar<T> value) {
      args.put(name, value);
      return this;
    }

    public <T extends Scalar<?>> Builder add(String name, IDAble<T> value) {
      args.put(name, value);
      return this;
    }

    public <T> Builder add(String name, List<T> value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, InputValue value) {
      args.put(name, value);
      return this;
    }

    public Builder add(String name, Enum value) {
      args.put(name, value);
      return this;
    }

    public Arguments build() {
      return Arguments.this;
    }
  }
}
