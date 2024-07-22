job [[ template "job_name" . ]] {
  [[ template "region" . ]]
  [[ template "node_pool" . ]]
  datacenters = [[ var "datacenters" . | toStringList ]]
  type = "system"

  group "engine" {

    restart {
      attempts = 2
      interval = "30m"
      delay = "15s"
      mode = "fail"
    }

    task "engine" {
      driver = "docker"

      config {
        image = [[ var "dagger_image" . | quote ]]        
        cap_add    = ["sys_admin"]
        privileged = true

      }
      [[- template "resources" . ]]
    }
  }
}
