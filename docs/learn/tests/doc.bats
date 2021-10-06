## Doc commands are being extracted from this file and helpers.
## Indentation is important, please append at the end

setup() {
  load 'helpers'

  common_setup
}

#  Test 1003-get-started
@test "doc-1003-get-started" {
  setup_example_sandbox

  # Follow tutorial
  mkdir -p "$DAGGER_SANDBOX"/plans/local
  cp "$DAGGER_PROJECT"/getting-started/plans/todoapp.cue "$DAGGER_SANDBOX"/plans/todoapp.cue
  cp "$DAGGER_PROJECT"/getting-started/plans/local/local.cue "$DAGGER_SANDBOX"/plans/local/local.cue
  
  dagger --project "$DAGGER_SANDBOX" new 'local' -p "$DAGGER_SANDBOX"/plans/local
  dagger --project "$DAGGER_SANDBOX" -e 'local' input socket dockerSocket /var/run/docker.sock
  dagger --project "$DAGGER_SANDBOX" -e 'local' input dir app.source "$DAGGER_SANDBOX"

  dagger --project "$DAGGER_SANDBOX" -e 'local' up

  until docker inspect --format "{{json .State.Status }}" todoapp | grep -m 1 "running"; do sleep 1 ; done
  run curl -f -LI http://localhost:8080
  assert_output --partial '200 OK'
  docker stop todoapp && docker rm todoapp
  docker stop registry && docker rm registry
}

@test "doc-1004-first-env" {
  setup_example_sandbox

  # Follow tutorial
  mkdir -p "$DAGGER_SANDBOX"/multibucket
  cp "$DAGGER_PROJECT"/multibucket/source.cue "$DAGGER_SANDBOX"/multibucket
  cp "$DAGGER_PROJECT"/multibucket/yarn.cue "$DAGGER_SANDBOX"/multibucket
  cp "$DAGGER_PROJECT"/multibucket/netlify.cue "$DAGGER_SANDBOX"/multibucket

  dagger --project "$DAGGER_SANDBOX" doc alpha.dagger.io/netlify
  dagger --project "$DAGGER_SANDBOX" doc alpha.dagger.io/js/yarn

  # Initialize new env
  dagger --project "$DAGGER_SANDBOX" new 'multibucket' -p "$DAGGER_SANDBOX"/multibucket

  # Copy corresponding env
  cp -r "$DAGGER_PROJECT"/.dagger/env/multibucket "$DAGGER_SANDBOX"/.dagger/env/

  # Add missing src input
  dagger --project "$DAGGER_SANDBOX" -e multibucket input dir src "$DAGGER_SANDBOX"

  # Run test
  dagger --project "$DAGGER_SANDBOX" -e multibucket up
  url=$(dagger --project "$DAGGER_SANDBOX" -e multibucket query -f text site.netlify.deployUrl)

  # Check output
  run curl "$url"
  assert_output --partial "./static/css/main.9149988f.chunk.css"
}

@test "doc-1006-google-cloud-run" {
  setup_example_sandbox

  # Follow tutorial
  mkdir -p "$DAGGER_SANDBOX"/gcpcloudrun
  cp "$DAGGER_PROJECT"/gcpcloudrun/source.cue "$DAGGER_SANDBOX"/gcpcloudrun

  # Initialize new env
  dagger --project "$DAGGER_SANDBOX" new 'gcpcloudrun' -p "$DAGGER_SANDBOX"/gcpcloudrun

  # Copy corresponding env
  cp -r "$DAGGER_PROJECT"/.dagger/env/gcpcloudrun "$DAGGER_SANDBOX"/.dagger/env/

  # Add missing src input
  dagger --project "$DAGGER_SANDBOX" -e gcpcloudrun input dir src "$DAGGER_SANDBOX"

  # Run test
  run dagger --project "$DAGGER_SANDBOX" -e gcpcloudrun up
  assert_success
}

@test "doc-1007-kube-kind" {
  skip "debug CI issue"
  # skip_unless_local_kube

  # #################### BASIC ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-kind-basic kube-kind

  # # Add kubeconfig
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-basic input text kubeconfig -f "$HOME"/.kube/config

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-basic up

  # # Check deployment
  # kubectl describe deployment todoapp | grep 'True'

  # # Clean
  # kubectl delete deployments --all
  # kubectl delete services --all

  # #################### DEPLOYMENT ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-kind-deployment kube-kind

  # # Add kubeconfig
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-deployment input text kubeconfig -f "$HOME"/.kube/config

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-deployment up

  # # Check deployment
  # kubectl describe deployment todoapp | grep 'True'

  # # Clean
  # kubectl delete deployments --all
  # kubectl delete services --all

  # #################### CUE MANIFEST ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-kind-cue-manifest kube-kind

  # # Add kubeconfig
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-cue-manifest input text kubeconfig -f "$HOME"/.kube/config

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-kind-cue-manifest up

  # # Check deployment
  # kubectl describe deployment todoapp | grep 'True'

  # # Clean
  # kubectl delete deployments --all
  # kubectl delete services --all
}

@test "doc-1007-kube-aws" {
  skip "debug CI issue"
  # #################### BASIC ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-aws-basic kube-aws

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-aws-basic up

  # #################### DEPLOYMENT ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-aws-deployment kube-aws

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-aws-deployment up
  # #################### CUE MANIFEST ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-aws-cue-manifest kube-aws

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-aws-cue-manifest up
}

@test "doc-1007-kube-gcp" {
  skip "debug CI issue"
  # #################### BASIC ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-gcp-basic kube-gcp

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-gcp-basic up

  # #################### DEPLOYMENT ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-gcp-deployment kube-gcp

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-gcp-deployment up
  # #################### CUE MANIFEST ####################
  # # Copy deployment to sandbox
  # copy_to_sandbox kube-gcp-cue-manifest kube-gcp

  # # Up deployment
  # dagger --project "$DAGGER_SANDBOX" -e kube-gcp-cue-manifest up
}

@test "doc-1008-aws-cloudformation" {
  skip_unless_local_localstack
  setup_example_sandbox

  ### Create a basic plan
  ## Construct
  mkdir -p "$DAGGER_SANDBOX"/cloudformation
  cp "$DAGGER_PROJECT"/cloudformation/template.cue "$DAGGER_SANDBOX"/cloudformation

  # Cloudformation relay
  dagger --project "$DAGGER_SANDBOX" doc alpha.dagger.io/aws/cloudformation
  cp "$DAGGER_PROJECT"/cloudformation/source-begin.cue "$DAGGER_SANDBOX"/cloudformation/source.cue

  # Initialize new env
  dagger --project "$DAGGER_SANDBOX" new 'cloudformation' -p "$DAGGER_SANDBOX"/cloudformation

  # Finish template setup
  cp "$DAGGER_PROJECT"/cloudformation/source-end.cue "$DAGGER_SANDBOX"/cloudformation/source.cue

  # Copy corresponding env
  cp -r "$DAGGER_PROJECT"/.dagger/env/cloudformation "$DAGGER_SANDBOX"/.dagger/env/

  # Run test
  dagger --project "$DAGGER_SANDBOX" -e cloudformation up
  stackName=$(dagger --project "$DAGGER_SANDBOX" -e cloudformation query cfnStackName -f text)

  ## Cleanup
  # Place back empty source
  cp "$DAGGER_PROJECT"/cloudformation/source-begin.cue "$DAGGER_SANDBOX"/cloudformation/source.cue
  cp "$DAGGER_PROJECT"/cloudformation/deletion.cue "$DAGGER_SANDBOX"/cloudformation/deletion.cue
  # Prepare and run cloudformation cleanup
  dagger --project "$DAGGER_SANDBOX" -e cloudformation input text stackRemoval.stackName "$stackName"
  dagger --project "$DAGGER_SANDBOX" -e cloudformation up

  ### Template part
  ## Create convert.cue
  cp "$DAGGER_PROJECT"/cloudformation/template/convert.cue "$DAGGER_SANDBOX"/cloudformation/convert.cue
  rm "$DAGGER_SANDBOX"/cloudformation/source.cue "$DAGGER_SANDBOX"/cloudformation/deletion.cue

  ## Retrieve Unmarshalled JSON
  dagger --project "$DAGGER_SANDBOX" query -e cloudformation s3Template

  ## Remove convert.cue
  rm "$DAGGER_SANDBOX"/cloudformation/convert.cue

  ## Store the output
  cp "$DAGGER_PROJECT"/cloudformation/template/template-begin.cue "$DAGGER_SANDBOX"/cloudformation/template.cue

  # Inspect conf
  dagger --project "$DAGGER_SANDBOX" query -e cloudformation template -f text

  cp "$DAGGER_PROJECT"/cloudformation/template/deployment.cue "$DAGGER_SANDBOX"/cloudformation/deployment.cue
  cp "$DAGGER_PROJECT"/cloudformation/template/template-end.cue "$DAGGER_SANDBOX"/cloudformation/template.cue
  cp "$DAGGER_PROJECT"/cloudformation/source-end.cue "$DAGGER_SANDBOX"/cloudformation/source.cue

  # Deploy again
  dagger --project "$DAGGER_SANDBOX" -e cloudformation query template -f text
  dagger --project "$DAGGER_SANDBOX" -e cloudformation up
  dagger --project "$DAGGER_SANDBOX" -e cloudformation output list

  ## Cleanup again
  stackName=$(dagger --project "$DAGGER_SANDBOX" -e cloudformation query cfnStackName -f text)
  rm -rf "$DAGGER_SANDBOX"/cloudformation/*

  # Place back empty source
  cp "$DAGGER_PROJECT"/cloudformation/source-begin.cue "$DAGGER_SANDBOX"/cloudformation/source.cue
  cp "$DAGGER_PROJECT"/cloudformation/deletion.cue "$DAGGER_SANDBOX"/cloudformation/deletion.cue

  # Prepare and run cloudformation cleanup
  dagger --project "$DAGGER_SANDBOX" -e cloudformation input text stackRemoval.stackName "$stackName"
  dagger --project "$DAGGER_SANDBOX" -e cloudformation up
}

@test "doc-1010-dev-cue-package" {
  # Initializing project
  mkdir -p "$DAGGER_SANDBOX"/project

  # Writing package
  # dagger init # The sandbox is already init
  mkdir -p "$DAGGER_SANDBOX"/cue.mod/pkg/github.com/tjovicic/gcpcloudrun
  cp "$DAGGER_PROJECT"/dev-cue-package/source.cue "$DAGGER_SANDBOX"/cue.mod/pkg/github.com/tjovicic/gcpcloudrun/source.cue
  cp "$DAGGER_PROJECT"/dev-cue-package/script.sh "$DAGGER_SANDBOX"/project/script.sh

  # We remove the last line of the script, as bats cannot expand dagger
  # to dagger() bats helper func inside bash files
  sed '$d' <"$DAGGER_SANDBOX"/project/script.sh >"$DAGGER_SANDBOX"/tmpFile
  mv "$DAGGER_SANDBOX"/tmpFile "$DAGGER_SANDBOX"/project/script.sh

  chmod +x "$DAGGER_SANDBOX"/project/script.sh
  "$DAGGER_SANDBOX"/project/script.sh

  # Sync file from documentation
  rsync -a test "$DAGGER_SANDBOX"

  # Command removed from script.sh above
  dagger --project "$DAGGER_SANDBOX" new staging -p "$DAGGER_SANDBOX"/test
  run dagger up --project "$DAGGER_SANDBOX" -e staging
  assert_output --partial "input=run.gcpConfig.serviceKey"

  # Clean script.sh output
  rm -rf ./test
}
