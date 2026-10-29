package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/plerionhq/terraform-provider-plerion/internal/fakeapi"
)

const testCheckAddress = "plerion_custom_check.test"

func minimalCheckConfig(slug string) string {
	return fmt.Sprintf(`
resource "plerion_custom_check" "test" {
  slug  = %q
  title = "Buckets must block public access"

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = "batch"
  }

  defaults = {
    severity_level = "HIGH"
    message        = "The bucket does not block public access."
  }

  body = <<-REGO
    package custom

    default result := []
  REGO
}
`, slug)
}

func fullCheckConfig(slug string) string {
	return fmt.Sprintf(`
resource "plerion_custom_check" "test" {
  slug        = %q
  title       = "Buckets must block public access everywhere"
  description = "Flags buckets whose public access block is off."

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = "all"
  }

  additional_assets = [
    { asset_type = "AWS::IAM::Role" },
    { asset_type = "AWS::KMS::Key" },
  ]

  defaults = {
    severity_level = "CRITICAL"
    message        = "The bucket is public."
    informational  = true
    remediation    = "Turn on the bucket's public access block."
    meta = jsonencode({
      owner    = "platform-team"
      priority = 1
      labels   = { team = "security", area = "storage" }
    })
  }

  body = <<-REGO
    package custom

    result := [{"id": bucket.id, "status": "FAILED"} |
      bucket := input.assets[_]
      not bucket.data.PublicAccessBlockConfiguration
    ]
  REGO
}
`, slug)
}

func expectNoChecksLeft(server *fakeapi.Server, slugs ...string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		for _, slug := range slugs {
			if server.CheckBySlug(slug) != nil {
				return fmt.Errorf("the check %q still exists after destroy", slug)
			}
		}
		return nil
	}
}

func TestCustomCheckResourceLifecycle(t *testing.T) {
	server := newTestServer(t)
	config := providerConfig(server)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             expectNoChecksLeft(server, "public-buckets"),
		Steps: []resource.TestStep{
			{
				Config: config + minimalCheckConfig("public-buckets"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("type"), knownvalue.StringExact("rego")),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("informational"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("meta"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("remediation"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("additional_assets"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("organization_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("tenant_id"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("created_at"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("created_by"), knownvalue.NotNull()),
				},
			},
			{
				ResourceName:      testCheckAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      testCheckAddress,
				ImportState:       true,
				ImportStateId:     "public-buckets",
				ImportStateVerify: true,
			},
			{
				Config: config + fullCheckConfig("public-buckets"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(testCheckAddress, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("target").AtMapKey("scope"), knownvalue.StringExact("all")),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("additional_assets"), knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{"asset_type": knownvalue.StringExact("AWS::IAM::Role")}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{"asset_type": knownvalue.StringExact("AWS::KMS::Key")}),
					})),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("informational"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("meta"),
						knownvalue.StringExact(`{"labels":{"area":"storage","team":"security"},"owner":"platform-team","priority":1}`)),
				},
				Check: func(_ *terraform.State) error {
					stored := server.CheckBySlug("public-buckets")
					if stored.Defaults.Meta["owner"] != "platform-team" || stored.Description == nil {
						return fmt.Errorf("the API did not receive the full check: %+v", stored.Writable)
					}
					return nil
				},
			},
			{
				// The API returns metadata with its keys reordered, so this proves
				// an import does not produce a diff on meta.
				ResourceName:      testCheckAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: config + minimalCheckConfig("public-buckets"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("version"), knownvalue.Int64Exact(3)),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("additional_assets"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("meta"), knownvalue.Null()),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("remediation"), knownvalue.Null()),
				},
				Check: func(_ *terraform.State) error {
					stored := server.CheckBySlug("public-buckets")
					if stored.Description != nil || stored.AdditionalAssets != nil || stored.Defaults.Meta != nil || stored.Defaults.Remediation != nil {
						return fmt.Errorf("removed attributes are still stored: %+v", stored.Writable)
					}
					return nil
				},
			},
		},
	})
}

func TestCustomCheckResourceRenamesSlugInPlace(t *testing.T) {
	server := newTestServer(t)
	config := providerConfig(server)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config + minimalCheckConfig("old-slug"),
			},
			{
				Config: config + minimalCheckConfig("new-slug"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(testCheckAddress, plancheck.ResourceActionUpdate),
					},
				},
				Check: func(_ *terraform.State) error {
					if server.CheckBySlug("old-slug") != nil || server.CheckBySlug("new-slug") == nil {
						return fmt.Errorf("the slug was not renamed on the existing check")
					}
					return nil
				},
			},
		},
	})
}

func TestCustomCheckResourceKeepsHandWrittenJSON(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			// Hand-written JSON with its own spacing and key order must not
			// cause a diff on the next plan, which the test runs automatically.
			Config: providerConfig(server) + `
resource "plerion_custom_check" "test" {
  slug  = "hand-written-meta"
  title = "Hand-written metadata"

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = "batch"
  }

  defaults = {
    severity_level = "LOW"
    message        = "Something to look at."
    meta           = <<-JSON
      {
        "zeta": 1,
        "alpha": { "nested": [1, 2, 3] }
      }
    JSON
  }

  body = "package custom\n\ndefault result := []\n"
}
`,
		}},
	})
}

func TestCustomCheckResourceRepairsDrift(t *testing.T) {
	server := newTestServer(t)
	config := providerConfig(server) + minimalCheckConfig("drifting")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					stored := server.CheckBySlug("drifting")
					server.Mutate(stored.ID, func(check *fakeapi.Check) {
						check.Title = "Changed in the Plerion app"
					})
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(testCheckAddress, plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("title"), knownvalue.StringExact("Buckets must block public access")),
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("version"), knownvalue.Int64Exact(3)),
				},
			},
			{
				PreConfig: func() {
					server.Remove(server.CheckBySlug("drifting").ID)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(testCheckAddress, plancheck.ResourceActionCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
				},
			},
		},
	})
}

func TestCustomCheckResourceExplainsASlugConflict(t *testing.T) {
	server := newTestServer(t)
	informational := false
	server.Seed(fakeapi.Writable{
		Slug:     "taken",
		Title:    "Created in the Plerion app",
		Target:   &fakeapi.Target{AssetType: "AWS::S3::Bucket", Scope: "batch"},
		Defaults: &fakeapi.Defaults{SeverityLevel: "LOW", Message: "m", Informational: &informational},
		Type:     "rego",
		Body:     "package custom",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config:      providerConfig(server) + minimalCheckConfig("taken"),
			ExpectError: regexp.MustCompile(`(?s)A custom check with this slug already exists.*import \{`),
		}},
	})
}

func TestCustomCheckResourceValidatesConfiguration(t *testing.T) {
	server := newTestServer(t)

	check := func(attributes string) string {
		return providerConfig(server) + fmt.Sprintf(`
resource "plerion_custom_check" "test" {
  title = "Invalid"
  body  = "package custom"
  %s
}
`, attributes)
	}
	validTarget := `target = { asset_type = "AWS::S3::Bucket", scope = "batch" }`
	validDefaults := `defaults = { severity_level = "LOW", message = "m" }`

	cases := map[string]struct {
		attributes string
		wantError  string
	}{
		"slug with capitals": {
			attributes: `slug = "Not-Valid"` + "\n" + validTarget + "\n" + validDefaults,
			wantError:  `lowercase letters, numbers and hyphens`,
		},
		"unknown scope": {
			attributes: `slug = "ok"` + "\n" + `target = { asset_type = "AWS::S3::Bucket", scope = "some" }` + "\n" + validDefaults,
			wantError:  `value must be one of`,
		},
		"unknown severity": {
			attributes: `slug = "ok"` + "\n" + validTarget + "\n" + `defaults = { severity_level = "SEVERE", message = "m" }`,
			wantError:  `value must be one of`,
		},
		"empty additional assets": {
			attributes: `slug = "ok"` + "\n" + validTarget + "\n" + validDefaults + "\n" + `additional_assets = []`,
			wantError:  `list must contain at least 1 elements`,
		},
		"meta that is not an object": {
			attributes: `slug = "ok"` + "\n" + validTarget + "\n" +
				`defaults = { severity_level = "LOW", message = "m", meta = jsonencode(["a", "b"]) }`,
			wantError: `Metadata must be a JSON object`,
		},
		"meta that is not JSON": {
			attributes: `slug = "ok"` + "\n" + validTarget + "\n" +
				`defaults = { severity_level = "LOW", message = "m", meta = "owner=platform" }`,
			wantError: `Invalid JSON String Value`,
		},
		"meta with an unsafe integer": {
			attributes: `slug = "ok"` + "\n" + validTarget + "\n" +
				`defaults = { severity_level = "LOW", message = "m", meta = jsonencode({ account = 123456789012345678 }) }`,
			wantError: `Metadata contains a number that is too large`,
		},
		"empty description": {
			attributes: `slug = "ok"` + "\n" + `description = ""` + "\n" + validTarget + "\n" + validDefaults,
			wantError:  `string length must be between 1 and 4096`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories,
				Steps: []resource.TestStep{{
					Config:      check(testCase.attributes),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(testCase.wantError),
				}},
			})
		})
	}

	if requests := server.Requests("POST /v1/tenant/custom-checks"); requests != 0 {
		t.Fatalf("invalid configuration reached the API %d times", requests)
	}
}

// The API stores metadata numbers as 64-bit floats, so it returns 0.0000005
// as 5e-7 and rounds 0.12345678901234567. Neither may cause a diff or an
// inconsistent result.
func TestCustomCheckResourceComparesMetaNumbersByValue(t *testing.T) {
	server := newTestServer(t)

	checkWithMeta := func(meta string) string {
		return providerConfig(server) + fmt.Sprintf(`
resource "plerion_custom_check" "test" {
  slug  = "meta-numbers"
  title = "Metadata numbers"

  target = {
    asset_type = "AWS::S3::Bucket"
    scope      = "batch"
  }

  defaults = {
    severity_level = "LOW"
    message        = "Something to look at."
    meta           = %s
  }

  body = "package custom\n\ndefault result := []\n"
}
`, meta)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: checkWithMeta(`jsonencode({ tiny = 0.0000005, precise = 0.12345678901234567, decimal = 1.50, count = 3 })`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(testCheckAddress, tfjsonpath.New("defaults").AtMapKey("meta"),
						knownvalue.StringExact(`{"count":3,"decimal":1.5,"precise":0.12345678901234567,"tiny":0.0000005}`)),
				},
			},
			{
				Config: checkWithMeta("<<-JSON\n    { \"tiny\": 5e-7, \"ratio\": 0.50 }\n  JSON"),
			},
			{
				Config: checkWithMeta(`jsonencode({ tiny = 0.0000005, ratio = 0.5 })`),
			},
			{
				// The API returned 5e-7, and the import must still match jsonencode().
				ResourceName:      testCheckAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
