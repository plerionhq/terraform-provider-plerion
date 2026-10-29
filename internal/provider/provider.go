// Package provider implements the Plerion Terraform provider.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

const (
	// DefaultAPIURL is used when neither api_url nor PLERION_API_URL is set.
	DefaultAPIURL = "https://au.api.plerion.com"

	apiKeyEnvVar = "PLERION_API_KEY"
	apiURLEnvVar = "PLERION_API_URL"
)

var _ provider.Provider = &plerionProvider{}

type plerionProvider struct {
	version string
}

type plerionProviderModel struct {
	APIKey types.String `tfsdk:"api_key"`
	APIURL types.String `tfsdk:"api_url"`
}

// New returns a constructor for the provider at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &plerionProvider{version: version}
	}
}

func (p *plerionProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "plerion"
	resp.Version = p.version
}

func (p *plerionProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Plerion resources through the Plerion customer API.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "A Plerion tenant API key. Create one in the Plerion app under **Settings > API Keys**. " +
					"Can also be set with the `" + apiKeyEnvVar + "` environment variable. " +
					"API keys only work in the region where they were created, so the key and `api_url` must match.",
				Optional:  true,
				Sensitive: true,
			},
			"api_url": schema.StringAttribute{
				MarkdownDescription: "The Plerion API address for your region, for example `https://us1.api.plerion.com`. " +
					"Can also be set with the `" + apiURLEnvVar + "` environment variable. Defaults to `" + DefaultAPIURL + "`.",
				Optional: true,
			},
		},
	}
}

func (p *plerionProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config plerionProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "The API key is not known yet",
			"The provider needs its API key while Terraform is planning, but this value is only known after apply. "+
				"Set api_key from a variable or the "+apiKeyEnvVar+" environment variable instead.")
	}
	if config.APIURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_url"), "The API address is not known yet",
			"The provider needs its API address while Terraform is planning, but this value is only known after apply. "+
				"Set api_url from a variable or the "+apiURLEnvVar+" environment variable instead.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := firstNonEmpty(config.APIKey.ValueString(), os.Getenv(apiKeyEnvVar))
	apiURL := firstNonEmpty(config.APIURL.ValueString(), os.Getenv(apiURLEnvVar), DefaultAPIURL)

	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing Plerion API key",
			"Set api_key in the provider block or the "+apiKeyEnvVar+" environment variable. "+
				"Create a tenant API key in the Plerion app under Settings > API Keys.")
		return
	}

	userAgent := fmt.Sprintf("terraform-provider-plerion/%s (+https://registry.terraform.io/providers/plerionhq/plerion)", p.version)
	if req.TerraformVersion != "" {
		userAgent += " Terraform/" + req.TerraformVersion
	}

	apiClient, err := client.New(client.Config{
		BaseURL:   apiURL,
		APIKey:    apiKey,
		UserAgent: userAgent,
	})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("api_url"), "Invalid Plerion API address", err.Error()+".")
		return
	}

	resp.ResourceData = apiClient
	resp.DataSourceData = apiClient
}

func (p *plerionProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewCustomCheckResource,
	}
}

func (p *plerionProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCustomCheckDataSource,
		NewCustomChecksDataSource,
		NewCustomCheckDryRunDataSource,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
