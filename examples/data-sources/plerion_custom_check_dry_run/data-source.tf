variable "production_integration_id" {
  description = "The Plerion integration to preview the policy against."
  type        = string
}

# Preview the findings a check would produce in one integration before
# saving it. Reading the data source fails if the policy cannot be evaluated.
data "plerion_custom_check_dry_run" "github_oidc_roles" {
  integration_id = var.production_integration_id

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

# Stop the plan if the policy would flag more roles than expected.
check "github_oidc_roles_preview" {
  assert {
    condition     = data.plerion_custom_check_dry_run.github_oidc_roles.total_findings <= 5
    error_message = "The policy would flag ${data.plerion_custom_check_dry_run.github_oidc_roles.total_findings} roles. Review the policy before applying it."
  }
}

output "flagged_roles" {
  value = [for finding in jsondecode(data.plerion_custom_check_dry_run.github_oidc_roles.findings) : finding.assetId]
}
