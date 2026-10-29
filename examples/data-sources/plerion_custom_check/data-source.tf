data "plerion_custom_check" "github_oidc_roles" {
  slug = "github-oidc-roles-must-pin-a-repository"
}

output "github_oidc_roles_findings" {
  value = try(data.plerion_custom_check.github_oidc_roles.run_status.total_finding_count, null)
}
