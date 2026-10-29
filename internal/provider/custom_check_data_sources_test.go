package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/plerionhq/terraform-provider-plerion/internal/fakeapi"
)

func seedCheck(server *fakeapi.Server, slug, assetType, scope string) *fakeapi.Check {
	informational := false
	description := "Seeded for a test."
	return server.Seed(fakeapi.Writable{
		Slug:        slug,
		Title:       "Seeded " + slug,
		Description: &description,
		Target:      &fakeapi.Target{AssetType: assetType, Scope: scope},
		Defaults: &fakeapi.Defaults{
			SeverityLevel: "MEDIUM",
			Message:       "Seeded finding.",
			Informational: &informational,
			Meta:          map[string]any{"owner": "security", "area": "iam"},
		},
		Type: "rego",
		Body: "package custom\n\ndefault result := []\n",
	})
}

func TestCustomCheckDataSourceLooksUpByIDAndSlug(t *testing.T) {
	server := newTestServer(t)
	seeded := seedCheck(server, "seeded-check", "AWS::IAM::Role", "batch")
	server.Mutate(seeded.ID, func(check *fakeapi.Check) {
		check.LastRunAt = "2026-09-02T00:00:00.000Z"
		check.Rollup = &fakeapi.Rollup{OverallStatus: "completed", IntegrationCount: 2, CompletedCount: 2, TotalFindingCount: 7}
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: providerConfig(server) + fmt.Sprintf(`
data "plerion_custom_check" "by_id" {
  id = %q
}

data "plerion_custom_check" "by_slug" {
  slug = "seeded-check"
}
`, seeded.ID),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("slug"), knownvalue.StringExact("seeded-check")),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("title"), knownvalue.StringExact("Seeded seeded-check")),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("target").AtMapKey("asset_type"), knownvalue.StringExact("AWS::IAM::Role")),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("defaults").AtMapKey("meta"),
					knownvalue.StringExact(`{"area":"iam","owner":"security"}`)),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("last_run_at"), knownvalue.StringExact("2026-09-02T00:00:00.000Z")),
				statecheck.ExpectKnownValue("data.plerion_custom_check.by_id", tfjsonpath.New("run_status"), knownvalue.ObjectExact(map[string]knownvalue.Check{
					"overall_status":      knownvalue.StringExact("completed"),
					"integration_count":   knownvalue.Int64Exact(2),
					"completed_count":     knownvalue.Int64Exact(2),
					"failed_count":        knownvalue.Int64Exact(0),
					"total_finding_count": knownvalue.Int64Exact(7),
				})),
				statecheck.CompareValuePairs(
					"data.plerion_custom_check.by_id", tfjsonpath.New("id"),
					"data.plerion_custom_check.by_slug", tfjsonpath.New("id"),
					compare.ValuesSame(),
				),
			},
		}},
	})
}

func TestCustomCheckDataSourceReportsAMissingCheck(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(server) + `
data "plerion_custom_check" "missing" {
  slug = "does-not-exist"
}
`,
				ExpectError: regexp.MustCompile(`No custom check in this tenant has the slug "does-not-exist"`),
			},
			{
				Config: providerConfig(server) + `
data "plerion_custom_check" "missing" {
  id = "0f8e7d6c-5b4a-4392-8170-6e5d4c3b2a19"
}
`,
				ExpectError: regexp.MustCompile(`No custom check in this tenant has the ID`),
			},
		},
	})
}

func TestCustomCheckDataSourceNeedsExactlyOneLookup(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(server) + `data "plerion_custom_check" "none" {}`,
				ExpectError: regexp.MustCompile(`Exactly one of these attributes must be configured: \[id,slug\]`),
			},
			{
				Config:      providerConfig(server) + `data "plerion_custom_check" "empty" { id = "" }`,
				ExpectError: regexp.MustCompile(`must be a custom check ID, which is a UUID`),
			},
			{
				Config: providerConfig(server) + `
data "plerion_custom_check" "both" {
  id   = "0f8e7d6c-5b4a-4392-8170-6e5d4c3b2a19"
  slug = "both"
}
`,
				ExpectError: regexp.MustCompile(`Invalid Attribute Combination`),
			},
		},
	})
}

func TestCustomChecksDataSourceFiltersAndPages(t *testing.T) {
	server := newTestServer(t)
	// More checks than one page holds, so the data source must follow the cursor.
	for index := range 205 {
		seedCheck(server, fmt.Sprintf("bucket-check-%03d", index), "AWS::S3::Bucket", "batch")
	}
	seedCheck(server, "role-check", "AWS::IAM::Role", "batch")
	seedCheck(server, "whole-population", "AWS::S3::Bucket", "all")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: providerConfig(server) + `
data "plerion_custom_checks" "all" {}

data "plerion_custom_checks" "roles" {
  asset_type = "AWS::IAM::Role"
}

data "plerion_custom_checks" "whole_population_buckets" {
  asset_type = "AWS::S3::Bucket"
  scope      = "all"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.plerion_custom_checks.all", tfjsonpath.New("custom_checks"), knownvalue.ListSizeExact(207)),
				statecheck.ExpectKnownValue("data.plerion_custom_checks.roles", tfjsonpath.New("custom_checks"), knownvalue.ListSizeExact(1)),
				statecheck.ExpectKnownValue("data.plerion_custom_checks.roles", tfjsonpath.New("custom_checks").AtSliceIndex(0).AtMapKey("slug"),
					knownvalue.StringExact("role-check")),
				statecheck.ExpectKnownValue("data.plerion_custom_checks.roles", tfjsonpath.New("custom_checks").AtSliceIndex(0).AtMapKey("run_status"),
					knownvalue.Null()),
				statecheck.ExpectKnownValue("data.plerion_custom_checks.whole_population_buckets", tfjsonpath.New("custom_checks").AtSliceIndex(0).AtMapKey("slug"),
					knownvalue.StringExact("whole-population")),
			},
		}},
	})

	if pages := server.Requests("GET /v1/tenant/custom-checks"); pages < 4 {
		t.Fatalf("expected the full list to take two pages, but the API saw %d list requests in total", pages)
	}
}

const dryRunConfig = `
data "plerion_custom_check_dry_run" "preview" {
  integration_id = "7c1e2d3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = "batch"
  }

  defaults = {
    severity_level = "HIGH"
    message        = "The bucket is public."
  }

  body    = "package custom\n\nresult := []\n"
  timeout = %q
}
`

func TestCustomCheckDryRunReturnsFindings(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: providerConfig(server) + fmt.Sprintf(dryRunConfig, "1m"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.plerion_custom_check_dry_run.preview", tfjsonpath.New("status"), knownvalue.StringExact("SUCCEEDED")),
				statecheck.ExpectKnownValue("data.plerion_custom_check_dry_run.preview", tfjsonpath.New("total_findings"), knownvalue.Int64Exact(1)),
				statecheck.ExpectKnownValue("data.plerion_custom_check_dry_run.preview", tfjsonpath.New("custom_check_id"),
					knownvalue.StringRegexp(uuidPattern)),
				statecheck.ExpectKnownValue("data.plerion_custom_check_dry_run.preview", tfjsonpath.New("findings"),
					knownvalue.StringExact(`[{"assetId":"asset-1","id":"finding-1","severityLevel":"HIGH","status":"FAILED"}]`)),
				statecheck.ExpectKnownValue("data.plerion_custom_check_dry_run.preview", tfjsonpath.New("completed_at"), knownvalue.NotNull()),
			},
		}},
	})
}

func TestCustomCheckDryRunFailsOnABrokenPolicy(t *testing.T) {
	cases := map[string]struct {
		outcome   fakeapi.DryRunOutcome
		timeout   string
		wantError string
	}{
		"evaluation failure": {
			outcome: fakeapi.DryRunOutcome{
				Status:      "SUCCEEDED",
				FailedCount: 1,
				Failures:    []map[string]any{{"error": "rego_parse_error: unexpected eof token"}},
			},
			timeout:   "1m",
			wantError: `(?s)The custom check could not be evaluated.*rego_parse_error`,
		},
		"failed run": {
			outcome:   fakeapi.DryRunOutcome{Status: "FAILED"},
			timeout:   "1m",
			wantError: `ended with the status FAILED`,
		},
		"run that never finishes": {
			outcome:   fakeapi.DryRunOutcome{Status: "SUCCEEDED", PollsBeforeDone: 1_000_000},
			timeout:   "300ms",
			wantError: `was still running after 300ms`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t)
			server.SetDryRunOutcome(testCase.outcome)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories,
				Steps: []resource.TestStep{{
					Config:      providerConfig(server) + fmt.Sprintf(dryRunConfig, testCase.timeout),
					ExpectError: regexp.MustCompile(testCase.wantError),
				}},
			})
		})
	}
}

func TestCustomCheckDryRunValidatesTheTimeout(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config:      providerConfig(server) + fmt.Sprintf(dryRunConfig, "five minutes"),
			ExpectError: regexp.MustCompile(`"five minutes" is not a valid timeout`),
		}},
	})
}

func TestCustomCheckDryRunPreviewsAManagedCheck(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: providerConfig(server) + minimalCheckConfig("previewed") + `
data "plerion_custom_check_dry_run" "preview" {
  integration_id    = "7c1e2d3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"
  custom_check_id   = plerion_custom_check.test.id
  slug              = plerion_custom_check.test.slug
  target            = plerion_custom_check.test.target
  additional_assets = plerion_custom_check.test.additional_assets
  defaults          = plerion_custom_check.test.defaults
  body              = plerion_custom_check.test.body
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.CompareValuePairs(
					"data.plerion_custom_check_dry_run.preview", tfjsonpath.New("custom_check_id"),
					testCheckAddress, tfjsonpath.New("id"),
					compare.ValuesSame(),
				),
			},
		}},
	})
}

// The documented pattern: shared locals let Terraform read the dry run during
// the plan, so a precondition can stop the check from being created.
func TestCustomCheckDryRunGatesTheApply(t *testing.T) {
	server := newTestServer(t)

	gatedConfig := func(maxFindings int) string {
		return providerConfig(server) + fmt.Sprintf(`
locals {
  check = {
    target   = { asset_type = "AWS::IAM::Role", scope = "batch" }
    defaults = { severity_level = "HIGH", message = "Flagged." }
    body     = "package custom\n\ndefault result := []\n"
  }
}

data "plerion_custom_check_dry_run" "gate" {
  integration_id = "7c1e2d3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f"
  target         = local.check.target
  defaults       = local.check.defaults
  body           = local.check.body
}

resource "plerion_custom_check" "test" {
  slug     = "gated"
  title    = "Gated by a dry run"
  target   = local.check.target
  defaults = local.check.defaults
  body     = local.check.body

  lifecycle {
    precondition {
      condition     = data.plerion_custom_check_dry_run.gate.total_findings <= %d
      error_message = "The dry run found too many problems."
    }
  }
}
`, maxFindings)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      gatedConfig(0),
				ExpectError: regexp.MustCompile(`The dry run found too many problems`),
			},
			{
				PreConfig: func() {
					if created := server.Requests("POST /v1/tenant/custom-checks"); created != 0 {
						t.Fatalf("the check was created %d times although the precondition failed", created)
					}
				},
				Config: gatedConfig(5),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("slug"), knownvalue.StringExact("gated")),
				},
			},
		},
	})
}
