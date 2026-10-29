// Command terraform-provider-plerion serves the Plerion provider to Terraform.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/plerionhq/terraform-provider-plerion/internal/provider"
)

// Generate the registry docs from the provider schema, examples and templates.
//go:generate go tool tfplugindocs generate --provider-name plerion

// version is set by GoReleaser at build time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers such as delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/plerionhq/plerion",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
