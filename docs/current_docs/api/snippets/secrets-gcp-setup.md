# Setting up GCP Secret Manager with Dagger

## Prerequisites

1. A Google Cloud Project with Secret Manager API enabled
2. Authentication credentials (one of the following):
   - Service account key file
   - Application Default Credentials (ADC) via `gcloud` CLI
   - Workload Identity (on GKE)
   - GCE metadata service (on Compute Engine)

## Setup Instructions

### 1. Enable Secret Manager API

```bash
gcloud services enable secretmanager.googleapis.com
```

### 2. Create a secret

```bash
# Create a secret
echo -n "my-secret-value" | gcloud secrets create my-api-key --data-file=-

# Or create from a file
gcloud secrets create my-api-key --data-file=path/to/secret.txt
```

### 3. Set up authentication

#### Option A: Service Account Key (for CI/CD)

```bash
# Create a service account
gcloud iam service-accounts create dagger-secrets \
    --display-name="Dagger Secret Manager"

# Grant access to Secret Manager
gcloud projects add-iam-policy-binding PROJECT_ID \
    --member="serviceAccount:dagger-secrets@PROJECT_ID.iam.gserviceaccount.com" \
    --role="roles/secretmanager.secretAccessor"

# Create and download key
gcloud iam service-accounts keys create key.json \
    --iam-account=dagger-secrets@PROJECT_ID.iam.gserviceaccount.com

# Set environment variable
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/key.json"
export GCP_PROJECT_ID="PROJECT_ID"
```

#### Option B: Application Default Credentials (for local development)

```bash
# Login with gcloud
gcloud auth application-default login

# Set project
export GCP_PROJECT_ID="PROJECT_ID"
```

### 4. Use with Dagger

```bash
# Simple usage
dagger call my-function --api-key=gcp://my-api-key

# With specific version
dagger call my-function --api-key=gcp://my-api-key/versions/2

# With full path (no GCP_PROJECT_ID needed)
dagger call my-function --api-key=gcp://projects/my-project/secrets/my-api-key

# With caching TTL
dagger call my-function --api-key="gcp://my-api-key?ttl=5m"
```

## Security Best Practices

1. **Use least privilege**: Only grant `secretmanager.secretAccessor` role, not admin roles
2. **Rotate service account keys**: Regularly rotate service account keys used in CI/CD
3. **Use Workload Identity on GKE**: Avoid using service account keys when running on GKE
4. **Audit access**: Enable Cloud Audit Logs to track secret access
5. **Use secret versions**: Pin to specific versions in production for stability

## Troubleshooting

### Authentication errors

```bash
# Check current authentication
gcloud auth list

# Verify project
gcloud config get-value project

# Test secret access
gcloud secrets versions access latest --secret="my-api-key"
```

### Common environment variables

- `GOOGLE_APPLICATION_CREDENTIALS`: Path to service account key file
- `GCP_PROJECT_ID`: Google Cloud project ID (fallback: `GOOGLE_CLOUD_PROJECT`, `GCLOUD_PROJECT`)
- `GOOGLE_CLOUD_PROJECT`: Alternative project ID variable
- `GCLOUD_PROJECT`: Legacy project ID variable

