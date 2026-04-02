package io.dagger.java.collection;

import io.dagger.module.annotation.Collection;
import io.dagger.module.annotation.Get;
import io.dagger.module.annotation.Keys;
import io.dagger.module.annotation.Object;
import java.util.List;

@Object
@Collection
public class Collections {
  @Keys public List<String> paths;

  @Get
  public GoTest module(String name) {
    return new GoTest();
  }
}
