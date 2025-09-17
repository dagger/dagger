package azblob

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/pkg/errors"
)

const (
	attrSecretAccessKey = "secret_access_key"
	attrAccountName     = "account_name"
	attrAccountURL      = "account_url"
	attrPrefix          = "prefix"
	attrManifestsPrefix = "manifests_prefix"
	attrBlobsPrefix     = "blobs_prefix"
	attrName            = "name"
	attrContainer       = "container"
	IOConcurrency       = 4
	IOChunkSize         = 32 * 1024 * 1024
)

type Config struct {
	AccountURL      string
	Container       string
	Prefix          string
	ManifestsPrefix string
	BlobsPrefix     string
	Names           []string
	AccountName     string
	secretAccessKey string
}

func getConfig(attrs map[string]string) (*Config, error) {
	accountURLString, ok := attrs[attrAccountURL]
	if !ok {
		accountURLString, ok = os.LookupEnv("BUILDKIT_AZURE_STORAGE_ACCOUNT_URL")
		if !ok {
			return &Config{}, errors.New("either ${BUILDKIT_AZURE_STORAGE_ACCOUNT_URL} or account_url attribute is required for azblob cache")
		}
	}

	accountURL, err := url.Parse(accountURLString)
	if err != nil {
		return &Config{}, errors.Wrap(err, "azure storage account url provided is not a valid url")
	}

	accountName, ok := attrs[attrAccountName]
	if !ok {
		accountName, ok = os.LookupEnv("BUILDKIT_AZURE_STORAGE_ACCOUNT_NAME")
		if !ok {
			accountName = strings.Split(accountURL.Hostname(), ".")[0]
		}
	}
	if accountName == "" {
		return &Config{}, errors.New("unable to retrieve account name from account url or ${BUILDKIT_AZURE_STORAGE_ACCOUNT_NAME} or account_name attribute for azblob cache")
	}

	container, ok := attrs[attrContainer]
	if !ok {
		container, ok = os.LookupEnv("BUILDKIT_AZURE_STORAGE_CONTAINER")
		if !ok {
			container = "buildkit-cache"
		}
	}

	prefix, ok := attrs[attrPrefix]
	if !ok {
		prefix, _ = os.LookupEnv("BUILDKIT_AZURE_STORAGE_PREFIX")
	}

	manifestsPrefix, ok := attrs[attrManifestsPrefix]
	if !ok {
		manifestsPrefix = "manifests"
	}

	blobsPrefix, ok := attrs[attrBlobsPrefix]
	if !ok {
		blobsPrefix = "blobs"
	}

	names := []string{"buildkit"}
	name, ok := attrs[attrName]
	if ok {
		splittedNames := strings.Split(name, ";")
		if len(splittedNames) > 0 {
			names = splittedNames
		}
	}

	secretAccessKey := attrs[attrSecretAccessKey]

	config := Config{
		AccountURL:      accountURLString,
		AccountName:     accountName,
		Container:       container,
		Prefix:          prefix,
		Names:           names,
		ManifestsPrefix: manifestsPrefix,
		BlobsPrefix:     blobsPrefix,
		secretAccessKey: secretAccessKey,
	}

	return &config, nil
}

func createContainerClient(ctx context.Context, config *Config) (*azblob.ContainerClient, error) {
	var serviceClient *azblob.ServiceClient
	if config.secretAccessKey != "" {
		sharedKey, err := azblob.NewSharedKeyCredential(config.AccountName, config.secretAccessKey)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create shared key")
		}
		serviceClient, err = azblob.NewServiceClientWithSharedKey(config.AccountURL, sharedKey, &azblob.ClientOptions{})
		if err != nil {
			return nil, errors.Wrap(err, "failed to created service client from shared key")
		}
	} else {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create default azure credentials")
		}

		serviceClient, err = azblob.NewServiceClient(config.AccountURL, cred, &azblob.ClientOptions{})
		if err != nil {
			return nil, errors.Wrap(err, "failed to create service client")
		}
	}

	ctx, cnclFn := context.WithCancelCause(ctx)
	ctx, _ = context.WithTimeoutCause(ctx, time.Second*60, errors.WithStack(context.DeadlineExceeded))
	defer cnclFn(errors.WithStack(context.Canceled))

	containerClient, err := serviceClient.NewContainerClient(config.Container)
	if err != nil {
		return nil, errors.Wrap(err, "error creating container client")
	}

	_, err = containerClient.GetProperties(ctx, &azblob.ContainerGetPropertiesOptions{})
	if err == nil {
		return containerClient, nil
	}

	var se *azblob.StorageError
	if errors.As(err, &se) && se.ErrorCode == azblob.StorageErrorCodeContainerNotFound {
		ctx, cnclFn := context.WithCancelCause(ctx)
		ctx, _ = context.WithTimeoutCause(ctx, time.Minute*5, errors.WithStack(context.DeadlineExceeded))
		defer cnclFn(errors.WithStack(context.Canceled))
		_, err := containerClient.Create(ctx, &azblob.ContainerCreateOptions{})
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create cache container %s", config.Container)
		}

		return containerClient, nil
	}

	return nil, errors.Wrapf(err, "failed to get properties of cache container %s", config.Container)
}

func manifestKey(config *Config, name string) string {
	key := filepath.Join(config.Prefix, config.ManifestsPrefix, name)
	return key
}

func blobKey(config *Config, digest string) string {
	key := filepath.Join(config.Prefix, config.BlobsPrefix, digest)
	return key
}

func blobExists(ctx context.Context, containerClient *azblob.ContainerClient, blobKey string) (bool, error) {
	blobClient, err := containerClient.NewBlobClient(blobKey)
	if err != nil {
		return false, errors.Wrap(err, "error creating blob client")
	}

	ctx, cnclFn := context.WithCancelCause(ctx)
	ctx, _ = context.WithTimeoutCause(ctx, time.Second*60, errors.WithStack(context.DeadlineExceeded))
	defer cnclFn(errors.WithStack(context.Canceled))
	_, err = blobClient.GetProperties(ctx, &azblob.BlobGetPropertiesOptions{})
	if err == nil {
		return true, nil
	}

	var se *azblob.StorageError
	if errors.As(err, &se) && se.ErrorCode == azblob.StorageErrorCodeBlobNotFound {
		return false, nil
	}

	return false, errors.Wrapf(err, "failed to check blob %s existence", blobKey)
}
