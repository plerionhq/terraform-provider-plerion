resource "plerion_custom_check" "github_oidc_roles" {
  slug        = "github-oidc-roles-must-pin-a-repository"
  title       = "GitHub OIDC roles must pin a repository"
  description = "Flags IAM roles that any GitHub repository can assume, because their trust policy has no repository condition."

  target = {
    asset_type = "AWS::IAM::Role"
    scope      = "batch"
  }

  defaults = {
    severity_level = "HIGH"
    message        = "The role trusts GitHub OIDC without limiting which repository can assume it."
    remediation    = "Add a StringLike condition on token.actions.githubusercontent.com:sub that names your repository."
    meta = jsonencode({
      owner = "platform-security"
    })
  }

  body = file("${path.module}/policies/github-oidc-roles.rego")
}
