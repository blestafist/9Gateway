package main

import (
	"os"
	"strings"
	"testing"
)

func TestDockerfileRuntimeContract(t *testing.T) {
	data, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, required := range []string{
		"FROM node:22.14.0-bookworm AS web-build",
		"npm install --global npm@10.9.2",
		"npm ci --ignore-scripts --include=dev",
		"COPY --from=web-build /web/dist ./web/dist",
		"test -z \"$(find dist -type f -name '*.map' -print -quit)\"",
		"FROM gcr.io/distroless/static-debian12:nonroot",
		"COPY --from=build --chown=65532:65532 /out/gwctl /gwctl",
		"USER 65532:65532",
		"ENV PATH=/:",
		"VOLUME [\"/data\"]",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing runtime contract %q", required)
		}
	}
}
