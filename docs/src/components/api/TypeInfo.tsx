import React, { useEffect, useId, useRef, useState } from "react";
import type { InputType, NamedKind, TypeRef } from "./data";
import { useApiModel } from "./data";
import { MarkdownInline } from "./Markdown";
import styles from "./styles.module.scss";

function PlainTypeRef({ type }: { type: TypeRef }): JSX.Element {
  switch (type.kind) {
    case "nonNull":
      return (
        <>
          <PlainTypeRef type={type.of} />
          <span className={styles.punct}>!</span>
        </>
      );
    case "list":
      return (
        <>
          <span className={styles.punct}>[</span>
          <PlainTypeRef type={type.of} />
          <span className={styles.punct}>]</span>
        </>
      );
    case "named":
      return (
        <span className={styles.typeName} data-kind={type.named}>
          {type.name}
        </span>
      );
  }
}

function InputSchema({ input }: { input: InputType }): JSX.Element {
  return (
    <>
      {input.description && (
        <span className={styles.typePopoverDesc}>
          <MarkdownInline>{input.description}</MarkdownInline>
        </span>
      )}
      <span className={styles.inputSchema}>
        {input.fields.map((field) => (
          <span className={styles.inputField} key={field.name}>
            <span className={styles.popoverCode}>
              <span className={styles.argName}>{field.name}</span>
              <span className={styles.punct}>: </span>
              <PlainTypeRef type={field.type} />
              {field.defaultValue !== undefined && (
                <span className={styles.literal}> = {field.defaultValue}</span>
              )}
            </span>
            {field.description && (
              <span className={styles.typePopoverDesc}>
                <MarkdownInline>{field.description}</MarkdownInline>
              </span>
            )}
          </span>
        ))}
      </span>
    </>
  );
}

export default function TypeInfo({
  name,
  kind,
}: {
  name: string;
  kind: NamedKind;
}): JSX.Element | null {
  const model = useApiModel();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLSpanElement>(null);
  const popoverId = useId();

  const enumType = kind === "enum" ? model.enums[name] : undefined;
  const scalar = kind === "scalar" ? model.scalars[name] : undefined;
  const input = kind === "input" ? model.inputs[name] : undefined;
  const hasInfo =
    Boolean(enumType?.values.length) ||
    Boolean(scalar?.description) ||
    Boolean(input?.description || input?.fields.length);

  useEffect(() => {
    if (!hasInfo) return;
    const closeIfOutside = (event: PointerEvent) => {
      const wrap = wrapRef.current;
      if (!wrap || !open) return;
      if (event.target instanceof Node && wrap.contains(event.target)) return;
      setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };

    document.addEventListener("pointerdown", closeIfOutside);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeIfOutside);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [hasInfo, open]);

  if (!hasInfo) return null;

  let label = "info";
  if (enumType) {
    label = `${enumType.values.length} option${
      enumType.values.length === 1 ? "" : "s"
    }`;
  } else if (input) {
    label = "schema";
  }

  return (
    <span
      className={styles.typeInfo}
      data-kind={kind}
      data-open={open ? "true" : "false"}
      ref={wrapRef}
    >
      <button
        type="button"
        className={styles.typeInfoButton}
        aria-expanded={open}
        aria-controls={popoverId}
        onClick={() => setOpen((v) => !v)}
      >
        {label}
      </button>
      {open && (
        <span
          id={popoverId}
          className={styles.typePopover}
          role="dialog"
          aria-label={`${name} ${label}`}
        >
          {enumType && (
            <span className={styles.typeValueList}>
              {enumType.values.map((value) => (
                <span className={styles.typeValue} key={value.name}>
                  <span className={styles.popoverCode}>{value.name}</span>
                  {value.description && (
                    <span className={styles.typePopoverDesc}>
                      {" - "}
                      <MarkdownInline>{value.description}</MarkdownInline>
                    </span>
                  )}
                </span>
              ))}
            </span>
          )}
          {scalar?.description && (
            <span className={styles.typePopoverDesc}>
              <MarkdownInline>{scalar.description}</MarkdownInline>
            </span>
          )}
          {input && <InputSchema input={input} />}
        </span>
      )}
    </span>
  );
}
