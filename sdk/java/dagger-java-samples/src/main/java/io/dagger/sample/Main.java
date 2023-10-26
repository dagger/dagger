package io.dagger.sample;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.List;

public class Main {

  private static final Class[] SAMPLES =
      new Class[] {
        RunContainer.class,
        GetDaggerWebsite.class,
        ListEnvVars.class,
        MountHostDirectoryInContainer.class,
        ListHostDirectoryContents.class,
        ReadFileInGitRepository.class,
        PublishImage.class,
        BuildFromDockerfile.class,
        CreateAndUseSecret.class,
        TestWithDatabase.class,
        HostToContainerNetworking.class,
        ContainerToHostNetworking.class
      };

  public static void main(String... args) {
    System.console().printf("=== Dagger.io Java SDK samples ===\n");
    while (true) {
      Table table = new Table();
      for (int i = 0; i < SAMPLES.length; i++) {
        Class klass = SAMPLES[i];
        Description description = (Description) klass.getAnnotation(Description.class);
        String str = klass.getName();
        if (description != null) {
          table.add(str, description.value());
        } else {
          table.add(str);
        }
      }
      System.console().printf(table.toString());
      String input = System.console().readLine("\nSelect sample: ");
      try {
        if ("q".equals(input)) {
          System.exit(0);
        }
        int index = Integer.parseInt(input);
        if (index < 1 || index > SAMPLES.length) {
          continue;
        }
        Class klass = SAMPLES[index - 1];
        Method m = klass.getMethod("main", new String[0].getClass());
        m.invoke(klass, new Object[] {new String[0]});
        System.console().printf("\n");
      } catch (NumberFormatException nfe) {
      } catch (NoSuchMethodException e) {
      } catch (InvocationTargetException e) {
      } catch (IllegalAccessException e) {
      }
    }
  }

  private static class CodeSampleEntry {

    private int index;
    private String name;
    private String description;

    public CodeSampleEntry(int index, String name, String description) {
      this.index = index;
      this.name = name;
      this.description = description;
    }

    public int getIndex() {
      return index;
    }

    public String getName() {
      return name;
    }

    public String getDescription() {
      return description;
    }
  }

  private static class Table {

    private int index = 1;

    private List<CodeSampleEntry> entries = new ArrayList<>();

    void add(String name) {
      add(name, "");
    }

    void add(String name, String description) {
      entries.add(new CodeSampleEntry(index++, name, description));
    }

    @Override
    public java.lang.String toString() {
      StringBuilder sb = new StringBuilder();
      int col1MaxSize =
          entries.stream()
              .mapToInt(e -> Integer.toString(e.getIndex(), 10).length())
              .max()
              .getAsInt();
      int col2MaxSize = entries.stream().mapToInt(e -> e.getName().length()).max().getAsInt();
      for (CodeSampleEntry e : entries) {
        String idx = Integer.toString(e.getIndex(), 10);
        sb.append("  ");
        sb.append(" ".repeat(col1MaxSize - idx.length())).append(idx).append("  ");
        String name = e.getName();
        sb.append(name);
        String description = e.getDescription();
        if (description != null) {
          sb.append(" ".repeat(col2MaxSize - name.length()));
          sb.append("  ").append(description);
        }
        sb.append("\n");
      }
      sb.append("  ").append(" ".repeat(col1MaxSize - 1)).append("q  exit").append("\n");
      return sb.toString();
    }
  }
}
