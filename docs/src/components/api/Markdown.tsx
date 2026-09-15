import React from "react";
import Link from "@docusaurus/Link";

// A compact, dependency-free renderer for the short Markdown found in GraphQL
// descriptions: paragraphs, inline `code`, **bold**, and [links](url). The core
// schema's descriptions only use these; anything richer would warrant pulling
// in react-markdown, but this keeps the reference self-contained.

const INLINE = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\([^)]+\))/g;

function renderInline(text: string, keyBase: string): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  INLINE.lastIndex = 0;
  let i = 0;
  while ((m = INLINE.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const tok = m[0];
    const key = `${keyBase}-${i++}`;
    if (tok.startsWith("`")) {
      out.push(<code key={key}>{tok.slice(1, -1)}</code>);
    } else if (tok.startsWith("**")) {
      out.push(<strong key={key}>{tok.slice(2, -2)}</strong>);
    } else {
      const lm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok)!;
      out.push(
        <Link key={key} to={lm[2]}>
          {lm[1]}
        </Link>
      );
    }
    last = m.index + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export default function Markdown({
  children,
  className,
}: {
  children: string;
  className?: string;
}): JSX.Element | null {
  const text = (children || "").trim();
  if (!text) return null;
  // Blank lines separate paragraphs; soft line breaks within a paragraph are
  // just wrapping and collapse to spaces.
  const paragraphs = text.split(/\n\s*\n/);
  return (
    <div className={className}>
      {paragraphs.map((p, i) => (
        <p key={i}>{renderInline(p.replace(/\s*\n\s*/g, " "), String(i))}</p>
      ))}
    </div>
  );
}

// Inline variant for table cells / one-liners: first sentence only, no <p>.
export function MarkdownInline({
  children,
}: {
  children: string;
}): JSX.Element | null {
  const text = (children || "").trim().replace(/\s*\n\s*/g, " ");
  if (!text) return null;
  return <>{renderInline(text, "i")}</>;
}
