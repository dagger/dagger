import React from "react";
import AgentsExamples from "../../versioned_docs/version-0.17.2/_examples_agents.mdx";
import CICDExamples from "../../versioned_docs/version-0.17.2/_examples_cicd.mdx";
import CookbookExamples from "../../versioned_docs/version-0.17.2/_examples_cookbook.mdx";

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

  const anyVisible = sections.some((section) => section.show);
  const renderSections = anyVisible
    ? sections.filter((section) => section.show)
    : sections;

  return (
    <>
      {renderSections.map((section, index) => {
        const Section = section.component;
        return <Section key={index} />;
      })}
    </>
  );
}
