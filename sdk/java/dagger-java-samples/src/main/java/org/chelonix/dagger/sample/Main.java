package org.chelonix.dagger.sample;

import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;

public class Main {

    private static final Class[] SAMPLES = new Class[] {
            RunContainer.class,
            GetDaggerWebsite.class,
            ListEnvVars.class,
            MountHostDirectoryInContainer.class,
            ListHostDirectoryContents.class,
            ReadFileInGitRepository.class,
            GetGitVersion.class,
            CreateAndUseSecret.class,
            TestWithDatabase.class
    };

    public static void main(String... args) {
        System.console().printf("=== Dagger.io Java SDK samples ===\n");
        while (true) {
            for (int i = 0; i < SAMPLES.length; i++) {
                System.console().printf("  %d - %s\n", i+1, SAMPLES[i].getName());
            }
            System.console().printf("  q - exit\n");
            String input = System.console().readLine("\nSelect sample: ");
            try {
                if ("q".equals(input)) {
                    System.exit(0);
                }
                int index = Integer.parseInt(input);
                if (index < 1 || index > SAMPLES.length) {
                    continue;
                }
                Class klass = SAMPLES[index-1];
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
}
