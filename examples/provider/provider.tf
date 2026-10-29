terraform {
  required_providers {
    plerion = {
      source  = "plerionhq/plerion"
      version = "~> 0.1"
    }
  }
}

# Reads the API key from the PLERION_API_KEY environment variable.
provider "plerion" {
  api_url = "https://us1.api.plerion.com"
}
