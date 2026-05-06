package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("mindgate v0.0.1")
		return
	}
	fmt.Fprintln(os.Stderr, "mindgate: not yet implemented")
	os.Exit(1)
}
