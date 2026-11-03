package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ resource.Resource                     = &awsServiceAccountResource{}
	_ resource.ResourceWithConfigure        = &awsServiceAccountResource{}
	_ resource.ResourceWithImportState      = &awsServiceAccountResource{}
	_ resource.ResourceWithConfigValidators = &awsServiceAccountResource{}
)

const serviceAccountDeleteTimeout = 5 * time.Minute

// NewAWSServiceAccountResource returns the plerion_aws_service_account resource.
func NewAWSServiceAccountResource() resource.Resource {
	return &awsServiceAccountResource{}
}

type awsServiceAccountResource struct {
	client *client.Client
}

type awsServiceAccountModel struct {
	ID                  types.String `tfsdk:"id"`
	AccountID           types.String `tfsdk:"account_id"`
	ControlPlaneRoleARN types.String `tfsdk:"control_plane_role_arn"`
	InstanceProfileARN  types.String `tfsdk:"instance_profile_arn"`
	Name                types.String `tfsdk:"name"`
	Status              types.String `tfsdk:"status"`
	Enabled             types.Bool   `tfsdk:"enabled"`
	AWSOrganizationID   types.String `tfsdk:"aws_organization_id"`
	TemplateVersion     types.String `tfsdk:"template_version"`
}

func (r *awsServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aws_service_account"
}

func (r *awsServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An AWS account you run Plerion's workload scanners in. Plerion assumes " +
			"`control_plane_role_arn` before it saves anything. Add the network for each region with " +
			"`plerion_aws_service_account_region`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `account_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{
				MarkdownDescription: "AWS account id, taken from `control_plane_role_arn`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"control_plane_role_arn": schema.StringAttribute{
				MarkdownDescription: "Role Plerion assumes to launch and manage scanners. The role must carry the " +
					"tag `RolePurpose = PlerionAccess`. Pointing it at another AWS account replaces the service account.",
				Required: true,
				Validators: []validator.String{stringvalidator.RegexMatches(serviceAccountRoleARNPattern,
					"must be an IAM role ARN with no path, and a name of letters, digits, - and _")},
				PlanModifiers: []planmodifier.String{replaceIfAccountChanges()},
			},
			"instance_profile_arn": schema.StringAttribute{
				MarkdownDescription: "Instance profile the scanners run with. Must be in the same account as the role.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(serviceAccountInstanceProfileARNPattern,
						"must be an IAM instance profile ARN with no path, and a name of letters, digits, - and _"),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Defaults to the account's IAM alias, or the account id.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{stringvalidator.RegexMatches(serviceAccountNamePattern,
					"must be 1 to 64 letters, digits, spaces, dots, - or _")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"template_version": schema.StringAttribute{
				MarkdownDescription: "Version of the Plerion template this was made from, so Plerion can tell when it's out of date. " +
					"The Plerion templates set it. Once set it is kept, even if you remove it from the configuration.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 32)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`active`, or `pending_approval` while Plerion waits for someone to approve " +
					"an account from an AWS organization the tenant hasn't seen before.",
				Computed: true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Plerion scans through this service account. Turned on and off in the Plerion console.",
				Computed:            true,
			},
			"aws_organization_id": schema.StringAttribute{
				MarkdownDescription: "AWS organization the account belongs to, as Plerion read it.",
				Computed:            true,
			},
		},
	}
}

func (r *awsServiceAccountResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		sameAccount{role: path.Root("control_plane_role_arn"), other: path.Root("instance_profile_arn")},
	}
}

func (r *awsServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *awsServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan awsServiceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := client.AWSServiceAccountInput{
		ControlPlaneRoleARN:         plan.ControlPlaneRoleARN.ValueString(),
		ApplianceInstanceProfileARN: plan.InstanceProfileARN.ValueString(),
		Name:                        plan.Name.ValueString(),
		TemplateVersion:             plan.TemplateVersion.ValueString(),
	}
	created, err := retryWhileRoleNotAssumable(ctx, func() (*client.AWSServiceAccount, error) {
		return r.client.CreateAWSServiceAccount(ctx, in)
	})
	if client.HasCode(err, "ServiceAccountAlreadyExists") {
		accountID := accountFromARN(in.ControlPlaneRoleARN)
		resp.Diagnostics.AddError("Service account is already registered with Plerion",
			"Account "+accountID+" is already a service account in this tenant. Bring it under Terraform with:\n\n"+
				"  terraform import <resource address> "+accountID)
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "register the service account", roleForServiceAccounts, err)
		return
	}

	plan.fromAPI(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if created.Status == statusPendingApproval {
		addApprovalWarning(&resp.Diagnostics, "The service account")
	}
}

func (r *awsServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state awsServiceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetAWSServiceAccount(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "read the service account", roleForServiceAccounts, err)
		return
	}

	state.fromAPI(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *awsServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state awsServiceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	changes := map[string]any{}
	addIfChanged(changes, "controlPlaneRoleArn", plan.ControlPlaneRoleARN, state.ControlPlaneRoleARN)
	addIfChanged(changes, "applianceInstanceProfileArn", plan.InstanceProfileARN, state.InstanceProfileARN)
	addIfChanged(changes, "name", plan.Name, state.Name)
	addIfChanged(changes, "templateVersion", plan.TemplateVersion, state.TemplateVersion)

	accountID := state.ID.ValueString()
	var updated *client.AWSServiceAccount
	var err error
	if len(changes) == 0 {
		updated, err = r.client.GetAWSServiceAccount(ctx, accountID)
	} else {
		updated, err = retryWhileRoleNotAssumable(ctx, func() (*client.AWSServiceAccount, error) {
			return r.client.UpdateAWSServiceAccount(ctx, accountID, changes)
		})
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "update the service account", roleForServiceAccounts, err)
		return
	}

	plan.fromAPI(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *awsServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state awsServiceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	accountID := state.ID.ValueString()
	err := r.client.DeleteAWSServiceAccount(ctx, accountID)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "remove the service account", roleForServiceAccounts, err)
		return
	}

	err = waitUntilGone(ctx, serviceAccountDeleteTimeout, func(ctx context.Context) error {
		_, err := r.client.GetAWSServiceAccount(ctx, accountID)
		return err
	})
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "confirm the service account was removed", roleForServiceAccounts, err)
	}
}

func (r *awsServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *awsServiceAccountModel) fromAPI(in *client.AWSServiceAccount) {
	m.ID = types.StringValue(in.AccountID)
	m.AccountID = types.StringValue(in.AccountID)
	m.ControlPlaneRoleARN = types.StringValue(in.ControlPlaneRoleARN)
	m.InstanceProfileARN = types.StringValue(in.ApplianceInstanceProfileARN)
	m.Name = types.StringValue(in.Name)
	m.Status = types.StringValue(in.Status)
	m.Enabled = types.BoolValue(in.Enabled)
	m.AWSOrganizationID = optionalString(in.AWSOrganizationID)
	m.TemplateVersion = optionalString(in.TemplateVersion)
}
