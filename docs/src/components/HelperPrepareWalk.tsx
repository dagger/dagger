import React, { useState } from "react";
import styles from "../css/stageWalk.module.scss";

type Part =
  | "host"
  | "engine"
  | "helper"
  | "function"
  | "cli"
  | "list";

const DETAIL: Record<Part, string> = {
  host: "You typed dagger here. The engine and the helper are containers. They do not use this shell.",
  cli: "The dagger command on this machine talks to the engine. Connecting does not send your shell, git, or GOPROXY into the helper.",
  engine: "Already on. It starts the helper. It does not run the function.",
  helper: "Fetches packages and builds the list. Does not inherit the host.",
  list: "The helper is still listing the functions, so Dagger cannot call yours yet.",
  function: "Empty until the list exists.",
};

function Chip({ children }: { children: string }) {
  return <span className={styles.chip}>{children}</span>;
}

function Wire({
  label,
  kind,
  selected,
  onClick,
}: {
  label: string;
  kind: "on" | "wait";
  selected: boolean;
  onClick: () => void;
}) {
  const kindClass = kind === "on" ? styles.wireOn : styles.wireWait;
  return (
    <button
      type="button"
      aria-pressed={selected}
      className={`${styles.wire} ${styles.wireClick} ${kindClass} ${selected ? styles.wireLit : ""}`}
      onClick={onClick}
    >
      <span className={styles.wireLabel}>{label}</span>
      <span className={styles.wireLine}>
        <span className={styles.wireShaft} />
        <span className={styles.wireHead} />
      </span>
    </button>
  );
}

export default function HelperPrepareWalk() {
  const [part, setPart] = useState<Part | null>(null);
  const toggle = (id: Part) => setPart((cur) => (cur === id ? null : id));

  return (
    <div className={styles.wrap}>
      <div className={styles.scene}>
        <div className={styles.diagram}>
          <button
            type="button"
            aria-pressed={part === "host"}
            className={`${styles.box} ${styles.host} ${styles.boxClick} ${part === "host" ? styles.hostLit : ""}`}
            onClick={() => toggle("host")}
          >
            <span className={styles.kicker}>typed the command</span>
            <p className={styles.title}>Host</p>
            <div className={styles.chips}>
              <Chip>GOPROXY</Chip>
              <Chip>git</Chip>
              <Chip>SSH</Chip>
            </div>
          </button>
          <Wire
            label="CLI"
            kind="on"
            selected={part === "cli"}
            onClick={() => toggle("cli")}
          />
          <div
            className={`${styles.box} ${styles.engine} ${part === "engine" ? styles.engineLit : ""}`}
          >
            <button
              type="button"
              aria-pressed={part === "engine"}
              className={`${styles.boxHit} ${styles.boxClick}`}
              onClick={() => toggle("engine")}
            >
              <span className={styles.kicker}>long-lived</span>
              <p className={styles.title}>
                <span className={styles.liveDot} aria-hidden="true" />
                Engine
              </p>
            </button>
            <span className={styles.starts} aria-hidden="true">
              starts
            </span>
            <button
              type="button"
              aria-pressed={part === "helper"}
              className={`${styles.box} ${styles.helper} ${styles.boxClick} ${part === "helper" ? styles.helperLit : ""}`}
              onClick={() => toggle("helper")}
            >
              <span className={styles.kicker}>short-lived</span>
              <p className={styles.title}>Helper</p>
              <div className={styles.chips}>
                <span className={`${styles.chip} ${styles.chipWork}`}>
                  fetch
                </span>
                <span className={`${styles.chip} ${styles.chipWork}`}>
                  function list
                </span>
              </div>
            </button>
          </div>
          <Wire
            label="function list"
            kind="wait"
            selected={part === "list"}
            onClick={() => toggle("list")}
          />
          <button
            type="button"
            aria-pressed={part === "function"}
            className={`${styles.box} ${styles.fn} ${styles.boxClick} ${part === "function" ? styles.fnLit : ""}`}
            onClick={() => toggle("function")}
          >
            <span className={styles.kicker}>not yet</span>
            <p className={styles.title}>Your function</p>
          </button>
        </div>
      </div>
      {part ? <p className={styles.caption}>{DETAIL[part]}</p> : null}
    </div>
  );
}
