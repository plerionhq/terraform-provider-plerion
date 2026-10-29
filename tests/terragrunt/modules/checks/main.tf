terraform {
  required_providers {
    plerion = {
      source = "plerionhq/plerion"
    }
  }
}

variable "run_id" {
  description = "Prefix for every slug this run creates."
  type        = string
}

variable "phase" {
  description = "Which version of the checks to apply: create or update."
  type        = string

  validation {
    condition     = contains(["create", "update"], var.phase)
    error_message = "The phase must be create or update."
  }
}

locals {
  updated = var.phase == "update"
}

# Uses only the required attributes, so it also covers every default.
resource "plerion_custom_check" "minimal" {
  slug  = "${var.run_id}-minimal"
  title = "Terragrunt suite minimal check"

  target = {
    asset_type = "AWS::IAM::Role"
    scope      = "batch"
  }

  defaults = {
    severity_level = "LOW"
    message        = "Test finding from the provider's Terragrunt suite."
  }

  body = file("${path.module}/policies/no-findings.rego")
}

# Sets every attribute when created, then the update phase renames it,
# changes most values and removes every optional attribute.
resource "plerion_custom_check" "full" {
  slug        = local.updated ? "${var.run_id}-full-renamed" : "${var.run_id}-full"
  title       = local.updated ? "Terragrunt suite full check, updated" : "Terragrunt suite full check"
  description = local.updated ? null : "Created by the provider's Terragrunt suite. Safe to delete."

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = local.updated ? "all" : "batch"
  }

  additional_assets = local.updated ? null : [
    { asset_type = "AWS::IAM::Role" },
    { asset_type = "AWS::KMS::Key" },
  ]

  defaults = {
    severity_level = local.updated ? "CRITICAL" : "HIGH"
    message        = local.updated ? "Updated test finding." : "Test finding."
    informational  = local.updated
    remediation    = local.updated ? null : "Nothing to fix. This check comes from a test run."
    # The API returns 0.0000005 as 5e-7, which must not show up as a change.
    meta = local.updated ? null : jsonencode({
      suite     = "terragrunt"
      run_id    = var.run_id
      threshold = 0.0000005
      labels    = { team = "security", purpose = "provider-test" }
    })
  }

  body = file("${path.module}/policies/${local.updated ? "marker-tag" : "no-findings"}.rego")
}

output "minimal" {
  value = plerion_custom_check.minimal
}

output "full" {
  value = plerion_custom_check.full
}
