import React from "react";
import styles from "./styles.module.scss";

type Variant = "experimental" | "deprecated";

const TOOLTIP: Record<Variant, string> = {
  experimental:
    "Experimental: this API may change or be removed in a future release.",
  deprecated: "Deprecated: avoid in new code; see the note for the replacement.",
};

// Badge renders a small pill for a Dagger schema directive. These are the
// flourish that makes this a Dagger reference and not a generic schema dump:
// @experimental / @deprecated carry a reason straight from the schema.
export default function Badge({
  variant,
  children,
}: {
  variant: Variant;
  children: React.ReactNode;
}): JSX.Element {
  return (
    <span
      className={`${styles.badge} ${styles[`badge-${variant}`]}`}
      title={TOOLTIP[variant]}
    >
      {children}
    </span>
  );
}
