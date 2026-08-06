/**
 * A basic coding agent, for @agent integration tests.
 */
import { LLM, agent, dag, func, object } from "@dagger.io/dagger";

@object()
class EditorTs {
  /**
   * Compose the editor's tools onto a base LLM. Base named `base`.
   */
  @func()
  @agent()
  agent(base: LLM): LLM {
    return base
      .withTools(dag.currentNode())
      .withSystemPrompt("editor-ts agent system prompt");
  }

  /**
   * Read a file (stub).
   */
  @func()
  readFileTs(path: string): string {
    return `read ${path}`;
  }
}
