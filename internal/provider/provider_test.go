package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/plerionhq/terraform-provider-plerion/internal/fakeapi"
)

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"plerion": providerserver.NewProtocol6WithError(New("test")()),
}

func init() {
	dryRunPollInterval = 10 * time.Millisecond
}

// newTestServer starts a fake API and clears the provider's environment
// variables so a developer's own settings cannot leak into a test.
func newTestServer(t *testing.T) *fakeapi.Server {
	t.Helper()
	t.Setenv(apiKeyEnvVar, "")
	t.Setenv(apiURLEnvVar, "")
	return fakeapi.New(t)
}

func providerConfig(server *fakeapi.Server) string {
	return fmt.Sprintf(`
provider "plerion" {
  api_url = %q
  api_key = %q
}
`, server.URL, fakeapi.APIKey)
}

func TestProviderReadsSettingsFromTheEnvironment(t *testing.T) {
	server := newTestServer(t)
	t.Setenv(apiKeyEnvVar, fakeapi.APIKey)
	t.Setenv(apiURLEnvVar, server.URL)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "plerion" {}

data "plerion_custom_checks" "all" {}
`,
		}},
	})
}

func TestProviderRequiresAnAPIKey(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "plerion" {
  api_url = %q
}

data "plerion_custom_checks" "all" {}
`, server.URL),
			ExpectError: regexp.MustCompile(`Missing Plerion API key`),
		}},
	})
}

func TestProviderRejectsAnInsecureAPIURL(t *testing.T) {
	newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "plerion" {
  api_url = "http://au.api.plerion.com"
  api_key = "any"
}

data "plerion_custom_checks" "all" {}
`,
			ExpectError: regexp.MustCompile(`only accepts https`),
		}},
	})
}

func TestProviderExplainsARejectedAPIKey(t *testing.T) {
	server := newTestServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "plerion" {
  api_url = %q
  api_key = "wrong-key"
}

data "plerion_custom_checks" "all" {}
`, server.URL),
			ExpectError: regexp.MustCompile(`(?s)Plerion rejected the API key.*region where they were created`),
		}},
	})
}
