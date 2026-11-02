package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ datasource.DataSource              = &tenantDataSource{}
	_ datasource.DataSourceWithConfigure = &tenantDataSource{}
)

// NewTenantDataSource returns the plerion_tenant data source.
func NewTenantDataSource() datasource.DataSource {
	return &tenantDataSource{}
}

type tenantDataSource struct {
	client *client.Client
}

type tenantModel struct {
	TenantID       types.String `tfsdk:"tenant_id"`
	OrganizationID types.String `tfsdk:"organization_id"`
	Name           types.String `tfsdk:"name"`
	ExternalID     types.String `tfsdk:"external_id"`
}

func (d *tenantDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant"
}

func (d *tenantDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The Plerion tenant the API key belongs to. Use `external_id` in the " +
			"trust policy of the IAM roles Plerion assumes.",
		Attributes: map[string]schema.Attribute{
			"tenant_id": schema.StringAttribute{
				MarkdownDescription: "Tenant id.",
				Computed:            true,
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "Id of the Plerion organization the tenant belongs to.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Tenant name.",
				Computed:            true,
			},
			"external_id": schema.StringAttribute{
				MarkdownDescription: "External id Plerion sends when it assumes a role in your account. " +
					"Reading it needs a key whose role can modify integrations.",
				Computed: true,
			},
		},
	}
}

func (d *tenantDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *tenantDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tenant, err := d.client.GetTenant(ctx)
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "read the Plerion tenant", roleForIntegrations, err)
		return
	}
	externalID, err := d.client.GetExternalID(ctx)
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "read the tenant's external id", roleForIntegrations, err)
		return
	}

	state := tenantModel{
		TenantID:       types.StringValue(tenant.TenantID),
		OrganizationID: types.StringValue(tenant.OrganizationID),
		Name:           types.StringValue(tenant.Name),
		ExternalID:     types.StringValue(externalID),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
