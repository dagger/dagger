/**
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
import React from 'react';
import Translate from '@docusaurus/Translate';
import IconEdit from '../../../static/img/Dagger_Icons_Edit.svg';

export default function EditThisPage({ editUrl }) {
  return (
    <a href={editUrl} className='edit-this-page' target="_blank" rel="noreferrer noopener">
      <IconEdit width='1.2em' height='1.2em' />
      <Translate
        id="theme.common.editThisPage"
        description="The link label to edit the current page">
        Edit this page
      </Translate>
    </a>
  );
}
