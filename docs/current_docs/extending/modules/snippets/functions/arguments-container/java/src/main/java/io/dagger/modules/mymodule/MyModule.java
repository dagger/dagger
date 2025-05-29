package io.dagger.modules.mymodule;

import io.dagger.client.Container;
import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.Function;


import io.dagger.module.annotation.Object;

import java.util.List;
import java.util.concurrent.ExecutionException;
 public class MyModule {unction String osInfo(Containerctr)rows ExecutionException,DaggerExecException,DaggerQueryException,InterruptedException {r.withExec(List.of(uname", "-a")).stdou();
