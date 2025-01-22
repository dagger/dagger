package io.dagger.client;

import com.google.gson.Gson;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.concurrent.ExecutionException;

public final class Convert {
  public static JSON toJSON(Object object)
      throws ExecutionException, DaggerQueryException, InterruptedException {
    Gson gson = new Gson();
    String json;
    if (object instanceof Scalar<?>) {
      json = gson.toJson((((Scalar<?>) object).convert()));
    } else if (object instanceof IDAble<?>) {
      var id = ((IDAble<?>) object).id();
      var idStr = ((Scalar<?>) id).convert();
      json = gson.toJson(idStr);
    } else {
      json = gson.toJson(object);
    }
    return JSON.from(json);
  }

  public static <T> T fromJSON(Client dag, JSON json, Class<T> clazz)
      throws ClassNotFoundException,
          InvocationTargetException,
          NoSuchMethodException,
          InstantiationException,
          IllegalAccessException {
    return fromJSON(dag, json.convert(), clazz);
  }

  public static <T> T fromJSON(Client dag, String json, Class<T> clazz)
      throws NoSuchMethodException,
          InvocationTargetException,
          InstantiationException,
          IllegalAccessException,
          ClassNotFoundException {
    Gson gson = new Gson();
    var o = clazz.getDeclaredConstructor().newInstance();
    if (o instanceof Scalar<?>) {
      String jsonString = gson.fromJson(json, String.class);
      Object res = clazz.getMethod("from", String.class).invoke(null, jsonString);
      return (T) res;
    } else if (o instanceof IDAble<?>) {
      String jsonString = gson.fromJson(json, String.class);
      Class<?> idType = Class.forName(clazz.getCanonicalName() + "ID");
      Object id = idType.getMethod("from", String.class).invoke(null, jsonString);
      Method m = Client.class.getMethod("load" + clazz.getSimpleName() + "FromID", idType);
      Object obj = m.invoke(dag, id);
      return (T) obj;
    } else {
      return gson.fromJson(json, clazz);
    }
  }
}
