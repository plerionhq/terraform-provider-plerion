locals {
  github_oidc_roles = {
    target = {
      asset_type = "AWS::IAM::Role"
      scope      = "batch"
    }
    defaults = {
      severity_level = "HIGH"
      message        = "The role trusts GitHub OIDC without limiting which repository can assume it."
    }
    body = file("${path.module}/policies/github-oidc-roles.rego")
  }
}

data "plerion_custom_check_dry_run" "github_oidc_roles_gate" {
  integration_id = var.production_integration_id
  target         = local.github_oidc_roles.target
  defaults       = local.github_oidc_roles.defaults
  body           = local.github_oidc_roles.body
}

resource "plerion_custom_check" "github_oidc_roles" {
  slug     = "github-oidc-roles-must-pin-a-repository"
  title    = "GitHub OIDC roles must pin a repository"
  target   = local.github_oidc_roles.target
  defaults = local.github_oidc_roles.defaults
  body     = local.github_oidc_roles.body

  lifecycle {
    precondition {
      condition     = data.plerion_custom_check_dry_run.github_oidc_roles_gate.total_findings <= 5
      error_message = "The policy would flag more than 5 roles. Review it before applying."
    }
  }
}
