terraform {
  required_providers {
    plerion = {
      source = "plerionhq/plerion"
    }
  }
}

variable "integration_id" {
  description = "Integration whose assets the dry run evaluates."
  type        = string
}

variable "custom_check_id" {
  description = "ID of the saved check whose change is being previewed."
  type        = string
}

variable "target" {
  description = "Target of the saved check."
  type = object({
    asset_type = string
    scope      = string
  })
}

# Flags every asset. Dry-run findings are never saved, so this is safe to run
# against a shared tenant.
data "plerion_custom_check_dry_run" "flag_everything" {
  integration_id  = var.integration_id
  custom_check_id = var.custom_check_id
  target          = var.target

  defaults = {
    severity_level = "INFORMATIONAL"
    message        = "Dry-run preview from the provider's Terragrunt suite."
  }

  body = <<-REGO
    package custom

    result := [{"id": asset.id, "status": "FAILED"} | asset := input.assets[_]]
  REGO

  timeout = "3m"
}

output "status" {
  value = data.plerion_custom_check_dry_run.flag_everything.status
}

output "custom_check_id" {
  value = data.plerion_custom_check_dry_run.flag_everything.custom_check_id
}

output "findings_match_total" {
  value = length(jsondecode(data.plerion_custom_check_dry_run.flag_everything.findings)) == data.plerion_custom_check_dry_run.flag_everything.total_findings
}
