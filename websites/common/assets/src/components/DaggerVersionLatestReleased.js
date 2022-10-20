import React, {ReactNode} from 'react';
import {usePluginData} from '@docusaurus/useGlobalData';

const VersionContext = React.createContext()

export const DaggerVersionLatestReleased = ({children}) => {


  const {daggerVersionLatestRelease} = usePluginData('docusaurus-plugin-dagger-version');

  return  <VersionContext.Provider value={daggerVersionLatestRelease}>
      <VersionContext.Consumer>
        {children}
      </VersionContext.Consumer>
    </VersionContext.Provider>
}