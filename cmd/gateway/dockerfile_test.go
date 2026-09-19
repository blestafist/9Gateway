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
		"FROM node:22.14.0-bookworm@sha256:e5ddf893cc6aeab0e5126e4edae35aa43893e2836d1d246140167ccc2616f5d7 AS web-build",
		"npm install --global npm@10.9.2",
		"npm ci --ignore-scripts --include=dev",
		"COPY --from=web-build /web/dist ./web/dist",
		"test -z \"$(find dist -type f -name '*.map' -print -quit)\"",
		"FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab",
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
