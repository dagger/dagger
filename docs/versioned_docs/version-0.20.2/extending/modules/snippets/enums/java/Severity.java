package io.dagger.modules.mymodule;

import io.dagger.module.annotation.Enum;

@Enum
public enum Severity {
  UNKNOWN,
  LOW,
  MEDIUM,
  HIGH,
  CRITICAL
}
