package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
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
	_ resource.Resource                     = &awsIntegrationResource{}
	_ resource.ResourceWithConfigure        = &awsIntegrationResource{}
	_ resource.ResourceWithImportState      = &awsIntegrationResource{}
	_ resource.ResourceWithConfigValidators = &awsIntegrationResource{}
)

// Plerion removes an integration in the background, so delete waits for it.
const integrationDeleteTimeout = 20 * time.Minute

// NewAWSIntegrationResource returns the plerion_aws_integration resource.
func NewAWSIntegrationResource() resource.Resource {
	return &awsIntegrationResource{}
}

type awsIntegrationResource struct {
	client *client.Client
}

type awsIntegrationModel struct {
	ID                          types.String `tfsdk:"id"`
	AccountID                   types.String `tfsdk:"account_id"`
	RoleARN                     types.String `tfsdk:"role_arn"`
	Name                        types.String `tfsdk:"name"`
	EnvironmentID               types.String `tfsdk:"environment_id"`
	InstanceProfileARN          types.String `tfsdk:"instance_profile_arn"`
	KMSKeyAccessMode            types.String `tfsdk:"kms_key_access_mode"`
	ServiceAccountID            types.String `tfsdk:"service_account_id"`
	ServiceAccountAccessRoleARN types.String `tfsdk:"service_account_access_role_arn"`
	ServiceAccountType          types.String `tfsdk:"service_account_type"`
	Status                      types.String `tfsdk:"status"`
}

func (r *awsIntegrationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aws_integration"
}

func (r *awsIntegrationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An AWS account connected to Plerion. Plerion assumes `role_arn` with " +
			"the tenant's external id before it saves anything, so the role must exist and trust Plerion first. " +
			"Plerion only assumes a role tagged `RolePurpose = PlerionAccess`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Integration id.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{
				MarkdownDescription: "AWS account id, taken from `role_arn`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"role_arn": schema.StringAttribute{
				MarkdownDescription: "ARN of the IAM role Plerion assumes to read the account. The role must " +
					"carry the tag `RolePurpose = PlerionAccess`. Pointing it at another AWS account replaces the integration.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.RegexMatches(roleARNPattern, "must be an IAM role ARN")},
				PlanModifiers: []planmodifier.String{replaceIfAccountChanges()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name. Defaults to the account alias, or the account id when it has none.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 64)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				MarkdownDescription: "Plerion environment to put the account in. Left out, the integration keeps " +
					"the environment it has, including one chosen in the Plerion console.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"instance_profile_arn": schema.StringAttribute{
				MarkdownDescription: "Instance profile for workload scanning in the same account. Must be in the same account as `role_arn`, " +
					"with no IAM path and only letters, digits, `-` and `_` in its name.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(instanceProfileARNPattern, "must be an IAM instance profile ARN with no path"),
				},
			},
			"kms_key_access_mode": schema.StringAttribute{
				MarkdownDescription: "Which KMS keys workload scanning may use: `ALL_KEYS` or `SELECTED_KEYS`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.OneOf("ALL_KEYS", "SELECTED_KEYS")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"service_account_id": schema.StringAttribute{
				MarkdownDescription: "AWS account id of the service account that scans this account's workloads.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(accountIDPattern, "must be a 12-digit AWS account id")},
			},
			"service_account_access_role_arn": schema.StringAttribute{
				MarkdownDescription: "Role in this account that the service account assumes. Required with `service_account_id`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(roleARNPattern, "must be an IAM role ARN")},
			},
			"service_account_type": schema.StringAttribute{
				MarkdownDescription: "Set to `PLERION_MANAGED` when Plerion runs the service account. Leave it " +
					"out when you run your own. Changing it replaces the integration.",
				Optional:      true,
				Validators:    []validator.String{stringvalidator.OneOf("PLERION_MANAGED")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "`active`, or `pending_approval` while Plerion waits for someone to approve " +
					"an account from an AWS organization the tenant hasn't seen before.",
				Computed: true,
			},
		},
	}
}

func (r *awsIntegrationResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.RequiredTogether(
			path.MatchRoot("service_account_id"),
			path.MatchRoot("service_account_access_role_arn"),
		),
		sameAccount{role: path.Root("role_arn"), other: path.Root("instance_profile_arn")},
		sameAccount{role: path.Root("role_arn"), other: path.Root("service_account_access_role_arn")},
	}
}

func (r *awsIntegrationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *awsIntegrationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan awsIntegrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := client.AWSIntegrationInput{
		RoleARN:                     plan.RoleARN.ValueString(),
		Name:                        plan.Name.ValueString(),
		EnvironmentID:               plan.EnvironmentID.ValueString(),
		InstanceProfileARN:          plan.InstanceProfileARN.ValueString(),
		CWPPKMSAccessMode:           plan.KMSKeyAccessMode.ValueString(),
		ServiceAccountID:            plan.ServiceAccountID.ValueString(),
		ServiceAccountAccessRoleARN: plan.ServiceAccountAccessRoleARN.ValueString(),
		ServiceAccountType:          plan.ServiceAccountType.ValueString(),
	}
	created, err := retryWhileRoleNotAssumable(ctx, func() (*client.AWSIntegration, error) {
		return r.client.CreateAWSIntegration(ctx, in)
	})
	if client.HasCode(err, "IntegrationAlreadyExists") {
		resp.Diagnostics.AddError("AWS account is already connected to Plerion",
			alreadyConnectedDetail(accountFromARN(in.RoleARN), err))
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "connect the AWS account", roleForIntegrations, err)
		return
	}

	plan.fromAPI(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if created.Status == statusPendingApproval {
		addApprovalWarning(&resp.Diagnostics, "The AWS integration")
	}
}

func (r *awsIntegrationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state awsIntegrationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetAWSIntegration(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "read the AWS integration", roleForIntegrations, err)
		return
	}

	state.fromAPI(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *awsIntegrationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state awsIntegrationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	changes := map[string]any{}
	addIfChanged(changes, "roleArn", plan.RoleARN, state.RoleARN)
	addIfChanged(changes, "name", plan.Name, state.Name)
	addIfChanged(changes, "environmentId", plan.EnvironmentID, state.EnvironmentID)
	addIfChanged(changes, "instanceProfileArn", plan.InstanceProfileARN, state.InstanceProfileARN)
	addIfChanged(changes, "cwppKMSAccessMode", plan.KMSKeyAccessMode, state.KMSKeyAccessMode)
	addIfChanged(changes, "serviceAccountId", plan.ServiceAccountID, state.ServiceAccountID)
	addIfChanged(changes, "serviceAccountAccessRoleArn", plan.ServiceAccountAccessRoleARN, state.ServiceAccountAccessRoleARN)

	id := state.ID.ValueString()
	var updated *client.AWSIntegration
	var err error
	if len(changes) == 0 {
		updated, err = r.client.GetAWSIntegration(ctx, id)
	} else {
		// A new role may not be assumable yet, the same as on create.
		updated, err = retryWhileRoleNotAssumable(ctx, func() (*client.AWSIntegration, error) {
			return r.client.UpdateAWSIntegration(ctx, id, changes)
		})
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "update the AWS integration", roleForIntegrations, err)
		return
	}

	plan.fromAPI(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *awsIntegrationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state awsIntegrationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	err := r.client.DeleteAWSIntegration(ctx, id)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "remove the AWS integration", roleForIntegrations, err)
		return
	}

	err = waitUntilGone(ctx, integrationDeleteTimeout, func(ctx context.Context) error {
		integration, err := r.client.GetAWSIntegration(ctx, id)
		if err == nil && integration.Status == statusDeleteFailed {
			return errDeleteFailed
		}
		return err
	})
	if errors.Is(err, errDeleteFailed) {
		resp.Diagnostics.AddError("Plerion could not remove the AWS integration",
			fmt.Sprintf("Plerion's removal of integration %s failed. Run terraform destroy again to retry, "+
				"or check the integration in the Plerion console.", id))
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "confirm the AWS integration was removed", roleForIntegrations, err)
	}
}

func (r *awsIntegrationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *awsIntegrationModel) fromAPI(in *client.AWSIntegration) {
	m.ID = types.StringValue(in.IntegrationID)
	m.AccountID = types.StringValue(in.AccountID)
	m.RoleARN = types.StringValue(in.RoleARN)
	m.Name = types.StringValue(in.Name)
	m.EnvironmentID = optionalString(in.EnvironmentID)
	m.InstanceProfileARN = optionalString(in.InstanceProfileARN)
	m.KMSKeyAccessMode = optionalString(in.CWPPKMSAccessMode)
	m.ServiceAccountID = optionalString(in.ServiceAccountID)
	m.ServiceAccountAccessRoleARN = optionalString(in.ServiceAccountAccessRoleARN)
	m.ServiceAccountType = optionalString(in.ServiceAccountType)
	m.Status = types.StringValue(in.Status)
}
