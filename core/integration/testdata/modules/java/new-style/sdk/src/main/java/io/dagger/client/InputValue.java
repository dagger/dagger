package io.dagger.client;

import java.util.Map;

interface InputValue {
  Map<String, Object> toMap();
}
