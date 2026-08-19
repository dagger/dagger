import React, { useState } from "react";
import styles from "../css/stageWalk.module.scss";

type Phase = "you" | "engine" | "helper" | "wires" | "fetch" | "function";

type PhaseSpec = {
  id: Phase;
  label: string;
  shows: string;
  running: string;
  say: string;
  laptop: string;
  engine: string;
  helper: string;
};

const PHASES: PhaseSpec[] = [
  {
    id: "you",
    label: "You type dagger",
    shows: "You asked Dagger to do something. That is all this step is.",
    running: "Your machine. The engine from last time may already be running.",
    say: "Typing the command is not the same as your function running.",
    laptop: "You, your shell, git, passwords, and an SSH agent if you have one.",
    engine: "Already on. It does not see this shell.",
    helper: "Not started.",
  },
  {
    id: "engine",
    label: "The engine",
    shows:
      "The engine is a long-lived container. It stays on between commands. It is already running.",
    running: "One container. Not your pipeline. Just the engine.",
    say: "The engine was already running. This command did not start it.",
    laptop: "Still has your shell, git, passwords, and SSH agent.",
    engine:
      "The container that stays on. A proxy name only lives here if you set it on this container.",
    helper: "Not started.",
  },
  {
    id: "helper",
    label: "A helper starts",
    shows:
      "The engine opens another container. That helper is short-lived. It exists to get the module ready.",
    running:
      "The helper. It does not have your laptop environment yet. Your function is not a command yet.",
    say: "This is still setup. The TUI may look busy. You have not reached your code.",
    laptop: "Unchanged. Nothing moved just because the helper exists.",
    engine: "Still on. It started the helper. It is not the helper.",
    helper: "A new container. No git from your host. No password store. No shell exports.",
  },
  {
    id: "wires",
    label: "What gets copied in",
    shows:
      "The helper does not inherit your laptop. Dagger only copies named settings, if it can find them.",
    running: "Still setup. Downloads have not finished. There is no function list yet.",
    say: "This is the step people treat as already configured. They configured the laptop.",
    laptop:
      "SSH is a path to an agent on this machine. Git and the password store stay here. A proxy in the shell stays here.",
    engine:
      "If a proxy name was put on this container, this is where it sits. The helper can ask the engine for it. The helper cannot ask your shell.",
    helper:
      "May get an SSH socket if that path was in the session. May get a proxy if the engine had it. Does not get the host password store. Does not get the git program on your machine.",
  },
  {
    id: "fetch",
    label: "Download packages and build the list",
    shows:
      "The helper downloads packages and builds the list of functions. It uses only what it actually has.",
    running:
      "The helper is fetching. Until this list exists, Dagger cannot offer your function as a command.",
    say: "If a download fails here, it failed before your pipeline ran.",
    laptop: "Still on the host. The helper is not reading it anymore.",
    engine: "Still on. Waiting for the list.",
    helper:
      "This is the fetch. Without a proxy, it asks the default host. Without git login, a private fetch fails. Without a live SSH path, wiring fails. Without git in the helper image, a git line in your module fails.",
  },
  {
    id: "function",
    label: "Your function",
    shows:
      "This box stays empty until the list exists. Then it can become a command. Not before.",
    running: "If the list is not done, still the helper. Your function has not started.",
    say: "The busy TUI was prepare. This is the thing you thought you already ran.",
    laptop: "You are still on the laptop. That was never the run.",
    engine: "Ready to call a function once it has the list.",
    helper: "Done, or stuck. Either way, this was never your function.",
  },
];

export default function HelperPrepareWalk() {
  const [phaseId, setPhaseId] = useState<Phase>("you");
  const phase = PHASES.find((p) => p.id === phaseId) ?? PHASES[0];
  const helperVisible =
    phaseId === "helper" ||
    phaseId === "wires" ||
    phaseId === "fetch" ||
    phaseId === "function";
  const helperActive =
    phaseId === "helper" || phaseId === "wires" || phaseId === "fetch";

  return (
    <div className={styles.wrap}>
      <p className={styles.intro}>
        Click each prepare step. Watch where things sit. Your laptop is not the
        helper.
      </p>
      <div className={styles.pills}>
        {PHASES.map((p) => (
          <button
            key={p.id}
            type="button"
            className={`${styles.pill} ${phaseId === p.id ? styles.pillActive : ""}`}
            onClick={() => setPhaseId(p.id)}
          >
            {p.label}
          </button>
        ))}
      </div>
      <div className={styles.diagram}>
        <div className={styles.col}>
          <div
            className={`${styles.box} ${phaseId === "you" ? styles.boxActive : ""}`}
          >
            <span className={styles.label}>host</span>
            <p className={styles.title}>You</p>
            <p className={styles.body}>{phase.laptop}</p>
          </div>
        </div>
        <div className={styles.col}>
          <div
            className={`${styles.box} ${phaseId === "engine" ? styles.boxActive : ""}`}
          >
            <span className={styles.label}>long-lived container</span>
            <p className={styles.title}>Engine</p>
            <p className={styles.body}>{phase.engine}</p>
          </div>
          <div
            className={`${styles.box} ${
              !helperVisible
                ? styles.boxDashed
                : helperActive
                  ? styles.boxActive
                  : ""
            }`}
          >
            <span className={styles.label}>short-lived container</span>
            <p className={styles.title}>Helper</p>
            <p className={styles.body}>
              {helperVisible ? phase.helper : "No helper yet."}
            </p>
          </div>
        </div>
        <div className={styles.col}>
          <div
            className={`${styles.box} ${styles.boxDashed} ${
              phaseId === "function" ? styles.boxActive : ""
            }`}
          >
            <span className={styles.label}>not yet</span>
            <p className={styles.title}>Your function</p>
            <p className={styles.body}>Not a command yet.</p>
          </div>
        </div>
      </div>
      <div className={styles.section}>
        <h3>Where it sits</h3>
        <p>
          <strong>Laptop:</strong> {phase.laptop}
        </p>
        <p>
          <strong>Engine:</strong> {phase.engine}
        </p>
        <p>
          <strong>Helper:</strong> {phase.helper}
        </p>
      </div>
      <div className={styles.section}>
        <h3>What this step does</h3>
        <p>{phase.shows}</p>
      </div>
      <div className={styles.section}>
        <h3>What is running</h3>
        <p>{phase.running}</p>
      </div>
      <p className={styles.note}>{phase.say}</p>
    </div>
  );
}
