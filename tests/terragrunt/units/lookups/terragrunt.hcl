include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../modules/lookups"
}

dependency "checks" {
  config_path = "../checks"

  mock_outputs = {
    full = { id = "00000000-0000-4000-8000-000000000000", slug = "mock" }
  }
  mock_outputs_allowed_terraform_commands = ["validate", "destroy"]
}

inputs = {
  full_id   = dependency.checks.outputs.full.id
  full_slug = dependency.checks.outputs.full.slug
}
