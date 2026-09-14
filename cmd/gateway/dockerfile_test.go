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
