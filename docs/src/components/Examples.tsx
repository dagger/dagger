import React from "react";
import AgentsExamples from "../../current_docs/_examples_agents.mdx";
import CICDExamples from "../../current_docs/_examples_cicd.mdx";
import CookbookExamples from "../../current_docs/_examples_cookbook.mdx";

export default function Examples({
  showAgentsExample = false,
  showCICDExamples = false,
  showCookbookExamples = false,
}) {
  const sections = [
    { show: showAgentsExample, component: AgentsExamples },
    { show: showCICDExamples, component: CICDExamples },
    { show: showCookbookExamples, component: CookbookExamples },
  ];

  const anyVisible = sections.some((s) => s.show);
  const renderSections = anyVisible ? sections.filter((s) => s.show) : sections;

  return (
    <>
      {renderSections.map((section, idx) => {
        const Section = section.component;
        return <Section key={idx} />;
      })}
    </>
  );
}
