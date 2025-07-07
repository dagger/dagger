package io.dagger.modules.mymodule;

import io.dagger.client.File;
import io.dagger.module.annotation.Object;

@Object
public class TestResult {
  public File report;
  public int exitCode;

  public TestResult() {}

  public TestResult(File report, int exitCode) {
    this.report = report;
    this.exitCode = exitCode;
  }
}
