include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../modules/conflict"
}

# Applying this unit is expected to fail, so it never runs as part of the
# stack. run.sh applies it on its own and checks the error.
exclude {
  if      = true
  actions = ["all"]
}

dependency "checks" {
  config_path = "../checks"

  mock_outputs = {
    minimal = { slug = "mock" }
  }
  mock_outputs_allowed_terraform_commands = ["validate", "destroy"]
}

inputs = {
  taken_slug = dependency.checks.outputs.minimal.slug
}
