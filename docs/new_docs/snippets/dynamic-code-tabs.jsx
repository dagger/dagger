import React, { useState } from 'react';

/**
 * DynamicCodeTabs component for documentation
 * 
 * This component renders code examples for multiple languages in a tabbed interface.
 * The actual file content is loaded during the build process using
 * the `file=` syntax in code blocks.
 * 
 * @param {Object} props Component properties
 * @param {string} props.basePath Base path for code examples
 * @param {Array<Object>} props.languages Languages to display tabs for
 * @param {string} props.groupId Tab group ID (defaults to "language")
 * @param {Object} props.descriptions Custom descriptions for each language tab
 * @param {Array<string>} props.availableLanguages Optional array of language values that are available
 * @returns {React.ReactElement} Tabs component with code examples
 */
export const DynamicCodeTabs = ({
  basePath,
  languages = [
    { value: 'go', label: 'Go', file: 'main.go', language: 'go' },
    { value: 'python', label: 'Python', file: 'main.py', language: 'python' },
    { value: 'typescript', label: 'TypeScript', file: 'index.ts', language: 'typescript' },
    { value: 'php', label: 'PHP', file: 'src/MyModule.php', language: 'php' }
  ],
  groupId = 'language',
  descriptions = {
    go: 'The default path is set by adding a `defaultPath` pragma on the corresponding Dagger Function `source` argument.',
    python: 'The default path is set by adding a `DefaultPath` annotation on the corresponding Dagger Function `source` argument.',
    typescript: 'The default path is set by adding an `@argument` decorator with a `defaultPath` parameter on the corresponding Dagger Function `source` argument.',
    php: 'The default path is set by adding a `#[DefaultPath]` Attribute on the corresponding Dagger Function `source` argument.'
  },
  // Allow passing specific available languages if known in advance
  availableLanguages = null
}) => {
  // Start with the first language tab active
  const [activeTab, setActiveTab] = useState(0);
  
  // Use all languages if availableLanguages is not provided
  const languagesToShow = availableLanguages 
    ? languages.filter(lang => availableLanguages.includes(lang.value))
    : languages;
  
  if (languagesToShow.length === 0) {
    return null;
  }

  // Create the tab content with proper MDX formatting
  const renderTabContent = (lang) => {
    const filePath = `${basePath}/${lang.value}/${lang.file}`;
    
    return (
      <div>
        {descriptions[lang.value] && <p>{descriptions[lang.value]}</p>}
        <div dangerouslySetInnerHTML={{
          __html: `\`\`\`${lang.language} file=./${filePath}\n\`\`\``
        }} />
      </div>
    );
  };

  return (
    <div className="tabs-container">
      <div className="tabs-header" role="tablist">
        {languagesToShow.map((lang, index) => (
          <button
            key={lang.value}
            role="tab"
            aria-selected={index === activeTab}
            onClick={() => setActiveTab(index)}
            data-value={lang.value}
            data-group-id={groupId}
            data-tab-query-string="sdk"
            className={`tab-button ${index === activeTab ? 'active' : ''}`}
          >
            {lang.label}
          </button>
        ))}
      </div>
      <div className="tabs-content">
        {renderTabContent(languagesToShow[activeTab])}
      </div>
    </div>
  );
}

// Example usage in MDX:
// <DynamicCodeTabs basePath="snippets/default-paths" />
// Or with specific available languages:
// <DynamicCodeTabs basePath="snippets/default-paths" availableLanguages={['go', 'python']} />