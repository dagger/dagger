package io.dagger.client;

import java.util.Map;

/** A GraphQL input object, implemented by generated input types in their own packages. */
public interface InputValue {
  Map<String, Object> toMap();
}
