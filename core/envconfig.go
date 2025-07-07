package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// EnvDaggerConfig represents the structure of .env.dagger.json
// This file contains typed arguments that will be passed to the current module's constructor
// Keys are argument names, values are string representations that will be parsed
// using the existing CLI argument parsing system
type EnvDaggerConfig map[string]string

// LoadEnvDaggerConfigFromBytes loads .env.dagger.json from the given byte content
func LoadEnvDaggerConfigFromBytes(ctx context.Context, data []byte) (*EnvDaggerConfig, error) {
	var config EnvDaggerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .env.dagger.json: %w", err)
	}
	
	slog.ExtraDebug("loaded .env.dagger.json config from bytes", "config", config)
	return &config, nil
}

// LoadEnvDaggerConfig loads .env.dagger.json from the given directory
func LoadEnvDaggerConfig(ctx context.Context, dir string) (*EnvDaggerConfig, error) {
	configPath := filepath.Join(dir, ".env.dagger.json")
	
	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		slog.ExtraDebug("no .env.dagger.json found", "path", configPath)
		return nil, nil // Not an error, just no config
	}
	
	// Read and parse the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .env.dagger.json: %w", err)
	}
	
	var config EnvDaggerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .env.dagger.json: %w", err)
	}
	
	slog.ExtraDebug("loaded .env.dagger.json config", "path", configPath, "config", config)
	return &config, nil
}

// Validate validates the configuration
func (c *EnvDaggerConfig) Validate() error {
	if c == nil {
		return nil
	}
	for argName, argValue := range *c {
		if argName == "" {
			return fmt.Errorf("argument name cannot be empty")
		}
		if argValue == "" {
			return fmt.Errorf("argument %s cannot have empty value", argName)
		}
	}
	return nil
}

// Clone creates a deep copy of the configuration
func (c *EnvDaggerConfig) Clone() *EnvDaggerConfig {
	if c == nil {
		return nil
	}
	
	cp := make(EnvDaggerConfig, len(*c))
	for k, v := range *c {
		cp[k] = v
	}
	
	return &cp
}

// MergeEnvArgsWithCallArgs merges .env.dagger.json arguments with constructor call arguments
// Call arguments take precedence over env arguments
func (c *EnvDaggerConfig) MergeEnvArgsWithCallArgs(
	ctx context.Context,
	query *Query,
	constructorArgs []*FunctionArg,
	callArgs map[string]dagql.Input,
) (map[string]dagql.Input, error) {
	if c == nil {
		return callArgs, nil
	}
	
	// Start with a copy of call args (these take precedence)
	mergedArgs := make(map[string]dagql.Input, len(callArgs))
	for k, v := range callArgs {
		mergedArgs[k] = v
	}
	
	// Add env args for any missing arguments
	parser := NewServerArgumentParser(query)
	
	for argName, argValue := range *c {
		// Skip if the argument was already provided in the call
		if _, exists := callArgs[argName]; exists {
			continue
		}
		
		// Find the corresponding constructor argument
		var targetArg *FunctionArg
		for _, arg := range constructorArgs {
			if arg.Name == argName || arg.OriginalName == argName {
				targetArg = arg
				break
			}
		}
		
		if targetArg == nil {
			// Log a warning but don't fail - the argument might have been removed
			slog.Warn("argument from .env.dagger.json not found in constructor", "arg", argName)
			continue
		}
		
		// Parse the argument value using the server-side parser
		parsedValue, err := parser.ParseArgument(ctx, targetArg, argValue)
		if err != nil {
			return nil, fmt.Errorf("failed to parse .env.dagger.json argument %s: %w", argName, err)
		}
		
		// Convert to dagql.Input
		input, err := dagql.AsInput(parsedValue)
		if err != nil {
			return nil, fmt.Errorf("failed to convert .env.dagger.json argument %s to input: %w", argName, err)
		}
		
		mergedArgs[argName] = input
	}
	
	return mergedArgs, nil
}