terraform {
  required_providers {
    plerion = {
      source = "plerionhq/plerion"
    }
  }
}

variable "taken_slug" {
  description = "A slug that another check already uses."
  type        = string
}

# Applying this must fail, because the slug belongs to the minimal check.
resource "plerion_custom_check" "duplicate" {
  slug  = var.taken_slug
  title = "Duplicate slug"

  target = {
    asset_type = "AWS::IAM::Role"
    scope      = "batch"
  }

  defaults = {
    severity_level = "LOW"
    message        = "Should never be created."
  }

  body = "package custom\n\ndefault result := []\n"
}
