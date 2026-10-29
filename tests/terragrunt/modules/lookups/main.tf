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

variable "full_id" {
  description = "ID of the full check from the checks unit."
  type        = string
}

variable "full_slug" {
  description = "Slug of the full check from the checks unit."
  type        = string
}

data "plerion_custom_check" "by_id" {
  id = var.full_id
}

data "plerion_custom_check" "by_slug" {
  slug = var.full_slug
}

data "plerion_custom_checks" "roles" {
  asset_type = "AWS::IAM::Role"
}

output "by_id" {
  # run_status changes as the engine runs the check, so it is left out to keep
  # the suite's no-changes plans stable.
  value = {
    slug    = data.plerion_custom_check.by_id.slug
    title   = data.plerion_custom_check.by_id.title
    version = data.plerion_custom_check.by_id.version
    body    = data.plerion_custom_check.by_id.body
    meta    = data.plerion_custom_check.by_id.defaults.meta
  }
}

output "by_slug_id" {
  value = data.plerion_custom_check.by_slug.id
}

output "run_role_check_slugs" {
  value = sort([for check in data.plerion_custom_checks.roles.custom_checks : check.slug if startswith(check.slug, var.run_id)])
}
