//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/build"
	"github.com/testcontainers/testcontainers-go"
)

func TestMain(m *testing.M) {
	if err := buildImage(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build image: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildImage() error {
	ctx := context.Background()
	// Find root directory
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Traversing up to find go.mod
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			return fmt.Errorf("could not find project root")
		}
		rootDir = parent
	}

	fmt.Println("Pre-building nylon-debug:latest image...")
	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    rootDir,
			Dockerfile: "Dockerfile",
			KeepImage:  true,
			Repo:       "nylon-debug",
			Tag:        "latest",
			BuildOptionsModifier: func(buildOptions *build.ImageBuildOptions) {
				buildOptions.Target = "debug"
			},
		},
	}

	// Creating the container triggers the build
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %v", err)
	}

	// We don't need this container, just the image.
	if err := c.Terminate(ctx); err != nil {
		fmt.Printf("Warning: failed to terminate builder container: %v\n", err)
	}
	return nil
}
