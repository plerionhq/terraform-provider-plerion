package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ resource.Resource                = &customCheckResource{}
	_ resource.ResourceWithConfigure   = &customCheckResource{}
	_ resource.ResourceWithImportState = &customCheckResource{}
)

// NewCustomCheckResource returns the plerion_custom_check resource.
func NewCustomCheckResource() resource.Resource {
	return &customCheckResource{}
}

type customCheckResource struct {
	client *client.Client
}

func (r *customCheckResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_check"
}

func (r *customCheckResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepValue := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom check. A custom check is a rego policy that Plerion evaluates against the assets it " +
			"collects from your integrations. The policy reports assets as passed or failed, and each report becomes a finding.\n\n" +
			"Plerion runs a check after each scan, and again straight after the check is created or changed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID Plerion assigned to the check.",
				Computed:            true,
				PlanModifiers:       keepValue,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "A name for the check that is unique within the tenant. " +
					"Use lowercase letters, numbers and hyphens, up to 128 characters. You can change it later without replacing the check.",
				Required:   true,
				Validators: slugValidators(),
			},
			"title": schema.StringAttribute{
				MarkdownDescription: "The title shown for the check in the Plerion app. Up to 256 characters.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 256)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "What the check looks for and why it matters. Up to 4,096 characters.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 4096)},
			},
			"target": schema.SingleNestedAttribute{
				MarkdownDescription: "The asset type the check evaluates. Each asset of this type is passed to the policy in `input.assets`.",
				Required:            true,
				Attributes:          targetResourceAttributes(),
			},
			"additional_assets": schema.ListNestedAttribute{
				MarkdownDescription: "Other asset types the policy needs to look up, such as the IAM roles a Lambda function uses. " +
					"Every asset of each type is loaded and passed to the policy in `input.additionalAssets`, keyed by asset type. " +
					"Leave this out rather than setting an empty list.",
				Optional:   true,
				Validators: []validator.List{listvalidator.SizeAtLeast(1)},
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
				Required:            true,
				Attributes:          defaultsResourceAttributes(),
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "The policy language. The only supported value is `rego`, which is the default.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(checkTypeRego),
				Validators:          []validator.String{stringvalidator.OneOf(checkTypes...)},
			},
			"body": schema.StringAttribute{
				MarkdownDescription: "The rego policy. It must declare `package custom` and define a `result` rule that " +
					"returns a list of verdicts, one for each asset to report on. " +
					"Load it from a file with `file(\"${path.module}/policies/my-check.rego\")` to keep the policy readable.",
				Required:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"version": schema.Int64Attribute{
				MarkdownDescription: "The check's version. It starts at 1 and goes up by one each time the check changes.",
				Computed:            true,
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the organization that owns the check.",
				Computed:            true,
				PlanModifiers:       keepValue,
			},
			"tenant_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the tenant that owns the check.",
				Computed:            true,
				PlanModifiers:       keepValue,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the check was created, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       keepValue,
			},
			"created_by": schema.StringAttribute{
				MarkdownDescription: "The ID of the user or API key that created the check.",
				Computed:            true,
				PlanModifiers:       keepValue,
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the check last changed, as an RFC 3339 timestamp.",
				Computed:            true,
			},
			"updated_by": schema.StringAttribute{
				MarkdownDescription: "The ID of the user or API key that last changed the check.",
				Computed:            true,
			},
		},
	}
}

func targetResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"asset_type": schema.StringAttribute{
			MarkdownDescription: "The asset type to evaluate, for example `AWS::S3::Bucket`.",
			Required:            true,
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"scope": schema.StringAttribute{
			MarkdownDescription: "How assets are passed to the policy. With `batch`, Plerion splits the assets into chunks and " +
				"evaluates each chunk separately, which suits checks that look at one asset at a time. With `all`, the policy " +
				"sees every asset of the type in one evaluation, which suits checks that compare assets with each other.",
			Required:   true,
			Validators: []validator.String{stringvalidator.OneOf(targetScopes...)},
		},
	}
}

func defaultsResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
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
			MarkdownDescription: "Whether findings are informational only. Defaults to `false`.",
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
		},
		"remediation": schema.StringAttribute{
			MarkdownDescription: "How to fix a failed asset. Shown on failed findings only. Up to 4,096 characters.",
			Optional:            true,
			Validators:          []validator.String{stringvalidator.LengthBetween(1, 4096)},
		},
		"meta": schema.StringAttribute{
			MarkdownDescription: "Extra metadata added to each finding, as a JSON object. Set it with `jsonencode()`. " +
				"Keys the policy's result sets for a finding take precedence.",
			Optional:   true,
			CustomType: metaType{},
			Validators: []validator.String{jsonObjectValidator{}},
		},
	}
}

func (r *customCheckResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	apiClient, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *client.Client but got %T. This is a bug in the provider; please report it.", req.ProviderData))
		return
	}
	r.client = apiClient
}

func (r *customCheckResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CustomCheckModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	check, err := r.client.CreateCustomCheck(ctx, plan.toInput())
	if err != nil {
		if client.IsConflict(err) {
			addSlugConflictError(&resp.Diagnostics, plan.Slug.ValueString(), err)
			return
		}
		addAPIError(&resp.Diagnostics, "create the custom check", err)
		return
	}

	tflog.Info(ctx, "Created custom check", map[string]any{"id": check.ID, "slug": check.Slug})

	plan.fromAPI(check)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *customCheckResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CustomCheckModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	check, err := r.client.GetCustomCheck(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			tflog.Warn(ctx, "Custom check no longer exists; removing it from state", map[string]any{"id": state.ID.ValueString()})
			resp.State.RemoveResource(ctx)
			return
		}
		addAPIError(&resp.Diagnostics, "read the custom check", err)
		return
	}

	state.fromAPI(check)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *customCheckResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state CustomCheckModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	check, err := r.client.UpdateCustomCheck(ctx, state.ID.ValueString(), plan.toInput())
	if err != nil {
		switch {
		case client.IsConflict(err):
			addSlugConflictError(&resp.Diagnostics, plan.Slug.ValueString(), err)
		case client.IsNotFound(err):
			resp.Diagnostics.AddError("The custom check no longer exists",
				"The check was deleted outside Terraform while this change was being applied. "+
					"Run terraform apply again to create it.")
		default:
			addAPIError(&resp.Diagnostics, "update the custom check", err)
		}
		return
	}

	tflog.Info(ctx, "Updated custom check", map[string]any{"id": check.ID, "version": check.Version})

	plan.fromAPI(check)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *customCheckResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CustomCheckModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteCustomCheck(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "delete the custom check", err)
		return
	}

	tflog.Info(ctx, "Deleted custom check", map[string]any{"id": state.ID.ValueString()})
}

// ImportState accepts either the check's ID or its slug.
func (r *customCheckResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importID := strings.TrimSpace(req.ID)
	if importID == "" {
		resp.Diagnostics.AddError("Missing import ID", "Import a custom check by its ID or by its slug.")
		return
	}

	if uuidPattern.MatchString(importID) {
		check, getErr := r.client.GetCustomCheck(ctx, importID)
		switch {
		case getErr == nil:
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), check.ID)...)
			return
		case !client.IsNotFound(getErr):
			addAPIError(&resp.Diagnostics, "import the custom check", getErr)
			return
		}
		// A slug can look like an ID, so fall back to a slug lookup.
	}

	check, err := r.client.FindCustomCheckBySlug(ctx, importID)
	if err != nil {
		if client.IsNotFound(err) {
			resp.Diagnostics.AddError("Custom check not found",
				fmt.Sprintf("No custom check in this tenant has the ID or slug %q. "+
					"Check the value, and check that the API key belongs to the tenant that owns the check.", importID))
			return
		}
		addAPIError(&resp.Diagnostics, "import the custom check", err)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), check.ID)...)
}
