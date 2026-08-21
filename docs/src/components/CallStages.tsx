import React, { useState } from "react";
import styles from "../css/stageWalk.module.scss";

type StageId = "start" | "generate" | "call";

const STAGES: {
  id: StageId;
  title: string;
  kicker: string;
  mark?: string;
  tone: "engine" | "helper" | "call";
}[] = [
  { id: "start", title: "Engine", kicker: "already on", tone: "engine" },
  {
    id: "generate",
    title: "Generate",
    kicker: "writes SDK files",
    mark: "go get",
    tone: "helper",
  },
  { id: "call", title: "Call", kicker: "build and run", tone: "call" },
];

const DETAIL: Record<StageId, string> = {
  start: "No Go download happens here.",
  generate:
    "This writes the SDK files. If the cache is empty, go get runs here.",
  call: "Asking for the list and running the function are the same process. A first build can still download if the cache is empty.",
};

const TONE = {
  engine: styles.engine,
  helper: styles.helper,
  call: styles.call,
};

const TONE_LIT = {
  engine: styles.engineLit,
  helper: styles.helperLit,
  call: styles.callLit,
};

export default function CallStages() {
  const [id, setId] = useState<StageId | null>(null);

  return (
    <div className={styles.wrap}>
      <div className={styles.scene}>
        <div className={styles.timeline} role="group" aria-label="When Go downloads happen">
          {STAGES.map((s, i) => (
            <React.Fragment key={s.id}>
              {i > 0 ? (
                <span className={`${styles.tlArrow} ${styles.wireOn}`} aria-hidden="true" />
              ) : null}
              <button
                type="button"
                aria-pressed={id === s.id}
                className={`${styles.tlBox} ${TONE[s.tone]} ${id === s.id ? TONE_LIT[s.tone] : ""}`}
                onClick={() => setId((cur) => (cur === s.id ? null : s.id))}
              >
                <span className={styles.kicker}>{s.kicker}</span>
                <span className={styles.title}>{s.title}</span>
                {s.mark ? (
                  <span className={`${styles.chip} ${styles.chipWork}`}>{s.mark}</span>
                ) : null}
              </button>
            </React.Fragment>
          ))}
        </div>
      </div>
      {id ? <p className={styles.caption}>{DETAIL[id]}</p> : null}
    </div>
  );
}
