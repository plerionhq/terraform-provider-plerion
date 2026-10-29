package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ datasource.DataSource                     = &customCheckDataSource{}
	_ datasource.DataSourceWithConfigure        = &customCheckDataSource{}
	_ datasource.DataSourceWithConfigValidators = &customCheckDataSource{}
)

// NewCustomCheckDataSource returns the plerion_custom_check data source.
func NewCustomCheckDataSource() datasource.DataSource {
	return &customCheckDataSource{}
}

type customCheckDataSource struct {
	client *client.Client
}

func (d *customCheckDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_check"
}

func (d *customCheckDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := customCheckComputedAttributes()
	attributes["id"] = schema.StringAttribute{
		MarkdownDescription: "The ID of the check to look up. Set either `id` or `slug`.",
		Optional:            true,
		Computed:            true,
		Validators:          []validator.String{stringvalidator.RegexMatches(uuidPattern, "must be a custom check ID, which is a UUID")},
	}
	attributes["slug"] = schema.StringAttribute{
		MarkdownDescription: "The slug of the check to look up. Set either `id` or `slug`.",
		Optional:            true,
		Computed:            true,
		Validators:          slugValidators(),
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one custom check by its ID or its slug, including a summary of its latest run.",
		Attributes:          attributes,
	}
}

func (d *customCheckDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("slug")),
	}
}

func (d *customCheckDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, resp)
}

func (d *customCheckDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config CustomCheckWithStatusModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var (
		check *client.CustomCheck
		err   error
	)
	if !config.ID.IsNull() {
		check, err = d.client.GetCustomCheck(ctx, config.ID.ValueString())
	} else {
		check, err = d.client.FindCustomCheckBySlug(ctx, config.Slug.ValueString())
	}
	if err != nil {
		if client.IsNotFound(err) {
			lookup := fmt.Sprintf("the ID %q", config.ID.ValueString())
			if config.ID.IsNull() {
				lookup = fmt.Sprintf("the slug %q", config.Slug.ValueString())
			}
			resp.Diagnostics.AddError("Custom check not found",
				fmt.Sprintf("No custom check in this tenant has %s. "+
					"Check the value, and check that the API key belongs to the tenant that owns the check.", lookup))
			return
		}
		addAPIError(&resp.Diagnostics, "read the custom check", err)
		return
	}

	config.fromAPI(check)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
