/**
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
import React, { useState, useRef, memo } from 'react';
import { useThemeConfig } from '@docusaurus/theme-common';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import clsx from 'clsx';
import styles from './styles.module.css';
import DarkIcon from "./icon_night.svg"
import LightIcon from "./icon_day.svg"

const Dark = ({ icon, style }) => (
  <span className={clsx(styles.toggle, styles.dark)} style={style}>
    <DarkIcon />
  </span>
);

const Light = ({ icon, style }) => (
  <span className={clsx(styles.toggle, styles.light)} style={style}>
    <LightIcon />
  </span>
); // Based on react-toggle (https://github.com/aaronshaf/react-toggle/).

const Toggle = memo(
  ({ className, icons, checked: defaultChecked, disabled, onChange }) => {
    const [checked, setChecked] = useState(defaultChecked);
    const [focused, setFocused] = useState(false);
    const inputRef = useRef(null);

    const handleToggle = (e) => {
      const checkbox = inputRef.current;

      if (!checkbox) {
        return;
      }

      if (e.target !== checkbox) {
        e.preventDefault();
        checkbox.focus();
        checkbox.click();
        return;
      }

      setChecked(checkbox?.checked);
    };

    return (
      <div
        className={clsx('react-toggle', className, {
          'react-toggle--checked': checked,
          'react-toggle--focus': focused,
          'react-toggle--disabled': disabled,
        })}
        role="button"
        tabIndex={-1}
        onClick={handleToggle}>
        <div className="react-toggle-track">
          <div className="react-toggle-track-check">{icons.checked}</div>
          <div className="react-toggle-track-x">{icons.unchecked}</div>
        </div>
        <div className="react-toggle-thumb" />

        <input
          ref={inputRef}
          checked={checked}
          type="checkbox"
          className="react-toggle-screenreader-only"
          aria-label="Switch between dark and light mode"
          onChange={onChange}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
        />
      </div>
    );
  },
);
export default function (props) {
  const {
    colorMode: {
      switchConfig: { darkIcon, darkIconStyle, lightIcon, lightIconStyle },
    },
  } = useThemeConfig();
  const { isClient } = useDocusaurusContext();
  return (
    <Toggle
      disabled={!isClient}
      icons={{
        checked: <Dark icon={darkIcon} style={darkIconStyle} />,
        unchecked: <Light icon={lightIcon} style={lightIconStyle} />,
      }}
      {...props}
    />
  );
}
