package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

// How long the provider waits, and how often it looks. Tests shorten the intervals.
var (
	pollInterval      = 10 * time.Second
	roleRetryInterval = 5 * time.Second
)

const (
	// New IAM roles can take a minute or two before STS lets Plerion assume them.
	roleAssumeTimeout = 2 * time.Minute

	statusPendingApproval = "pending_approval"
	codeRoleNotAssumable  = "RoleNotAssumable"
)

var (
	roleARNPattern = regexp.MustCompile(`^arn:aws:iam::\d{12}:role/[\w+=,.@/-]+$`)
	// Plerion stores a workload-scanning profile only when it has no IAM path
	// and only letters, digits, - and _ in the name.
	instanceProfileARNPattern = regexp.MustCompile(`^arn:aws:iam::\d{12}:instance-profile/[a-zA-Z0-9_-]{1,128}$`)
	accountIDPattern          = regexp.MustCompile(`^\d{12}$`)

	// Service accounts are stricter: Plerion's workload service takes no IAM
	// path and only letters, digits, - and _ in the name.
	serviceAccountRoleARNPattern            = regexp.MustCompile(`^arn:aws:iam::\d{12}:role/[a-zA-Z0-9_-]{1,100}$`)
	serviceAccountInstanceProfileARNPattern = regexp.MustCompile(`^arn:aws:iam::\d{12}:instance-profile/[a-zA-Z0-9_-]{1,100}$`)
	serviceAccountNamePattern               = regexp.MustCompile(`^[a-zA-Z0-9_ .-]{1,64}$`)
	arnAccountPattern                       = regexp.MustCompile(`^arn:aws:iam::(\d{12}):`)

	// Integration ids are UUIDs, and Plerion's already-connected message names
	// the existing one.
	integrationIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// alreadyConnectedDetail tells the customer how to import the integration an
// account already has, naming its id when the API's message does.
func alreadyConnectedDetail(accountID string, err error) string {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		if id := integrationIDPattern.FindString(apiErr.Detail()); id != "" {
			return "Account " + accountID + " already has integration " + id + " in this tenant. " +
				"Bring it under Terraform with:\n\n  terraform import <resource address> " + id
		}
	}
	return "Account " + accountID + " already has an integration in this tenant. Find its " +
		"integration id in the Plerion console and bring it under Terraform with:\n\n" +
		"  terraform import <resource address> <integration id>"
}

// accountFromARN returns the AWS account id inside an IAM ARN, or "".
func accountFromARN(arn string) string {
	if m := arnAccountPattern.FindStringSubmatch(arn); m != nil {
		return m[1]
	}
	return ""
}

// replaceIfAccountChanges plans a new resource when an IAM ARN moves to
// another AWS account. A Plerion record belongs to one account, so it can't be
// edited across accounts. A new role in the same account is an update.
func replaceIfAccountChanges() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			if req.StateValue.IsNull() || req.PlanValue.IsUnknown() || req.PlanValue.IsNull() {
				return
			}
			resp.RequiresReplace = accountFromARN(req.StateValue.ValueString()) != accountFromARN(req.PlanValue.ValueString())
		},
		"Replaces the resource when the ARN points at another AWS account.",
		"Replaces the resource when the ARN points at another AWS account.",
	)
}

// sameAccount checks that two IAM ARNs are in the same AWS account. An
// appliance can't use an instance profile from another account.
type sameAccount struct {
	role, other path.Path
}

func (v sameAccount) Description(_ context.Context) string {
	return fmt.Sprintf("%s must be in the same AWS account as %s", v.other, v.role)
}

func (v sameAccount) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v sameAccount) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var role, other types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, v.role, &role)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, v.other, &other)...)
	if resp.Diagnostics.HasError() || role.IsNull() || role.IsUnknown() || other.IsNull() || other.IsUnknown() {
		return
	}
	if accountFromARN(role.ValueString()) != accountFromARN(other.ValueString()) {
		resp.Diagnostics.AddAttributeError(v.other, "ARN in another AWS account", v.Description(ctx)+".")
	}
}

// clientFrom unpacks the client the provider handed to a resource or data source.
func clientFrom(providerData any, diags *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	c, ok := providerData.(*client.Client)
	if !ok {
		diags.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", providerData),
		)
		return nil
	}
	return c
}

// addApprovalWarning explains a record Plerion is holding for approval.
func addApprovalWarning(diags *diag.Diagnostics, what string) {
	diags.AddWarning(
		what+" is waiting for approval",
		"This AWS account is in an AWS organization this Plerion tenant hasn't seen before, so "+
			"Plerion is holding it until someone approves it in the Plerion console. Plerion doesn't "+
			"scan through it until then.",
	)
}

// retryWhileRoleNotAssumable repeats call while Plerion can't assume a new role yet.
func retryWhileRoleNotAssumable[T any](ctx context.Context, call func() (T, error)) (T, error) {
	deadline := time.Now().Add(roleAssumeTimeout)
	for {
		out, err := call()
		if err == nil || !client.HasCode(err, codeRoleNotAssumable) || time.Now().After(deadline) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(roleRetryInterval):
		}
	}
}

// waitUntilGone polls get until the API answers 404 or timeout passes.
func waitUntilGone(ctx context.Context, timeout time.Duration, get func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		err := get(ctx)
		if client.IsNotFound(err) {
			return nil
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still present in Plerion after %s", timeout)
		case <-time.After(pollInterval):
		}
	}
}

// optionalString maps an empty API value to null, so unset attributes stay unset.
func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// addIfChanged records a changed attribute for a PATCH body. An attribute
// removed from the configuration is sent as null, which clears it.
func addIfChanged(changes map[string]any, key string, plan, state types.String) {
	if plan.IsUnknown() || plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		changes[key] = nil
		return
	}
	changes[key] = plan.ValueString()
}
