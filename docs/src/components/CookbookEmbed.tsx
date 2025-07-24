import React, { useState, lazy, Suspense } from "react";
import styles from "@site/src/css/cookbookLoader.module.scss";

interface CookbookFrontMatter {
  title: string;
  description: string;
  cookbook_tag: string;
  slug?: string;
}

interface CookbookFile {
  path: string;
  frontMatter: CookbookFrontMatter;
  contentTitle: string;
  excerpt: string;
  firstHeading?: string;
}

interface CookbookEmbedProps {
  cookbookTag: string;
  files: CookbookFile[];
  className?: string;
}

/**
 * Alternative component that embeds the actual MDX content directly
 * This requires the MDX files to be available as components
 */
export default function CookbookEmbed({
  cookbookTag,
  files,
  className,
}: CookbookEmbedProps) {
  const containerClass = className ? `${styles.cookbookLoader} ${className}` : styles.cookbookLoader;

  if (files.length === 0) {
    return (
      <div className={containerClass}>
        <div className={styles.cookbookEmpty}>
          <p>No cookbook files found for tag: <strong>{cookbookTag}</strong></p>
        </div>
      </div>
    );
  }

  return (
    <div className={containerClass}>
      {files.map((file, index) => (
        <CookbookEmbedCard key={index} file={file} />
      ))}
    </div>
  );
}

function CookbookEmbedCard({ file }: { file: CookbookFile }) {
  const [expanded, setExpanded] = useState(false);
  const [MdxComponent, setMdxComponent] = useState<React.ComponentType | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadMdxComponent = async () => {
    if (MdxComponent || loading) return;
    
    setLoading(true);
    setError(null);
    
    try {
      const fileName = file.path.split('/').pop()?.replace('.mdx', '') || '';
      
      // Dynamic import of the MDX file
      // This assumes the MDX files are compiled and available as modules
      const module = await import(`@site/current_docs/partials/cookbook/${fileName}.mdx`);
      setMdxComponent(() => module.default);
    } catch (err) {
      console.error('Failed to load MDX component:', err);
      setError('Failed to load content');
    } finally {
      setLoading(false);
    }
  };

  const handleToggle = () => {
    if (!expanded && !MdxComponent) {
      loadMdxComponent();
    }
    setExpanded(!expanded);
  };

  return (
    <div className={styles.cookbookFileCard}>
      <div onClick={handleToggle} style={{ cursor: 'pointer' }}>
        <h2 className={styles.cookbookFileTitle}>
          {file.frontMatter.title}
          <span style={{ 
            float: 'right', 
            fontSize: '1rem', 
            color: 'var(--ifm-color-emphasis-600)' 
          }}>
            {expanded ? '▼' : '▶'}
          </span>
        </h2>
        <p className={styles.cookbookFileDescription}>
          {file.frontMatter.description}
        </p>
      </div>
      
      {expanded && (
        <div className={styles.cookbookContent} style={{
          marginTop: '1rem',
          padding: '1.5rem',
          backgroundColor: 'var(--ifm-color-emphasis-100)',
          border: '1px solid var(--ifm-color-emphasis-200)',
          borderRadius: '8px'
        }}>
          {loading && (
            <div style={{ textAlign: 'center', padding: '2rem' }}>
              Loading content...
            </div>
          )}
          
          {error && (
            <div style={{ 
              color: 'var(--ifm-color-danger)', 
              textAlign: 'center', 
              padding: '2rem' 
            }}>
              {error}
            </div>
          )}
          
          {MdxComponent && (
            <Suspense fallback={<div>Rendering content...</div>}>
              <div className="mdx-embedded-content">
                <MdxComponent />
              </div>
            </Suspense>
          )}
        </div>
      )}
    </div>
  );
}
