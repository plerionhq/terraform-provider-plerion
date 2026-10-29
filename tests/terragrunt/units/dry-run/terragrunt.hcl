include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../modules/dry-run"
}

# run.sh finds an integration to use; without one there is nothing to run
# the dry run against.
exclude {
  if      = get_env("PLERION_TEST_INTEGRATION_ID", "") == ""
  actions = ["all"]
}

dependency "checks" {
  config_path = "../checks"

  mock_outputs = {
    full = {
      id     = "00000000-0000-4000-8000-000000000000"
      target = { asset_type = "AWS::S3::Bucket", scope = "batch" }
    }
  }
  mock_outputs_allowed_terraform_commands = ["validate", "destroy"]
}

inputs = {
  integration_id  = get_env("PLERION_TEST_INTEGRATION_ID", "")
  custom_check_id = dependency.checks.outputs.full.id
  target          = dependency.checks.outputs.full.target
}
