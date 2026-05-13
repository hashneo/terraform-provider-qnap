// main.go is the entry point for the terraform-provider-qnap provider server.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/steventaylor/terraform-provider-qnap/internal/provider"
)

// version is set at build time via -ldflags.
var version = "0.1.0"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "Enable debug mode for use with a Terraform debugger (e.g. delve)")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "local/qnap/qnap",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
