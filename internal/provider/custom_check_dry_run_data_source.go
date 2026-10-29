package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

const defaultDryRunTimeout = 5 * time.Minute

// dryRunPollInterval is a variable so tests can poll faster.
var dryRunPollInterval = 2 * time.Second

var (
	_ datasource.DataSource              = &customCheckDryRunDataSource{}
	_ datasource.DataSourceWithConfigure = &customCheckDryRunDataSource{}
)

// NewCustomCheckDryRunDataSource returns the plerion_custom_check_dry_run data source.
func NewCustomCheckDryRunDataSource() datasource.DataSource {
	return &customCheckDryRunDataSource{}
}

type customCheckDryRunDataSource struct {
	client *client.Client
}

type customCheckDryRunModel struct {
	IntegrationID    types.String           `tfsdk:"integration_id"`
	CustomCheckID    types.String           `tfsdk:"custom_check_id"`
	Slug             types.String           `tfsdk:"slug"`
	Title            types.String           `tfsdk:"title"`
	Description      types.String           `tfsdk:"description"`
	Target           *targetModel           `tfsdk:"target"`
	AdditionalAssets []additionalAssetModel `tfsdk:"additional_assets"`
	Defaults         *defaultsModel         `tfsdk:"defaults"`
	Type             types.String           `tfsdk:"type"`
	Body             types.String           `tfsdk:"body"`
	Timeout          types.String           `tfsdk:"timeout"`
	DryRunID         types.String           `tfsdk:"dry_run_id"`
	Status           types.String           `tfsdk:"status"`
	StartedAt        types.String           `tfsdk:"started_at"`
	CompletedAt      types.String           `tfsdk:"completed_at"`
	TotalFindings    types.Int64            `tfsdk:"total_findings"`
	Findings         jsontypes.Normalized   `tfsdk:"findings"`
}

func (d *customCheckDryRunDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_check_dry_run"
}

func (d *customCheckDryRunDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a custom check against the current assets of one integration and returns the findings it would " +
			"produce, without saving them. Use it to preview a new or changed policy before you apply it.\n\n" +
			"The check does not need to be saved first. A new dry run starts every time Terraform reads this data source, " +
			"which is usually on each plan. Reading fails if the policy cannot be evaluated, so a broken policy stops the plan.",
		Attributes: map[string]schema.Attribute{
			"integration_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the integration whose assets the check runs against. " +
					"The API key's role must be able to read assets in this integration.",
				Required:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"custom_check_id": schema.StringAttribute{
				MarkdownDescription: "The check ID that the preview findings refer to. Set it to a saved check's ID when you preview " +
					"a change to that check. When left out, the provider generates a new ID for each dry run.",
				Optional: true,
				Computed: true,
				Validators: []validator.String{stringvalidator.RegexMatches(uuidPattern,
					"must be a UUID, such as the id of a plerion_custom_check resource")},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "The check's slug. Optional for a dry run.",
				Optional:            true,
				Validators:          slugValidators(),
			},
			"title": schema.StringAttribute{
				MarkdownDescription: "The check's title. Optional for a dry run.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 256)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "The check's description. Optional for a dry run.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 4096)},
			},
			"target": schema.SingleNestedAttribute{
				MarkdownDescription: "The asset type the check evaluates. Takes the same values as the `plerion_custom_check` resource.",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"asset_type": schema.StringAttribute{
						MarkdownDescription: "The asset type to evaluate, for example `AWS::S3::Bucket`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"scope": schema.StringAttribute{
						MarkdownDescription: "Either `batch` or `all`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf(targetScopes...)},
					},
				},
			},
			"additional_assets": schema.ListNestedAttribute{
				MarkdownDescription: "Other asset types the policy needs to look up.",
				Optional:            true,
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"asset_type": schema.StringAttribute{
							MarkdownDescription: "The asset type to load, for example `AWS::IAM::Role`.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						},
					},
				},
			},
			"defaults": schema.SingleNestedAttribute{
				MarkdownDescription: "Values applied to each finding when the policy's result does not set them.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"severity_level": schema.StringAttribute{
						MarkdownDescription: "The finding severity. One of `INFORMATIONAL`, `LOW`, `MEDIUM`, `HIGH` or `CRITICAL`.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf(severityLevels...)},
					},
					"message": schema.StringAttribute{
						MarkdownDescription: "The finding message.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"informational": schema.BoolAttribute{
						MarkdownDescription: "Whether findings are informational only. Treated as `false` when left out.",
						Optional:            true,
					},
					"remediation": schema.StringAttribute{
						MarkdownDescription: "How to fix a failed asset.",
						Optional:            true,
						Validators:          []validator.String{stringvalidator.LengthBetween(1, 4096)},
					},
					"meta": schema.StringAttribute{
						MarkdownDescription: "Extra metadata added to each finding, as a JSON object. Set it with `jsonencode()`.",
						Optional:            true,
						CustomType:          metaType{},
						Validators:          []validator.String{jsonObjectValidator{}},
					},
				},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "The policy language. The only supported value is `rego`, which is used when this is left out.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(checkTypes...)},
			},
			"body": schema.StringAttribute{
				MarkdownDescription: "The rego policy to run.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"timeout": schema.StringAttribute{
				MarkdownDescription: "How long to wait for the dry run to finish, as a duration such as `90s` or `5m`. Defaults to `5m`.",
				Optional:            true,
				Validators:          []validator.String{durationValidator{}},
			},
			"dry_run_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the dry run.",
				Computed:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "The dry run's final status. Always `SUCCEEDED`, because any other outcome fails the read.",
				Computed:            true,
			},
			"started_at": schema.StringAttribute{
				MarkdownDescription: "When the dry run started, as an RFC 3339 timestamp.",
				Computed:            true,
			},
			"completed_at": schema.StringAttribute{
				MarkdownDescription: "When the dry run finished, as an RFC 3339 timestamp.",
				Computed:            true,
			},
			"total_findings": schema.Int64Attribute{
				MarkdownDescription: "How many findings the check would produce.",
				Computed:            true,
			},
			"findings": schema.StringAttribute{
				MarkdownDescription: "The findings the check would produce, as a JSON array. Read it with `jsondecode()`. " +
					"Each finding has the same shape as the findings the Plerion API returns.",
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
			},
		},
	}
}

func (d *customCheckDryRunDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, resp)
}

func (d *customCheckDryRunDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config customCheckDryRunModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	timeout := defaultDryRunTimeout
	if !config.Timeout.IsNull() {
		// The schema validator has already checked the format.
		timeout, _ = time.ParseDuration(strings.TrimSpace(config.Timeout.ValueString()))
	}

	if config.CustomCheckID.IsNull() {
		config.CustomCheckID = types.StringValue(uuid.NewString())
	}

	input := client.StartDryRunInput{
		IntegrationID: config.IntegrationID.ValueString(),
		Check: client.DryRunCheck{
			CustomCheckID:    config.CustomCheckID.ValueString(),
			Slug:             stringPointer(config.Slug),
			Title:            stringPointer(config.Title),
			Description:      stringPointer(config.Description),
			Target:           expandTarget(config.Target),
			AdditionalAssets: expandAdditionalAssets(config.AdditionalAssets),
			Defaults:         expandDefaults(config.Defaults),
			Type:             config.Type.ValueString(),
			Body:             config.Body.ValueString(),
		},
	}
	if input.Check.Type == "" {
		input.Check.Type = checkTypeRego
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started, err := d.client.StartCustomCheckDryRun(waitCtx, input)
	if err != nil {
		addDryRunAPIError(&resp.Diagnostics, "start the dry run", err)
		return
	}
	tflog.Info(ctx, "Started custom check dry run", map[string]any{"dry_run_id": started.DryRunID})

	dryRun, err := d.client.WaitForCustomCheckDryRun(waitCtx, started.DryRunID, dryRunPollInterval)
	if err != nil {
		// A single request can also time out, so check the wait's own deadline.
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			resp.Diagnostics.AddError("The dry run did not finish in time",
				fmt.Sprintf("Dry run %s was still running after %s. Set a longer timeout on the data source, "+
					"or target an asset type with fewer assets.", started.DryRunID, timeout))
			return
		}
		addDryRunAPIError(&resp.Diagnostics, "get the dry run's result", err)
		return
	}

	if message := dryRunFailure(dryRun); message != "" {
		resp.Diagnostics.AddError("The custom check could not be evaluated", message)
		return
	}

	findings, err := json.Marshal(nonNilFindings(dryRun.Findings))
	if err != nil {
		resp.Diagnostics.AddError("Could not read the dry run's findings", err.Error())
		return
	}

	config.DryRunID = types.StringValue(dryRun.DryRunID)
	config.Status = types.StringValue(dryRun.Status)
	config.StartedAt = types.StringValue(dryRun.StartedAt)
	config.CompletedAt = types.StringValue(dryRun.CompletedAt)
	config.TotalFindings = types.Int64Value(int64(len(dryRun.Findings)))
	if dryRun.Output != nil {
		config.TotalFindings = types.Int64Value(dryRun.Output.TotalFindings)
	}
	config.Findings = jsontypes.NewNormalizedValue(string(findings))

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// dryRunFailure explains why a finished dry run is not usable, or returns an
// empty string when it succeeded.
func dryRunFailure(dryRun *client.DryRun) string {
	var failures string
	if dryRun.Output != nil && len(dryRun.Output.Failures) > 0 {
		encoded, err := json.MarshalIndent(dryRun.Output.Failures, "", "  ")
		if err == nil {
			failures = "\n\nDetails from Plerion:\n" + string(encoded)
		}
	}

	switch {
	case dryRun.Status != client.DryRunSucceeded:
		return fmt.Sprintf("Dry run %s ended with the status %s, so there are no findings to preview. "+
			"Check the policy for errors and try again.%s", dryRun.DryRunID, dryRun.Status, failures)
	case dryRun.Output != nil && dryRun.Output.FailedCount > 0:
		return fmt.Sprintf("Dry run %s finished, but Plerion could not evaluate the policy. "+
			"Check that the policy declares `package custom`, defines a `result` rule, and returns a PASSED or FAILED status "+
			"only for assets in input.assets.%s", dryRun.DryRunID, failures)
	}
	return ""
}

func nonNilFindings(findings []json.RawMessage) []json.RawMessage {
	if findings == nil {
		return []json.RawMessage{}
	}
	return findings
}

func addDryRunAPIError(diagnostics *diag.Diagnostics, action string, err error) {
	if client.IsForbidden(err) {
		diagnostics.AddError("The API key does not have permission to run a dry run",
			fmt.Sprintf("Could not %s because the API key's role does not allow it (%s). "+
				"A dry run needs permission to manage custom checks and to read assets in the integration. "+
				"Give the key a role that has both, then try again.", action, err))
		return
	}
	addAPIError(diagnostics, action, err)
}

// durationValidator checks that a string parses as a positive Go duration.
type durationValidator struct{}

func (v durationValidator) Description(_ context.Context) string {
	return "value must be a positive duration such as 90s or 5m"
}

func (v durationValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v durationValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	duration, err := time.ParseDuration(strings.TrimSpace(req.ConfigValue.ValueString()))
	if err != nil || duration <= 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid timeout",
			fmt.Sprintf("%q is not a valid timeout. Use a positive duration such as 90s or 5m.", req.ConfigValue.ValueString()))
	}
}
