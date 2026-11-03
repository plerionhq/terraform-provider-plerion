package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/plerionhq/terraform-provider-plerion/internal/client"
	"github.com/plerionhq/terraform-provider-plerion/internal/fakeapi"
)

func providerConfigWithKey(server *fakeapi.Server, apiKey string) string {
	return fmt.Sprintf(`
provider "plerion" {
  api_url = %q
  api_key = %q
}
`, server.URL, apiKey)
}

// checkAWSCounts compares what the fake holds. A negative wantCreates skips
// the create count.
func checkAWSCounts(server *fakeapi.Server, wantIntegrations, wantCreates, wantServiceAccounts, wantRegions int) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		integrations, creates, serviceAccounts, regions := server.AWSCounts()
		if wantCreates >= 0 && creates != wantCreates {
			return fmt.Errorf("integration creates = %d, want %d", creates, wantCreates)
		}
		if integrations != wantIntegrations || serviceAccounts != wantServiceAccounts || regions != wantRegions {
			return fmt.Errorf("fake holds %d integrations, %d service accounts, %d regions; want %d, %d, %d",
				integrations, serviceAccounts, regions, wantIntegrations, wantServiceAccounts, wantRegions)
		}
		return nil
	}
}

func TestTenantDataSourceReadsTheTenant(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: providerConfig(server) + `data "plerion_tenant" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.plerion_tenant.this", "tenant_id", fakeapi.TenantID),
				resource.TestCheckResourceAttr("data.plerion_tenant.this", "external_id", fakeapi.ExternalID),
			),
		}},
	})
}

func integrationConfig(server *fakeapi.Server, roleARN, extra string) string {
	return providerConfig(server) + fmt.Sprintf(`
resource "plerion_aws_integration" "test" {
  role_arn = %q
%s
}
`, roleARN, extra)
}

func TestAWSIntegrationLifecycle(t *testing.T) {
	server := newTestServer(t)
	const address = "plerion_aws_integration.test"
	// The role fails to assume once, as a just-created IAM role does.
	firstRole := "arn:aws:iam::111111111111:role/not-ready-PlerionRole"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             checkAWSCounts(server, 0, -1, 0, 0),
		Steps: []resource.TestStep{
			{
				Config: integrationConfig(server, firstRole, `  name = "prod"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "id", "int-111111111111"),
					resource.TestCheckResourceAttr(address, "account_id", "111111111111"),
					resource.TestCheckResourceAttr(address, "name", "prod"),
					resource.TestCheckResourceAttr(address, "status", "active"),
					resource.TestCheckNoResourceAttr(address, "kms_key_access_mode"),
					resource.TestCheckNoResourceAttr(address, "environment_id"),
				),
			},
			{
				ResourceName:      address,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Changed settings update the record in place.
				Config: integrationConfig(server, firstRole, `
  name                 = "prod-renamed"
  environment_id       = "env-1"
  kms_key_access_mode  = "SELECTED_KEYS"
  instance_profile_arn = "arn:aws:iam::111111111111:instance-profile/PlerionInstanceProfile"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "name", "prod-renamed"),
					resource.TestCheckResourceAttr(address, "environment_id", "env-1"),
					resource.TestCheckResourceAttr(address, "kms_key_access_mode", "SELECTED_KEYS"),
					resource.TestCheckResourceAttr(address, "instance_profile_arn", "arn:aws:iam::111111111111:instance-profile/PlerionInstanceProfile"),
					checkAWSCounts(server, 1, 1, 0, 0),
				),
			},
			{
				// Removing instance_profile_arn clears it in Plerion, and a
				// rename in the console is drift that apply puts back. Removing
				// environment_id keeps the environment.
				PreConfig: func() { server.RenameAWSIntegration("int-111111111111", "renamed-in-console") },
				Config: integrationConfig(server, firstRole, `
  name                = "prod-renamed"
  kms_key_access_mode = "SELECTED_KEYS"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "environment_id", "env-1"),
					resource.TestCheckNoResourceAttr(address, "instance_profile_arn"),
					resource.TestCheckResourceAttr(address, "name", "prod-renamed"),
				),
			},
			{
				// Another AWS account is a different integration.
				Config: integrationConfig(server, "arn:aws:iam::333333333333:role/PlerionRole", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(address, "account_id", "333333333333"),
					resource.TestCheckResourceAttr(address, "name", "333333333333"),
					checkAWSCounts(server, 1, 2, 0, 0),
				),
			},
		},
	})
}

// The templates don't set environment_id, so an environment chosen in the
// console, or kept through terraform import, must not be undone by an apply.
func TestAWSIntegrationKeepsTheConsoleEnvironment(t *testing.T) {
	server := newTestServer(t)
	const address = "plerion_aws_integration.test"
	config := integrationConfig(server, "arn:aws:iam::111111111111:role/PlerionRole", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() { server.SetAWSIntegrationEnvironment("int-111111111111", "env-console") },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr(address, "environment_id", "env-console"),
			},
		},
	})
}

// A removal that fails in Plerion keeps answering with the integration, so
// destroy reports it at once instead of waiting out its timeout.
func TestAWSIntegrationDestroyReportsAFailedRemoval(t *testing.T) {
	server := newTestServer(t)
	const id = "int-111111111111"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             checkAWSCounts(server, 0, -1, 0, 0),
		Steps: []resource.TestStep{
			{Config: integrationConfig(server, "arn:aws:iam::111111111111:role/PlerionRole", "")},
			{
				PreConfig:   func() { server.FailAWSIntegrationDeletes(id, true) },
				Config:      providerConfig(server),
				ExpectError: regexp.MustCompile(`removal of integration int-111111111111 failed`),
			},
			{
				// Destroying again starts the removal again.
				PreConfig: func() { server.FailAWSIntegrationDeletes(id, false) },
				Config:    providerConfig(server),
			},
		},
	})
}

func TestAWSIntegrationHeldForApproval(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: integrationConfig(server, "arn:aws:iam::"+fakeapi.HeldAccountID+":role/PlerionRole", ""),
			Check:  resource.TestCheckResourceAttr("plerion_aws_integration.test", "status", statusPendingApproval),
		}},
	})
}

func TestAWSIntegrationAlreadyConnectedSuggestsImport(t *testing.T) {
	server := newTestServer(t)
	id := server.SeedAWSIntegration("555555555555")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config:      integrationConfig(server, "arn:aws:iam::555555555555:role/PlerionRole", ""),
			ExpectError: regexp.MustCompile(`terraform import <resource address> ` + regexp.QuoteMeta(id)),
		}},
	})
}

func TestAlreadyConnectedDetail(t *testing.T) {
	const id = "0f6c2a8e-5d41-4b7a-9e3c-2a1b8d7f6e50"
	named := &client.APIError{StatusCode: 409, Errors: []client.FieldError{{
		Code: "IntegrationAlreadyExists", Message: "AWS account 555555555555 already has integration " + id + " in this tenant.",
	}}}
	unnamed := &client.APIError{StatusCode: 409, Errors: []client.FieldError{{
		Code: "IntegrationAlreadyExists", Message: "This account already has an integration.",
	}}}

	if got := alreadyConnectedDetail("555555555555", named); !strings.HasSuffix(got, "terraform import <resource address> "+id) {
		t.Errorf("with the id in the message, got %q", got)
	}
	if got := alreadyConnectedDetail("555555555555", unnamed); !strings.Contains(got, "Plerion console") ||
		!strings.HasSuffix(got, "<integration id>") {
		t.Errorf("without the id in the message, got %q", got)
	}
}

func TestAPIErrorEndsInOneFullStop(t *testing.T) {
	var diags diag.Diagnostics
	addAPIError(&diags, "update the thing", &client.APIError{
		StatusCode: 409, Method: "PATCH", Path: "/v1/thing",
		Errors: []client.FieldError{{Message: "Only the name can be changed here."}},
	})

	if detail := diags[0].Detail(); !strings.HasSuffix(detail, "changed here.") {
		t.Errorf("detail = %q, want one full stop at the end", detail)
	}
}

func TestReadOnlyKeyCannotConnectAccounts(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerConfigWithKey(server, fakeapi.ReadOnlyAPIKey) + `
resource "plerion_aws_integration" "test" {
  role_arn = "arn:aws:iam::111111111111:role/PlerionRole"
}`,
				ExpectError: regexp.MustCompile(`(?s)does not have permission.*Integration:Modify`),
			},
			{
				Config: providerConfigWithKey(server, fakeapi.ReadOnlyAPIKey) + `
resource "plerion_aws_service_account" "test" {
  control_plane_role_arn = "arn:aws:iam::444444444444:role/PlerionControlPlane"
  instance_profile_arn   = "arn:aws:iam::444444444444:instance-profile/PlerionAppliance"
}`,
				ExpectError: regexp.MustCompile(`(?s)does not have permission.*ServiceAccount:Modify`),
			},
		},
	})
}

func TestARNsMustShareAnAccount(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: integrationConfig(server, "arn:aws:iam::111111111111:role/PlerionRole",
				`  instance_profile_arn = "arn:aws:iam::999999999999:instance-profile/Elsewhere"`),
			ExpectError: regexp.MustCompile(`ARN in another AWS account`),
		}},
	})
}

func serviceAccountConfig(server *fakeapi.Server, accountID, serviceAccountExtra, regionExtra string) string {
	return providerConfig(server) + fmt.Sprintf(`
resource "plerion_aws_service_account" "test" {
  control_plane_role_arn = "arn:aws:iam::%[1]s:role/PlerionControlPlane"
  instance_profile_arn   = "arn:aws:iam::%[1]s:instance-profile/PlerionAppliance"
%[2]s
}

resource "plerion_aws_service_account_region" "test" {
  account_id        = plerion_aws_service_account.test.account_id
  region            = "ap-south-1"
  vpc_id            = "vpc-0abc"
  security_group_id = "sg-0abc"
  queue_url         = "https://sqs.ap-south-1.amazonaws.com/%[1]s/plerion-appliance-scan-queue-1"
%[3]s
}
`, accountID, serviceAccountExtra, regionExtra)
}

func TestAWSServiceAccountAndRegion(t *testing.T) {
	server := newTestServer(t)
	const serviceAccount, region = "plerion_aws_service_account.test", "plerion_aws_service_account_region.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             checkAWSCounts(server, 0, -1, 0, 0),
		Steps: []resource.TestStep{
			{
				Config: serviceAccountConfig(server, "444444444444", `  template_version = "v1.1.1"`, `
  subnet_id        = "subnet-0aaa"
  template_version = "v1.4.1"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccount, "id", "444444444444"),
					resource.TestCheckResourceAttr(serviceAccount, "status", "active"),
					resource.TestCheckResourceAttr(serviceAccount, "enabled", "true"),
					resource.TestCheckResourceAttr(serviceAccount, "aws_organization_id", fakeapi.AWSOrganizationID),
					resource.TestCheckResourceAttr(serviceAccount, "template_version", "v1.1.1"),
					resource.TestCheckResourceAttr(region, "id", "444444444444/ap-south-1"),
					resource.TestCheckResourceAttr(region, "max_instance_limit", "10"),
					resource.TestCheckResourceAttr(region, "enabled", "true"),
					resource.TestCheckResourceAttr(region, "template_version", "v1.4.1"),
				),
			},
			{ResourceName: serviceAccount, ImportState: true, ImportStateVerify: true},
			{ResourceName: region, ImportState: true, ImportStateVerify: true},
			{
				// The console turns the region off. Terraform updates the
				// network and leaves enabled alone. template_version is
				// dropped from the configuration and keeps its value.
				PreConfig: func() { server.DisableServiceAccountRegion("444444444444", "ap-south-1") },
				Config: serviceAccountConfig(server, "444444444444", `  name = "scanners"`, `
  subnet_id          = "subnet-0bbb"
  max_instance_limit = 25`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(serviceAccount, plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction(region, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccount, "name", "scanners"),
					resource.TestCheckResourceAttr(serviceAccount, "template_version", "v1.1.1"),
					resource.TestCheckResourceAttr(region, "subnet_id", "subnet-0bbb"),
					resource.TestCheckResourceAttr(region, "max_instance_limit", "25"),
					resource.TestCheckResourceAttr(region, "enabled", "false"),
					resource.TestCheckResourceAttr(region, "template_version", "v1.4.1"),
					checkAWSCounts(server, 0, 0, 1, 1),
				),
			},
		},
	})
}

func TestRegionRefusedOnHeldServiceAccount(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             checkAWSCounts(server, 0, -1, 0, 0),
		Steps: []resource.TestStep{{
			Config:      serviceAccountConfig(server, fakeapi.HeldAccountID, "", `  subnet_id = "subnet-0aaa"`),
			ExpectError: regexp.MustCompile(`awaiting approval`),
		}},
	})
}

// Plerion's workload service refuses IAM paths and most punctuation, so the
// provider stops them during the plan rather than letting the API answer 400.
func TestServiceAccountInputsMatchTheAPI(t *testing.T) {
	server := newTestServer(t)
	config := func(role, name string) string {
		return providerConfig(server) + fmt.Sprintf(`
resource "plerion_aws_service_account" "test" {
  control_plane_role_arn = %q
  instance_profile_arn   = "arn:aws:iam::444444444444:instance-profile/PlerionAppliance"
  name                   = %q
}
`, role, name)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      config("arn:aws:iam::444444444444:role/plerion/PlerionControlPlane", "scanners"),
				ExpectError: regexp.MustCompile(`with no path`),
			},
			{
				Config:      config("arn:aws:iam::444444444444:role/PlerionControlPlane", "scanners!"),
				ExpectError: regexp.MustCompile(`letters, digits, spaces`),
			},
		},
	})
}

func TestRegionImportIDIsChecked(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config:        providerConfig(server) + `resource "plerion_aws_service_account_region" "test" {}`,
			ResourceName:  "plerion_aws_service_account_region.test",
			ImportState:   true,
			ImportStateId: "not-an-id",
			ExpectError:   regexp.MustCompile(`Invalid import id`),
		}},
	})
}
