package com.mycompany.app

import io.dagger.client.Container
import io.dagger.client.Dagger.dag
import io.dagger.client.Directory
import io.dagger.client.ReturnType
import java.util.concurrent.atomic.AtomicBoolean

object Test {
    @JvmStatic
    fun main(args: Array<String>) {
        try {
            // Get reference to the local project
            val src: Directory = dag().host().directory(".")

            val hasFailure = AtomicBoolean(false)

            listOf(17, 21, 23).forEach { javaVersion ->
                val gradle: Container = dag()
                    .container()
                    .from("gradle:jdk$javaVersion")
                    .withDirectory("/src", src)
                    .withExec(
                        listOf("gradle", "test"),
                        Container.WithExecArguments().withExpect(ReturnType.ANY) // Do not fail on errors
                    )

                val output = gradle.stdout()
                println("Output for JDK $javaVersion:\n$output")

                if (!gradle.exitCode().equals(0)) {
                    hasFailure.set(true)
                }
            }

            if (hasFailure.get()) {
                throw RuntimeException("Some tests failed")
            }
        } finally {
            dag().close()
        }
    }
}
