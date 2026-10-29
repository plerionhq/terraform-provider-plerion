package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ datasource.DataSource              = &customChecksDataSource{}
	_ datasource.DataSourceWithConfigure = &customChecksDataSource{}
)

// NewCustomChecksDataSource returns the plerion_custom_checks data source.
func NewCustomChecksDataSource() datasource.DataSource {
	return &customChecksDataSource{}
}

type customChecksDataSource struct {
	client *client.Client
}

type customChecksDataSourceModel struct {
	AssetType    types.String                 `tfsdk:"asset_type"`
	Scope        types.String                 `tfsdk:"scope"`
	CustomChecks []CustomCheckWithStatusModel `tfsdk:"custom_checks"`
}

func (d *customChecksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_checks"
}

func (d *customChecksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the custom checks in the tenant, optionally filtered by the asset type they evaluate or by scope.",
		Attributes: map[string]schema.Attribute{
			"asset_type": schema.StringAttribute{
				MarkdownDescription: "Only return checks that evaluate this asset type, for example `AWS::S3::Bucket`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: "Only return checks with this scope: `batch` or `all`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(targetScopes...)},
			},
			"custom_checks": schema.ListNestedAttribute{
				MarkdownDescription: "The matching checks.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: customCheckComputedAttributes(),
				},
			},
		},
	}
}

func (d *customChecksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, resp)
}

func (d *customChecksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config customChecksDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	checks, err := d.client.ListAllCustomChecks(ctx, config.AssetType.ValueString(), config.Scope.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "list custom checks", err)
		return
	}

	config.CustomChecks = make([]CustomCheckWithStatusModel, 0, len(checks))
	for index := range checks {
		var item CustomCheckWithStatusModel
		item.fromAPI(&checks[index])
		config.CustomChecks = append(config.CustomChecks, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
