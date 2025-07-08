import React from 'react';
// Import the CodeBlock component from your documentation framework
import { CodeBlock, CodeGroup } from '@mintlify/components';

export const CustomCodeGroup = ({ 
  goCode, 
  pythonCode, 
  typescriptCode, 
  phpCode, 
  javaCode,
  // Add metadata objects for each language
  goMeta = {},
  pythonMeta = {},
  typescriptMeta = {},
  phpMeta = {},
  javaMeta = {}
}) => {
  // Determine which code snippets are available
  const hasGoCode = Boolean(goCode);
  const hasPythonCode = Boolean(pythonCode);
  const hasTypeScriptCode = Boolean(typescriptCode);
  const hasPhpCode = Boolean(phpCode);
  const hasJavaCode = Boolean(javaCode);

  // Default language display names
  const languageDisplayNames = {
    'go': 'Go',
    'python': 'Python',
    'typescript': 'TypeScript',
    'php': 'PHP',
    'java': 'Java'
  };

  // Default icons for languages
  const defaultIcons = {
    'go': 'golang',
    'python': 'python',
    'typescript': 'js',
    'php': 'php',
    'java': 'java'
  };

  // Get metadata for a specific language
  const getMetaForLanguage = (language) => {
    switch(language) {
      case 'go': return goMeta;
      case 'python': return pythonMeta;
      case 'typescript': return typescriptMeta;
      case 'php': return phpMeta;
      case 'java': return javaMeta;
      default: return {};
    }
  };

  const renderCodeBlock = (code, language) => {
    // If no code is provided, don't render anything
    if (!code) return null;
    
    // Remove any variable syntax and get the actual code
    const actualCode = typeof code === 'string' ? code.replace(/\{.*?\}/g, '').trim() : '';
    
    // Get the metadata for this language
    const meta = getMetaForLanguage(language);
    
    // Map SDK names to their language identifiers for syntax highlighting
    const languageMap = {
      'go': 'go',
      'python': 'python',
      'typescript': 'typescript',
      'php': 'php',
      'java': 'java'
    };

    // Prepare title metadata - ensure title is always set
    // Priority: explicit meta.title > default language display name
    const title = meta.title || languageDisplayNames[language] || language.charAt(0).toUpperCase() + language.slice(1);
    
    // Prepare icon metadata - ensure icon is always set
    // Priority: explicit meta.icon > default language icon
    const icon = meta.icon || defaultIcons[language] || language;
    
    // Other metadata
    const shouldWrap = meta.wrap === true;
    
    // Use the CodeBlock component with all metadata
    return (
      <CodeBlock 
        language={languageMap[language] || language} 
        className="sdk-code"
        icon={icon}
        wrap={shouldWrap}
        lineNumbers={meta.lineNumbers !== false}
        title={title}
        {...meta}
      >
        {actualCode}
      </CodeBlock>
    );
  };

  // If no code snippets are available, don't render anything
  if (!hasGoCode && !hasPythonCode && !hasTypeScriptCode && !hasPhpCode && !hasJavaCode) {
    return null;
  }

  // If only one code snippet is available, render just that code without a group
  if ([hasGoCode, hasPythonCode, hasTypeScriptCode, hasPhpCode, hasJavaCode].filter(Boolean).length === 1) {
    if (hasGoCode) return renderCodeBlock(goCode, 'go');
    if (hasPythonCode) return renderCodeBlock(pythonCode, 'python');
    if (hasTypeScriptCode) return renderCodeBlock(typescriptCode, 'typescript');
    if (hasPhpCode) return renderCodeBlock(phpCode, 'php');
    if (hasJavaCode) return renderCodeBlock(javaCode, 'java');
  }

  // Render all code blocks within a CodeGroup
  return (
    <CodeGroup>
      {hasGoCode && renderCodeBlock(goCode, 'go')}
      {hasPythonCode && renderCodeBlock(pythonCode, 'python')}
      {hasTypeScriptCode && renderCodeBlock(typescriptCode, 'typescript')}
      {hasPhpCode && renderCodeBlock(phpCode, 'php')}
      {hasJavaCode && renderCodeBlock(javaCode, 'java')}
    </CodeGroup>
  );
};

// For backward compatibility with the CodeGroup usage
export default function ({ children }) {
  // Parse code snippets from children if using the old format
  const codeSnippets = {
    goCode: null,
    pythonCode: null,
    typescriptCode: null,
    phpCode: null,
    javaCode: null,
    goMeta: {},
    pythonMeta: {},
    typescriptMeta: {},
    phpMeta: {},
    javaMeta: {}
  };

  // Process children to extract actual code values and metadata if available
  if (children) {
    React.Children.forEach(children, child => {
      if (React.isValidElement(child) && child.props) {
        let language = '';
        let meta = {};
        
        // Get language and metadata from the title or metastring
        if (child.props.title) {
          const titleParts = child.props.title.split(' ');
          language = titleParts[0].toLowerCase();
          
          // Extract metadata from title (e.g., "Go {icon=golang wrap=true}")
          const metaRegex = /\{([^}]+)\}/g;
          const metaMatches = child.props.title.match(metaRegex);
          
          if (metaMatches) {
            metaMatches.forEach(metaStr => {
              const metaContent = metaStr.slice(1, -1); // Remove the braces
              const metaPairs = metaContent.split(' ');
              
              metaPairs.forEach(pair => {
                const [key, value] = pair.split('=');
                if (key && value) {
                  meta[key.trim()] = value.trim() === 'true' ? true : 
                                     value.trim() === 'false' ? false : 
                                     value.trim();
                }
              });
            });
          }
          
          // Set title based on first part of the title (the language name)
          // If no explicit title is in metadata, use the first word of the title
          if (!meta.title) {
            // Capitalize first letter
            meta.title = titleParts[0].charAt(0).toUpperCase() + titleParts[0].slice(1);
          }
        } else if (child.props.className) {
          // Try to get language from className (e.g., "language-go")
          const langMatch = child.props.className.match(/language-(\w+)/);
          if (langMatch) {
            language = langMatch[1].toLowerCase();
            // Set default title based on language if not provided
            if (!meta.title) {
              meta.title = language.charAt(0).toUpperCase() + language.slice(1);
            }
          }
        } else if (child.props.language) {
          // Get language directly from the language prop
          language = child.props.language.toLowerCase();
          // Set default title based on language if not provided
          if (!meta.title) {
            meta.title = language.charAt(0).toUpperCase() + language.slice(1);
          }
        }
        
        // Get metadata from metastring if available (often provided by MDX processors)
        if (child.props.metastring) {
          const metaItems = child.props.metastring.split(' ');
          metaItems.forEach(item => {
            const [key, value] = item.split('=');
            if (key && value) {
              meta[key.trim()] = value.trim() === 'true' ? true : 
                                 value.trim() === 'false' ? false : 
                                 value.trim().replace(/^["'](.*)["']$/, '$1'); // Remove quotes if present
            }
          });
        }
        
        // Map the language to the appropriate code snippet
        if (language === 'go') {
          codeSnippets.goCode = child.props.children;
          codeSnippets.goMeta = { ...meta, ...(child.props.meta || {}) };
        } else if (language === 'python') {
          codeSnippets.pythonCode = child.props.children;
          codeSnippets.pythonMeta = { ...meta, ...(child.props.meta || {}) };
        } else if (language === 'typescript' || language === 'ts') {
          codeSnippets.typescriptCode = child.props.children;
          codeSnippets.typescriptMeta = { ...meta, ...(child.props.meta || {}) };
        } else if (language === 'php') {
          codeSnippets.phpCode = child.props.children;
          codeSnippets.phpMeta = { ...meta, ...(child.props.meta || {}) };
        } else if (language === 'java') {
          codeSnippets.javaCode = child.props.children;
          codeSnippets.javaMeta = { ...meta, ...(child.props.meta || {}) };
        }
      }
    });
  }

  return <CustomCodeGroup {...codeSnippets} />;
}