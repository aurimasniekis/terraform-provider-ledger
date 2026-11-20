// Command terraform-provider-ledger is the Terraform provider for Ledger.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/aurimasniekis/terraform-provider-ledger/internal/provider"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

//go:generate go tool tfplugindocs generate --provider-name ledger

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers such as delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/aurimasniekis/ledger",
		Debug:   debug,
	}
	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
