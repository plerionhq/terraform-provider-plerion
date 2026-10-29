package provider

import (
	"bytes"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
)

const (
	checkTypeRego = "rego"
)

var (
	severityLevels = []string{"INFORMATIONAL", "LOW", "MEDIUM", "HIGH", "CRITICAL"}
	targetScopes   = []string{"batch", "all"}
	checkTypes     = []string{checkTypeRego}
)

type targetModel struct {
	AssetType types.String `tfsdk:"asset_type"`
	Scope     types.String `tfsdk:"scope"`
}

type additionalAssetModel struct {
	AssetType types.String `tfsdk:"asset_type"`
}

type defaultsModel struct {
	SeverityLevel types.String `tfsdk:"severity_level"`
	Message       types.String `tfsdk:"message"`
	Informational types.Bool   `tfsdk:"informational"`
	Remediation   types.String `tfsdk:"remediation"`
	Meta          metaValue    `tfsdk:"meta"`
}

type runStatusModel struct {
	OverallStatus     types.String `tfsdk:"overall_status"`
	IntegrationCount  types.Int64  `tfsdk:"integration_count"`
	CompletedCount    types.Int64  `tfsdk:"completed_count"`
	FailedCount       types.Int64  `tfsdk:"failed_count"`
	TotalFindingCount types.Int64  `tfsdk:"total_finding_count"`
}

// CustomCheckModel holds the attributes shared by the resource and the data
// sources.
type CustomCheckModel struct {
	ID               types.String           `tfsdk:"id"`
	Slug             types.String           `tfsdk:"slug"`
	Title            types.String           `tfsdk:"title"`
	Description      types.String           `tfsdk:"description"`
	Target           *targetModel           `tfsdk:"target"`
	AdditionalAssets []additionalAssetModel `tfsdk:"additional_assets"`
	Defaults         *defaultsModel         `tfsdk:"defaults"`
	Type             types.String           `tfsdk:"type"`
	Body             types.String           `tfsdk:"body"`
	Version          types.Int64            `tfsdk:"version"`
	OrganizationID   types.String           `tfsdk:"organization_id"`
	TenantID         types.String           `tfsdk:"tenant_id"`
	CreatedAt        types.String           `tfsdk:"created_at"`
	CreatedBy        types.String           `tfsdk:"created_by"`
	UpdatedAt        types.String           `tfsdk:"updated_at"`
	UpdatedBy        types.String           `tfsdk:"updated_by"`
}

// CustomCheckWithStatusModel adds the run status the data sources report.
type CustomCheckWithStatusModel struct {
	CustomCheckModel

	LastRunAt types.String    `tfsdk:"last_run_at"`
	RunStatus *runStatusModel `tfsdk:"run_status"`
}

// toInput builds the API request body. The caller must have checked that
// target and defaults are set, which the schema guarantees for the resource.
func (m *CustomCheckModel) toInput() client.CustomCheckInput {
	input := client.CustomCheckInput{
		Slug:             m.Slug.ValueString(),
		Title:            m.Title.ValueString(),
		Description:      stringPointer(m.Description),
		Target:           expandTarget(m.Target),
		AdditionalAssets: expandAdditionalAssets(m.AdditionalAssets),
		Type:             m.Type.ValueString(),
		Body:             m.Body.ValueString(),
	}
	if input.Type == "" {
		input.Type = checkTypeRego
	}
	if defaults := expandDefaults(m.Defaults); defaults != nil {
		input.Defaults = *defaults
	}
	return input
}

func (m *CustomCheckModel) fromAPI(check *client.CustomCheck) {
	m.ID = types.StringValue(check.ID)
	m.Slug = types.StringValue(check.Slug)
	m.Title = types.StringValue(check.Title)
	m.Description = types.StringPointerValue(check.Description)
	m.Target = &targetModel{
		AssetType: types.StringValue(check.Target.AssetType),
		Scope:     types.StringValue(check.Target.Scope),
	}
	m.AdditionalAssets = flattenAdditionalAssets(check.AdditionalAssets)
	m.Defaults = &defaultsModel{
		SeverityLevel: types.StringValue(check.Defaults.SeverityLevel),
		Message:       types.StringValue(check.Defaults.Message),
		Informational: types.BoolValue(check.Defaults.Informational),
		Remediation:   types.StringPointerValue(check.Defaults.Remediation),
		Meta:          flattenMeta(check.Defaults.Meta),
	}
	m.Type = types.StringValue(check.Type)
	m.Body = types.StringValue(check.Body)
	m.Version = types.Int64Value(check.Version)
	m.OrganizationID = types.StringValue(check.OrganizationID)
	m.TenantID = types.StringValue(check.TenantID)
	m.CreatedAt = types.StringValue(check.CreatedAt)
	m.CreatedBy = types.StringValue(check.CreatedBy)
	m.UpdatedAt = types.StringValue(check.UpdatedAt)
	m.UpdatedBy = types.StringValue(check.UpdatedBy)
}

func (m *CustomCheckWithStatusModel) fromAPI(check *client.CustomCheck) {
	m.CustomCheckModel.fromAPI(check)
	m.LastRunAt = types.StringPointerValue(check.LastRunAt)
	m.RunStatus = nil
	if check.Rollup != nil {
		m.RunStatus = &runStatusModel{
			OverallStatus:     types.StringValue(check.Rollup.OverallStatus),
			IntegrationCount:  types.Int64Value(check.Rollup.IntegrationCount),
			CompletedCount:    types.Int64Value(check.Rollup.CompletedCount),
			FailedCount:       types.Int64Value(check.Rollup.FailedCount),
			TotalFindingCount: types.Int64Value(check.Rollup.TotalFindingCount),
		}
	}
}

func expandTarget(target *targetModel) client.Target {
	if target == nil {
		return client.Target{}
	}
	return client.Target{
		AssetType: target.AssetType.ValueString(),
		Scope:     target.Scope.ValueString(),
	}
}

func expandAdditionalAssets(assets []additionalAssetModel) []client.AdditionalAsset {
	if len(assets) == 0 {
		return nil
	}
	expanded := make([]client.AdditionalAsset, 0, len(assets))
	for _, asset := range assets {
		expanded = append(expanded, client.AdditionalAsset{AssetType: asset.AssetType.ValueString()})
	}
	return expanded
}

func flattenAdditionalAssets(assets []client.AdditionalAsset) []additionalAssetModel {
	if len(assets) == 0 {
		return nil
	}
	flattened := make([]additionalAssetModel, 0, len(assets))
	for _, asset := range assets {
		flattened = append(flattened, additionalAssetModel{AssetType: types.StringValue(asset.AssetType)})
	}
	return flattened
}

// expandDefaults sends informational as false when it is not set, because the
// API requires the field whenever defaults are present.
func expandDefaults(defaults *defaultsModel) *client.Defaults {
	if defaults == nil {
		return nil
	}
	expanded := &client.Defaults{
		SeverityLevel: defaults.SeverityLevel.ValueString(),
		Message:       defaults.Message.ValueString(),
		Informational: defaults.Informational.ValueBool(),
		Remediation:   stringPointer(defaults.Remediation),
	}
	if !defaults.Meta.IsNull() && !defaults.Meta.IsUnknown() {
		expanded.Meta = json.RawMessage(defaults.Meta.ValueString())
	}
	return expanded
}

// flattenMeta converts the API's metadata to the form jsonencode() produces.
func flattenMeta(meta json.RawMessage) metaValue {
	trimmed := bytes.TrimSpace(meta)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return newMetaNull()
	}
	return newMetaValue(canonicalJSON(trimmed))
}

func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	return value.ValueStringPointer()
}
