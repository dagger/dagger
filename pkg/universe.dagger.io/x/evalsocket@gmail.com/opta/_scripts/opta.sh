#!/usr/bin/env bash
set -xeo pipefail

if [[ ${ACTION} == "apply" ||  ${ACTION} == "destroy" ]] then
  opta ${ACTION} -c ${OPTA_CONFIG}  --auto-approve --env ${ENV} ${EXTRA_ARGS}
else
  opta force-unlock -c ${OPTA_CONFIG}  --auto-approve --env ${ENV} ${EXTRA_ARGS}
fi

