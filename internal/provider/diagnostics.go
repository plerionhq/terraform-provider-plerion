package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

// addAPIError turns a client error into a diagnostic that says what failed and
// what to do next. action completes the sentence "Could not ...".
func addAPIError(diagnostics *diag.Diagnostics, action string, err error) {
	switch {
	case client.IsUnauthorized(err):
		diagnostics.AddError("Plerion rejected the API key",
			fmt.Sprintf("Could not %s because the API key was not accepted (%s). "+
				"Check that the key is correct and has not been revoked or expired. "+
				"API keys only work in the region where they were created, so also check that api_url points to that region.", action, err))
	case client.IsForbidden(err):
		diagnostics.AddError("The API key does not have permission",
			fmt.Sprintf("Could not %s because the API key's role does not allow it (%s). "+
				"Give the key a role that can manage custom checks, then try again.", action, err))
	default:
		diagnostics.AddError("Plerion API request failed", fmt.Sprintf("Could not %s: %s.", action, err))
	}
}

func addSlugConflictError(diagnostics *diag.Diagnostics, slug string, err error) {
	diagnostics.AddError("A custom check with this slug already exists",
		fmt.Sprintf("Plerion already has a custom check with the slug %q in this tenant (%s). "+
			"Slugs must be unique within a tenant. Choose a different slug, or bring the existing check under Terraform "+
			"with an import block that uses the slug as its ID:\n\n"+
			"import {\n  to = plerion_custom_check.<name>\n  id = %q\n}", slug, err, slug))
}
