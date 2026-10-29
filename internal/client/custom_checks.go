package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	customChecksPath = "/v1/tenant/custom-checks"
	dryRunsPath      = "/v1/tenant/custom-check-dry-runs"

	// ListPageSize is the largest page the list endpoint returns.
	ListPageSize = 200
)

// Target is the primary asset type a check evaluates.
type Target struct {
	AssetType string `json:"assetType"`
	Scope     string `json:"scope"`
}

// AdditionalAsset is an extra asset type loaded in full for the policy.
type AdditionalAsset struct {
	AssetType string `json:"assetType"`
}

// Defaults are the values applied to findings when the policy leaves them out.
type Defaults struct {
	SeverityLevel string          `json:"severityLevel"`
	Message       string          `json:"message"`
	Informational bool            `json:"informational"`
	Remediation   *string         `json:"remediation,omitempty"`
	Meta          json.RawMessage `json:"meta,omitempty"`
}

// CustomCheckInput is the writable part of a custom check. The API treats an
// update as a full replacement, so a field left out here is removed.
type CustomCheckInput struct {
	Slug             string            `json:"slug"`
	Title            string            `json:"title"`
	Description      *string           `json:"description,omitempty"`
	Target           Target            `json:"target"`
	AdditionalAssets []AdditionalAsset `json:"additionalAssets,omitempty"`
	Defaults         Defaults          `json:"defaults"`
	Type             string            `json:"type"`
	Body             string            `json:"body"`
}

// RunStatus summarizes the latest run of a check across integrations.
type RunStatus struct {
	OverallStatus     string `json:"overallStatus"`
	IntegrationCount  int64  `json:"integrationCount"`
	CompletedCount    int64  `json:"completedCount"`
	FailedCount       int64  `json:"failedCount"`
	TotalFindingCount int64  `json:"totalFindingCount"`
}

// CustomCheck is a stored custom check.
type CustomCheck struct {
	CustomCheckInput

	ID             string     `json:"customCheckId"`
	OrganizationID string     `json:"organizationId"`
	TenantID       string     `json:"tenantId"`
	Version        int64      `json:"version"`
	CreatedBy      string     `json:"createdBy"`
	CreatedAt      string     `json:"createdAt"`
	UpdatedBy      string     `json:"updatedBy"`
	UpdatedAt      string     `json:"updatedAt"`
	LastRunAt      *string    `json:"lastRunAt,omitempty"`
	Rollup         *RunStatus `json:"rollup,omitempty"`
}

// ListCustomChecksOptions filters and pages the list endpoint.
type ListCustomChecksOptions struct {
	AssetType string
	Scope     string
	Limit     int
	Cursor    string
}

// CustomCheckPage is one page of the list endpoint.
type CustomCheckPage struct {
	Items      []CustomCheck `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// CreateCustomCheck creates a check. The API returns 409 when the slug is
// already used in the tenant.
func (c *Client) CreateCustomCheck(ctx context.Context, input CustomCheckInput) (*CustomCheck, error) {
	var check CustomCheck
	if err := c.do(ctx, http.MethodPost, customChecksPath, nil, input, &check); err != nil {
		return nil, err
	}
	return &check, nil
}

// GetCustomCheck fetches one check by ID.
func (c *Client) GetCustomCheck(ctx context.Context, id string) (*CustomCheck, error) {
	var check CustomCheck
	if err := c.do(ctx, http.MethodGet, customChecksPath+"/"+url.PathEscape(id), nil, nil, &check); err != nil {
		return nil, err
	}
	return &check, nil
}

// UpdateCustomCheck replaces a check in full.
func (c *Client) UpdateCustomCheck(ctx context.Context, id string, input CustomCheckInput) (*CustomCheck, error) {
	var check CustomCheck
	if err := c.do(ctx, http.MethodPut, customChecksPath+"/"+url.PathEscape(id), nil, input, &check); err != nil {
		return nil, err
	}
	return &check, nil
}

// DeleteCustomCheck deletes a check. Findings it produced are cleaned up
// asynchronously by the API.
func (c *Client) DeleteCustomCheck(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, customChecksPath+"/"+url.PathEscape(id), nil, nil, nil)
}

// ListCustomChecks returns one page of checks.
func (c *Client) ListCustomChecks(ctx context.Context, options ListCustomChecksOptions) (*CustomCheckPage, error) {
	query := url.Values{}
	if options.AssetType != "" {
		query.Set("assetType", options.AssetType)
	}
	if options.Scope != "" {
		query.Set("scope", options.Scope)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}

	var page CustomCheckPage
	if err := c.do(ctx, http.MethodGet, customChecksPath, query, nil, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListAllCustomChecks follows the pagination cursor and returns every
// matching check.
func (c *Client) ListAllCustomChecks(ctx context.Context, assetType, scope string) ([]CustomCheck, error) {
	var checks []CustomCheck
	seenCursors := map[string]bool{}
	options := ListCustomChecksOptions{AssetType: assetType, Scope: scope, Limit: ListPageSize}

	for {
		page, err := c.ListCustomChecks(ctx, options)
		if err != nil {
			return nil, err
		}
		checks = append(checks, page.Items...)

		if page.NextCursor == "" {
			return checks, nil
		}
		if seenCursors[page.NextCursor] {
			return nil, fmt.Errorf("the list endpoint returned the same page cursor twice; stopping to avoid an endless loop")
		}
		seenCursors[page.NextCursor] = true
		options.Cursor = page.NextCursor
	}
}

// FindCustomCheckBySlug returns the check with the given slug. The API has no
// slug filter, so this pages through the whole list. It returns a 404
// APIError when no check matches.
func (c *Client) FindCustomCheckBySlug(ctx context.Context, slug string) (*CustomCheck, error) {
	checks, err := c.ListAllCustomChecks(ctx, "", "")
	if err != nil {
		return nil, err
	}
	for index := range checks {
		if checks[index].Slug == slug {
			return &checks[index], nil
		}
	}
	return nil, &APIError{
		StatusCode: http.StatusNotFound,
		Method:     http.MethodGet,
		Path:       customChecksPath,
		Message:    fmt.Sprintf("no custom check has the slug %q", slug),
	}
}

// Dry-run statuses reported by the API.
const (
	DryRunRunning   = "RUNNING"
	DryRunSucceeded = "SUCCEEDED"
	DryRunFailed    = "FAILED"
	DryRunTimedOut  = "TIMED_OUT"
	DryRunAborted   = "ABORTED"
)

// DryRunCheck is the check definition a dry run evaluates. It does not need
// to be saved first.
type DryRunCheck struct {
	CustomCheckID    string            `json:"customCheckId"`
	Slug             *string           `json:"slug,omitempty"`
	Title            *string           `json:"title,omitempty"`
	Description      *string           `json:"description,omitempty"`
	Target           Target            `json:"target"`
	AdditionalAssets []AdditionalAsset `json:"additionalAssets,omitempty"`
	Defaults         *Defaults         `json:"defaults,omitempty"`
	Type             string            `json:"type,omitempty"`
	Body             string            `json:"body"`
}

// StartDryRunInput starts a dry run against one integration.
type StartDryRunInput struct {
	IntegrationID string      `json:"integrationId"`
	Check         DryRunCheck `json:"check"`
}

// DryRunStarted is the response to starting a dry run.
type DryRunStarted struct {
	DryRunID string `json:"dryRunId"`
	Status   string `json:"status"`
	PollURL  string `json:"pollUrl"`
}

// DryRunOutput is the summary of a finished dry run.
type DryRunOutput struct {
	TotalChecks    int64             `json:"totalChecks"`
	SucceededCount int64             `json:"succeededCount"`
	FailedCount    int64             `json:"failedCount"`
	TotalFindings  int64             `json:"totalFindings"`
	Failures       []json.RawMessage `json:"failures,omitempty"`
}

// DryRun is the state of a dry run.
type DryRun struct {
	DryRunID    string            `json:"dryRunId"`
	Status      string            `json:"status"`
	StartedAt   string            `json:"startedAt,omitempty"`
	CompletedAt string            `json:"completedAt,omitempty"`
	Output      *DryRunOutput     `json:"output,omitempty"`
	Findings    []json.RawMessage `json:"findings,omitempty"`
}

// StartCustomCheckDryRun starts a dry run. Findings from a dry run are never
// saved.
func (c *Client) StartCustomCheckDryRun(ctx context.Context, input StartDryRunInput) (*DryRunStarted, error) {
	var started DryRunStarted
	if err := c.do(ctx, http.MethodPost, dryRunsPath, nil, input, &started); err != nil {
		return nil, err
	}
	return &started, nil
}

// GetCustomCheckDryRun fetches the current state of a dry run.
func (c *Client) GetCustomCheckDryRun(ctx context.Context, id string) (*DryRun, error) {
	var dryRun DryRun
	if err := c.do(ctx, http.MethodGet, dryRunsPath+"/"+url.PathEscape(id), nil, nil, &dryRun); err != nil {
		return nil, err
	}
	return &dryRun, nil
}

// WaitForCustomCheckDryRun polls a dry run until it leaves the RUNNING state
// or ctx is done.
func (c *Client) WaitForCustomCheckDryRun(ctx context.Context, id string, pollInterval time.Duration) (*DryRun, error) {
	for {
		dryRun, err := c.GetCustomCheckDryRun(ctx, id)
		if err != nil {
			return nil, err
		}
		if dryRun.Status != DryRunRunning {
			return dryRun, nil
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return dryRun, ctx.Err()
		case <-timer.C:
		}
	}
}
