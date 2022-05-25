package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

var originalWorkingDirectory string

func setup() {
	originalWorkingDirectory, _ = os.Getwd()
	defer teardown()
}

func teardown() {
	_ = os.Chdir(originalWorkingDirectory)
}

func TestLoadPlanWithHomeAlias(t *testing.T) {
	setup()
	ctx := context.TODO()
	planPath := fmt.Sprintf("~%c", os.PathSeparator)
	_, _ = loadPlan(ctx, planPath)
	currentDir, _ := os.Getwd()
	expectedPath, _ := os.UserHomeDir()
	require.Equal(t, expectedPath, currentDir)
}

func TestLoadPlanWithCurrentPathAlias(t *testing.T) {
	setup()
	ctx := context.TODO()
	planPath := fmt.Sprintf(".%c", os.PathSeparator)
	_, _ = loadPlan(ctx, planPath)
	currentDir, _ := os.Getwd()
	require.Equal(t, originalWorkingDirectory, currentDir)
}

func TestLoadPlanWithParentPathAlias(t *testing.T) {
	setup()
	ctx := context.TODO()
	planPath := fmt.Sprintf("..%c/", os.PathSeparator)
	_, _ = loadPlan(ctx, planPath)
	currentDir, _ := os.Getwd()
	require.Equal(t, originalWorkingDirectory, currentDir)
}

func TestLoadPlanWithAbsPath(t *testing.T) {
	setup()
	ctx := context.TODO()
	planPath, _ := filepath.Abs(".")
	_, _ = loadPlan(ctx, planPath)
	currentDir, _ := os.Getwd()
	require.Equal(t, planPath, currentDir)
}
