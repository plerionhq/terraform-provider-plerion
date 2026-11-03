package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

var (
	_ resource.Resource                = &awsServiceAccountRegionResource{}
	_ resource.ResourceWithConfigure   = &awsServiceAccountRegionResource{}
	_ resource.ResourceWithImportState = &awsServiceAccountRegionResource{}
)

var (
	awsRegionPattern       = regexp.MustCompile(`^[a-z]{2}-[a-z]{4,9}-[1-9]$`)
	vpcIDPattern           = regexp.MustCompile(`^vpc-[a-zA-Z0-9]{1,36}$`)
	subnetIDPattern        = regexp.MustCompile(`^subnet-[a-zA-Z0-9]{1,36}$`)
	securityGroupIDPattern = regexp.MustCompile(`^sg-[a-zA-Z0-9]{1,36}$`)
	queueURLPattern        = regexp.MustCompile(`^https://sqs\.[a-z]{2}-[a-z]{4,9}-[1-9]\.amazonaws\.com/\d{12}/[a-zA-Z0-9_-]{1,200}$`)
)

// NewAWSServiceAccountRegionResource returns the plerion_aws_service_account_region resource.
func NewAWSServiceAccountRegionResource() resource.Resource {
	return &awsServiceAccountRegionResource{}
}

type awsServiceAccountRegionResource struct {
	client *client.Client
}

type awsServiceAccountRegionModel struct {
	ID               types.String `tfsdk:"id"`
	AccountID        types.String `tfsdk:"account_id"`
	Region           types.String `tfsdk:"region"`
	VPCID            types.String `tfsdk:"vpc_id"`
	SubnetID         types.String `tfsdk:"subnet_id"`
	SecurityGroupID  types.String `tfsdk:"security_group_id"`
	QueueURL         types.String `tfsdk:"queue_url"`
	MaxInstanceLimit types.Int64  `tfsdk:"max_instance_limit"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	TemplateVersion  types.String `tfsdk:"template_version"`
}

func (r *awsServiceAccountRegionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_aws_service_account_region"
}

func (r *awsServiceAccountRegionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiredID := func(description string, pattern *regexp.Regexp, message string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: description,
			Required:            true,
			Validators:          []validator.String{stringvalidator.RegexMatches(pattern, message)},
		}
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "The network a service account's scanners run in, for one AWS region. Plerion " +
			"checks through the service account's role that the VPC is tagged with your tenant id and that " +
			"the subnet, security group and queue belong to it. Turning the region on and off stays in the " +
			"Plerion console.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<account_id>/<region>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{
				MarkdownDescription: "AWS account id of the service account.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(accountIDPattern, "must be a 12-digit AWS account id")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "AWS region, for example `ap-southeast-2`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(awsRegionPattern, "must be an AWS region")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"vpc_id":            requiredID("VPC the scanners run in. It must carry the `PlerionTenantId` tag.", vpcIDPattern, "must be a VPC id"),
			"subnet_id":         requiredID("Subnet in that VPC.", subnetIDPattern, "must be a subnet id"),
			"security_group_id": requiredID("Security group in that VPC.", securityGroupIDPattern, "must be a security group id"),
			"queue_url":         requiredID("URL of the scan queue in this account and region.", queueURLPattern, "must be an SQS queue URL"),
			"max_instance_limit": schema.Int64Attribute{
				MarkdownDescription: "Most scanners that may run at once in this region, from 1 to 100. Defaults to 10.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(10),
				Validators:          []validator.Int64{int64validator.Between(1, 100)},
			},
			"template_version": schema.StringAttribute{
				MarkdownDescription: "Version of the Plerion template this was made from, so Plerion can tell when it's out of date. " +
					"The Plerion templates set it. Once set it is kept, even if you remove it from the configuration.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{stringvalidator.LengthBetween(1, 32)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Plerion scans in this region. Turned on and off in the Plerion console.",
				Computed:            true,
			},
		},
	}
}

func (r *awsServiceAccountRegionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *awsServiceAccountRegionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan awsServiceAccountRegionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &plan, &resp.State, &resp.Diagnostics, "add the service account region")
}

func (r *awsServiceAccountRegionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state awsServiceAccountRegionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	current, err := r.client.GetAWSServiceAccountRegion(ctx, state.AccountID.ValueString(), state.Region.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIErrorWithRole(&resp.Diagnostics, "read the service account region", roleForServiceAccounts, err)
		return
	}

	state.fromAPI(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *awsServiceAccountRegionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan awsServiceAccountRegionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &plan, &resp.State, &resp.Diagnostics, "update the service account region")
}

func (r *awsServiceAccountRegionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state awsServiceAccountRegionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteAWSServiceAccountRegion(ctx, state.AccountID.ValueString(), state.Region.ValueString())
	if err != nil && !client.IsNotFound(err) {
		addAPIErrorWithRole(&resp.Diagnostics, "remove the service account region", roleForServiceAccounts, err)
	}
}

// ImportState takes an id of the form <account_id>/<region>.
func (r *awsServiceAccountRegionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	accountID, region, ok := strings.Cut(req.ID, "/")
	if !ok || !accountIDPattern.MatchString(accountID) || !awsRegionPattern.MatchString(region) {
		resp.Diagnostics.AddError("Invalid import id",
			"Use <account_id>/<region>, for example 123456789012/ap-southeast-2.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("account_id"), accountID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("region"), region)...)
}

// put creates or replaces the region. The API answers a held service account
// with a 409 that says to approve it first, which is shown as is.
func (r *awsServiceAccountRegionResource) put(ctx context.Context, plan *awsServiceAccountRegionModel, state *tfsdk.State, diags *diag.Diagnostics, action string) {
	in := client.AWSServiceAccountRegionInput{
		VPCID:            plan.VPCID.ValueString(),
		SubnetID:         plan.SubnetID.ValueString(),
		SecurityGroupID:  plan.SecurityGroupID.ValueString(),
		QueueURL:         plan.QueueURL.ValueString(),
		MaxInstanceLimit: plan.MaxInstanceLimit.ValueInt64(),
		TemplateVersion:  plan.TemplateVersion.ValueString(),
	}
	saved, err := r.client.PutAWSServiceAccountRegion(ctx, plan.AccountID.ValueString(), plan.Region.ValueString(), in)
	if err != nil {
		addAPIErrorWithRole(diags, action, roleForServiceAccounts, err)
		return
	}

	plan.fromAPI(saved)
	diags.Append(state.Set(ctx, plan)...)
}

func (m *awsServiceAccountRegionModel) fromAPI(in *client.AWSServiceAccountRegion) {
	m.ID = types.StringValue(in.AccountID + "/" + in.Region)
	m.AccountID = types.StringValue(in.AccountID)
	m.Region = types.StringValue(in.Region)
	m.VPCID = types.StringValue(in.VPCID)
	m.SubnetID = types.StringValue(in.SubnetID)
	m.SecurityGroupID = types.StringValue(in.SecurityGroupID)
	m.QueueURL = types.StringValue(in.QueueURL)
	m.MaxInstanceLimit = types.Int64Value(in.MaxInstanceLimit)
	m.Enabled = types.BoolValue(in.Enabled)
	m.TemplateVersion = optionalString(in.TemplateVersion)
}
