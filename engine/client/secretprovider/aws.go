package secretprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	aws_config "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

var (
	awsMutex                sync.Mutex
	awsSecretsManagerClient *secretsmanager.Client
	awsSSMClient            *ssm.Client
	awsCache                = make(map[string][]byte)
)

// AWS provider for SecretProvider
// Auto-detects between Secrets Manager and Parameter Store based on path format
func awsProvider(ctx context.Context, pathWithQuery string) ([]byte, error) {
	awsMutex.Lock()
	defer awsMutex.Unlock()

	// Check cache first (cache key is the full pathWithQuery including params)
	if cached, ok := awsCache[pathWithQuery]; ok {
		return cached, nil
	}

	// Parse the URI
	parsed, err := url.Parse(pathWithQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to parse aws:// URI: %w", err)
	}

	path := parsed.Path
	query := parsed.Query()

	// Get optional region parameter
	region := query.Get("region")
	if region == "" {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			return nil, fmt.Errorf("AWS_REGION environment variable not set and no region parameter provided")
		}
	}

	// Initialize AWS clients if needed
	if err := initAWSClients(ctx, region); err != nil {
		return nil, err
	}

	var data []byte

	// Auto-detect service based on path format
	// Parameter Store paths must start with "/"
	if strings.HasPrefix(path, "/") {
		data, err = awsParameterStoreGet(ctx, path)
	} else {
		// Secrets Manager parameters
		version := query.Get("version")
		stage := query.Get("stage")
		field := query.Get("field")

		// Validate that version and stage are not both specified
		if version != "" && stage != "" {
			return nil, fmt.Errorf("cannot specify both version and stage parameters")
		}

		data, err = awsSecretsManagerGet(ctx, path, version, stage, field)
	}

	if err != nil {
		return nil, err
	}

	// Cache the result
	awsCache[pathWithQuery] = data

	return data, nil
}

// Initialize AWS clients with the default credential chain
func initAWSClients(ctx context.Context, region string) error {
	// Skip if already initialized
	if awsSecretsManagerClient != nil && awsSSMClient != nil {
		return nil
	}

	// Load default AWS config
	cfg, err := aws_config.LoadDefaultConfig(ctx, aws_config.WithRegion(region))
	if err != nil {
		return fmt.Errorf("unable to load AWS SDK config: %w", err)
	}

	// Support custom endpoint for testing (e.g., LocalStack)
	endpointURL := os.Getenv("AWS_ENDPOINT_URL")

	// Initialize Secrets Manager client
	awsSecretsManagerClient = secretsmanager.NewFromConfig(cfg, func(options *secretsmanager.Options) {
		if endpointURL != "" {
			options.BaseEndpoint = aws.String(endpointURL)
		}
	})

	// Initialize SSM client
	awsSSMClient = ssm.NewFromConfig(cfg, func(options *ssm.Options) {
		if endpointURL != "" {
			options.BaseEndpoint = aws.String(endpointURL)
		}
	})

	return nil
}

// Retrieve secret from AWS Secrets Manager
func awsSecretsManagerGet(ctx context.Context, secretName, version, stage, field string) ([]byte, error) {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	// Set version or stage if specified
	if version != "" {
		input.VersionId = aws.String(version)
	} else if stage != "" {
		input.VersionStage = aws.String(stage)
	} else {
		// Default to AWSCURRENT
		input.VersionStage = aws.String("AWSCURRENT")
	}

	result, err := awsSecretsManagerClient.GetSecretValue(ctx, input)
	if err != nil {
		return nil, mapAWSError(err, secretName, "secret")
	}

	var data []byte

	// Handle both string and binary secrets
	if result.SecretString != nil {
		data = []byte(*result.SecretString)
	} else if result.SecretBinary != nil {
		data = result.SecretBinary
	} else {
		return nil, fmt.Errorf("secret %q has no string or binary value", secretName)
	}

	// Extract field from JSON if requested
	if field != "" {
		data, err = extractJSONField(data, field)
		if err != nil {
			return nil, fmt.Errorf("failed to extract field %q from secret %q: %w", field, secretName, err)
		}
	}

	return data, nil
}

// Retrieve parameter from AWS Systems Manager Parameter Store
func awsParameterStoreGet(ctx context.Context, parameterName string) ([]byte, error) {
	input := &ssm.GetParameterInput{
		Name:           aws.String(parameterName),
		WithDecryption: aws.Bool(true), // Always decrypt SecureString parameters
	}

	result, err := awsSSMClient.GetParameter(ctx, input)
	if err != nil {
		return nil, mapAWSError(err, parameterName, "parameter")
	}

	if result.Parameter == nil || result.Parameter.Value == nil {
		return nil, fmt.Errorf("parameter %q has no value", parameterName)
	}

	return []byte(*result.Parameter.Value), nil
}

// Extract a specific field from a JSON secret
func extractJSONField(data []byte, field string) ([]byte, error) {
	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("secret value is not valid JSON: %w", err)
	}

	value, ok := jsonData[field]
	if !ok {
		return nil, fmt.Errorf("field %q not found in JSON secret", field)
	}

	// Handle different value types
	switch v := value.(type) {
	case string:
		return []byte(v), nil
	case nil:
		return []byte{}, nil
	default:
		// For numbers, booleans, and nested objects, return JSON representation
		jsonValue, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal field value: %w", err)
		}
		return jsonValue, nil
	}
}

// Map AWS SDK errors to user-friendly messages
func mapAWSError(err error, name, resourceType string) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ResourceNotFoundException":
			return fmt.Errorf("secret not found: %q", name)
		case "ParameterNotFound":
			return fmt.Errorf("parameter not found: %q", name)
		case "AccessDeniedException":
			return fmt.Errorf("access denied to %s %q: check IAM permissions", resourceType, name)
		case "DecryptionFailure":
			return fmt.Errorf("failed to decrypt %s %q: check KMS permissions", resourceType, name)
		case "InvalidRequestException":
			return fmt.Errorf("invalid request for %s %q: %s", resourceType, name, apiErr.ErrorMessage())
		}
	}
	return fmt.Errorf("failed to retrieve %s %q: %w", resourceType, name, err)
}
