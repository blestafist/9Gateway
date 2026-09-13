package main

import (
	"context"
	"os"

	"github.com/pestit/9gateway/internal/gwctl"
)

func main() {
	os.Exit(gwctl.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
