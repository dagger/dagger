[[- /*

## `job_name` helper

*/ -]]

[[- define "job_name" -]]
[[ coalesce ( var "job_name" .) (meta "pack.name" .) | quote ]]
[[- end -]]

[[- /*

## `region` helper

*/ -]]

[[ define "region" -]]
[[- if var "region" . -]]
  region = "[[ var "region" . ]]"
[[- end -]]
[[- end -]]

[[- /*

## `node_pool` helper

*/ -]]

[[ define "node_pool" -]]
[[- if var "node_pool" . -]]
  node_pool = "[[ var "node_pool" . ]]"
[[- end -]]
[[- end -]]

[[- /*

## `resources` helper

*/ -]]

[[ define "resources" -]]
[[- if var "resources" . ]]
    resources {
      cpu    = [[ var "resources.cpu" . ]]
      memory = [[ var "resources.memory" . ]]
    }
[[- end ]]
[[- end -]]