package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/metadata-driven/mdd/modelgen"
)

func main() {
	flag.Usage = func() { fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s <models-directory>\n", os.Args[0]) }
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := modelgen.Generate(flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "modelgen:", err)
		os.Exit(1)
	}
}
