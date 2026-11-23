// Command gencontract writes internal/gateway/contract.json from the Go
// declarations (internal/gateway/contract.go). It runs through `go generate`:
//
//	go generate ./internal/gateway
//
// The output is deterministic (sorted keys, sorted lists), so regenerating
// without a source change produces no diff — which is what makes
// TestContractIsUpToDate a meaningful check instead of a source of noise.
package main

import (
	"fmt"
	"os"

	"github.com/zeokeel/zeokeel/internal/gateway"
)

func main() {
	data, err := gateway.ContractJSON()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gencontract: %v\n", err)
		os.Exit(1)
	}
	// go generate runs this with the working directory of the package, so the
	// generated file sits next to the declarations it came from.
	target := "contract.json"
	if err := os.WriteFile(target, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gencontract: write %s: %v\n", target, err)
		os.Exit(1)
	}
	fmt.Printf("gencontract: wrote %s (%d bytes)\n", target, len(data))
}
