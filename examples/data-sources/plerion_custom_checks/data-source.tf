data "plerion_custom_checks" "iam_roles" {
  asset_type = "AWS::IAM::Role"
}

output "iam_role_check_slugs" {
  value = data.plerion_custom_checks.iam_roles.custom_checks[*].slug
}
