variable "plerion_account_id" {
  description = "Plerion AWS account shown in the Plerion console during onboarding"
  type        = string
}

data "plerion_tenant" "this" {}

resource "aws_iam_role" "plerion" {
  name = "PlerionAccessRole"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${var.plerion_account_id}:root" }
      Action    = "sts:AssumeRole"
      Condition = { StringEquals = { "sts:ExternalId" = data.plerion_tenant.this.external_id } }
    }]
  })

  # Plerion only assumes roles with this tag.
  tags = { RolePurpose = "PlerionAccess" }
}

resource "aws_iam_role_policy_attachment" "security_audit" {
  role       = aws_iam_role.plerion.name
  policy_arn = "arn:aws:iam::aws:policy/SecurityAudit"
}

resource "plerion_aws_integration" "this" {
  role_arn = aws_iam_role.plerion.arn
  name     = "production"

  depends_on = [aws_iam_role_policy_attachment.security_audit]
}
