import React, { useState } from "react";
import styles from "../css/stageWalk.module.scss";

type StageId = "connect" | "load" | "prepare" | "run";

type Stage = {
  id: StageId;
  number: string;
  title: string;
  whatYouSee: string;
  whatDaggerIsDoing: string;
  whyItExists: string;
  note: string;
};

const STAGES: Stage[] = [
  {
    id: "connect",
    number: "1",
    title: "Start Dagger",
    whatYouSee:
      "The CLI connects to the engine. The first start on a machine may pull the engine image. Later calls reuse the engine that is already running.",
    whatDaggerIsDoing:
      "Dagger is a CLI on your machine and an engine in a container. The CLI does not run your pipeline. It starts the engine if needed, then talks to it.",
    whyItExists:
      "The engine holds cache, containers, and modules. A function call needs that engine to be running.",
    note: "This step does not download Go modules. GOPROXY is not used here.",
  },
  {
    id: "load",
    number: "2",
    title: "Load your module",
    whatYouSee:
      "Dagger loads the workspace. It reads your project so it knows the module name and the language.",
    whatDaggerIsDoing:
      "Dagger opens your directory, reads the module config, and copies that source into the engine. It does not have the function list yet.",
    whyItExists:
      "A module is the unit you call. Dagger has to load the files before it can prepare them.",
    note: "A short load means Dagger found your files. The longer wait, if there is one, comes next.",
  },
  {
    id: "prepare",
    number: "3",
    title: "Prepare the module",
    whatYouSee:
      "The TUI says loading type definitions. This is still not your function.",
    whatDaggerIsDoing:
      "Dagger builds the function list so the CLI, the engine, and your language agree on the module API. This step uses a Go helper, even if you wrote Python or TypeScript. That helper downloads Go packages. By default it uses the Go module proxy at proxy.golang.org.",
    whyItExists:
      "Dagger has to generate the module API before you can call a function. That download is setup. You did not ask for it by name.",
    note: "GOPROXY in your shell does not reach this helper. Plain GOPROXY on the engine does not either. Set _DAGGER_ENGINE_SYSTEMENV_GOPROXY on the engine container. If the helper cannot reach the default proxy, the module never finishes loading, and your function never starts.",
  },
  {
    id: "run",
    number: "4",
    title: "Run your function",
    whatYouSee:
      "Your function name, then the work that function does. This is your pipeline.",
    whatDaggerIsDoing:
      "Dagger calls the function you named. Everything before this was starting the engine and preparing the module.",
    whyItExists:
      "This is the call. Prepare has to finish before this stage can run.",
    note: "If prepare did not finish, this stage does not run.",
  },
];

export default function CallStages() {
  const [id, setId] = useState<StageId>("prepare");
  const stage = STAGES.find((s) => s.id === id) ?? STAGES[2];

  return (
    <div className={styles.wrap}>
      <p className={styles.intro}>
        A call has four stages. Go module downloads happen while Dagger
        prepares the module. Not while it connects. Not while your function
        runs. Click a stage.
      </p>
      <div className={styles.stages}>
        {STAGES.map((s) => (
          <button
            key={s.id}
            type="button"
            className={`${styles.stage} ${id === s.id ? styles.stageActive : ""}`}
            onClick={() => setId(s.id)}
          >
            <span className={styles.stageNum}>Stage {s.number}</span>
            {s.title}
          </button>
        ))}
      </div>
      <div className={styles.detail}>
        <h3>
          {stage.number}. {stage.title}
        </h3>
        <dl>
          <dt>What you see</dt>
          <dd>{stage.whatYouSee}</dd>
          <dt>What Dagger is doing</dt>
          <dd>{stage.whatDaggerIsDoing}</dd>
          <dt>Why this stage exists</dt>
          <dd>{stage.whyItExists}</dd>
        </dl>
        <p className={styles.note}>{stage.note}</p>
      </div>
      <div className={styles.section}>
        <h3>What this is showing</h3>
        <p>
          You asked to run a function. Dagger prepares the module first.
          Prepare downloads Go packages. By default that request goes to
          proxy.golang.org. GOPROXY in your shell does not change that. If
          prepare cannot finish, Dagger never runs your function.
        </p>
      </div>
    </div>
  );
}
