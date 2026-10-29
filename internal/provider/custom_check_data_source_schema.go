package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

// customCheckComputedAttributes describes a stored check, with every attribute
// computed. Both check data sources use it.
func customCheckComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: "The ID of the check.",
			Computed:            true,
		},
		"slug": schema.StringAttribute{
			MarkdownDescription: "The check's slug, unique within the tenant.",
			Computed:            true,
		},
		"title": schema.StringAttribute{
			MarkdownDescription: "The check's title.",
			Computed:            true,
		},
		"description": schema.StringAttribute{
			MarkdownDescription: "The check's description.",
			Computed:            true,
		},
		"target": schema.SingleNestedAttribute{
			MarkdownDescription: "The asset type the check evaluates.",
			Computed:            true,
			Attributes: map[string]schema.Attribute{
				"asset_type": schema.StringAttribute{
					MarkdownDescription: "The asset type, for example `AWS::S3::Bucket`.",
					Computed:            true,
				},
				"scope": schema.StringAttribute{
					MarkdownDescription: "Either `batch` or `all`.",
					Computed:            true,
				},
			},
		},
		"additional_assets": schema.ListNestedAttribute{
			MarkdownDescription: "Other asset types loaded for the policy.",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"asset_type": schema.StringAttribute{
						MarkdownDescription: "The asset type.",
						Computed:            true,
					},
				},
			},
		},
		"defaults": schema.SingleNestedAttribute{
			MarkdownDescription: "Values applied to findings when the policy's result does not set them.",
			Computed:            true,
			Attributes: map[string]schema.Attribute{
				"severity_level": schema.StringAttribute{
					MarkdownDescription: "The finding severity.",
					Computed:            true,
				},
				"message": schema.StringAttribute{
					MarkdownDescription: "The finding message.",
					Computed:            true,
				},
				"informational": schema.BoolAttribute{
					MarkdownDescription: "Whether findings are informational only.",
					Computed:            true,
				},
				"remediation": schema.StringAttribute{
					MarkdownDescription: "How to fix a failed asset.",
					Computed:            true,
				},
				"meta": schema.StringAttribute{
					MarkdownDescription: "Extra metadata added to each finding, as a JSON object. Read it with `jsondecode()`.",
					Computed:            true,
					CustomType:          metaType{},
				},
			},
		},
		"type": schema.StringAttribute{
			MarkdownDescription: "The policy language.",
			Computed:            true,
		},
		"body": schema.StringAttribute{
			MarkdownDescription: "The rego policy.",
			Computed:            true,
		},
		"version": schema.Int64Attribute{
			MarkdownDescription: "The check's version.",
			Computed:            true,
		},
		"organization_id": schema.StringAttribute{
			MarkdownDescription: "The ID of the organization that owns the check.",
			Computed:            true,
		},
		"tenant_id": schema.StringAttribute{
			MarkdownDescription: "The ID of the tenant that owns the check.",
			Computed:            true,
		},
		"created_at": schema.StringAttribute{
			MarkdownDescription: "When the check was created, as an RFC 3339 timestamp.",
			Computed:            true,
		},
		"created_by": schema.StringAttribute{
			MarkdownDescription: "The ID of the user or API key that created the check.",
			Computed:            true,
		},
		"updated_at": schema.StringAttribute{
			MarkdownDescription: "When the check last changed, as an RFC 3339 timestamp.",
			Computed:            true,
		},
		"updated_by": schema.StringAttribute{
			MarkdownDescription: "The ID of the user or API key that last changed the check.",
			Computed:            true,
		},
		"last_run_at": schema.StringAttribute{
			MarkdownDescription: "When the check last reported a run result, as an RFC 3339 timestamp. Null if it has not run yet.",
			Computed:            true,
		},
		"run_status": schema.SingleNestedAttribute{
			MarkdownDescription: "A summary of the check's latest run across your integrations. " +
				"Null while a new or changed check waits for its first run.",
			Computed: true,
			Attributes: map[string]schema.Attribute{
				"overall_status": schema.StringAttribute{
					MarkdownDescription: "`running` while any integration is still being evaluated, `error` if the check " +
						"could not be evaluated on at least one integration, `completed` when it ran on every integration, " +
						"or `pending` when no integration has assets the check can run against.",
					Computed: true,
				},
				"integration_count": schema.Int64Attribute{
					MarkdownDescription: "How many integrations the check ran against.",
					Computed:            true,
				},
				"completed_count": schema.Int64Attribute{
					MarkdownDescription: "How many integrations the check finished on.",
					Computed:            true,
				},
				"failed_count": schema.Int64Attribute{
					MarkdownDescription: "How many integrations the check could not be evaluated on.",
					Computed:            true,
				},
				"total_finding_count": schema.Int64Attribute{
					MarkdownDescription: "How many findings the latest run produced across all integrations.",
					Computed:            true,
				},
			},
		},
	}
}

func clientFromProviderData(providerData any, resp *datasource.ConfigureResponse) *client.Client {
	if providerData == nil {
		return nil
	}
	apiClient, ok := providerData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *client.Client but got %T. This is a bug in the provider; please report it.", providerData))
		return nil
	}
	return apiClient
}
