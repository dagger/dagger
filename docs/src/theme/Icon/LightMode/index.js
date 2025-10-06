import React from 'react';
import LightMode from '@theme-original/Icon/LightMode';

export default function LightModeWrapper(props) {
  return (
    <>
      <LightMode {...props} style={{color: 'var(--ifm-color-primary-light)'}}/>
    </>
  );
}
