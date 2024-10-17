#!/bin/bash

mapfile -t containers < <(docker ps --filter name="dagger-engine-*" -q)
  if [[ "${#containers[@]}" -gt 0 ]]; then
    docker stop -t 300 "${containers[@]}";
  fi
