# Shared settings for every unit in the suite. run.sh sets the environment
# variables read here; see README.md.

locals {
  # Prefixes every slug so a run only touches the checks it created.
  run_id = get_env("PLERION_TEST_RUN_ID")
  phase  = get_env("PLERION_TEST_PHASE", "create")
}

# The provider reads PLERION_API_URL and PLERION_API_KEY from the
# environment, which also tests that way of configuring it.
generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    provider "plerion" {}
  EOF
}

# Keep state next to each unit rather than inside .terragrunt-cache, so it
# survives cache rebuilds between the suite's steps.
generate "backend" {
  path      = "backend.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    terraform {
      backend "local" {
        path = "${get_terragrunt_dir()}/terraform.tfstate"
      }
    }
  EOF
}

inputs = {
  run_id = local.run_id
  phase  = local.phase
}
