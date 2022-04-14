import React from 'react'

function DeprecatedVersionBanner() {
  return (
      <div class="theme-doc-version-banner alert alert--warning margin-bottom--md" role="alert">
        <div>This is documentation for Dagger <b>0.1</b>, which is no longer actively maintained.</div>
        <div class="margin-top--md">For up-to-date documentation, see the <b><a href="/">latest version</a></b> (0.2).</div>
      </div>
  )
}

export default DeprecatedVersionBanner