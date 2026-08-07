#!/usr/bin/env bash
#
# verify-aws.sh — checks OpenBench's AWS prerequisites and pre-creates the
# remote-state resources the orchestrator needs for `run --mode k8s`:
#
#   1. OPENBENCH_STATE_BUCKET   (S3, also hosts the staged k3s kubeconfig)
#   2. OPENBENCH_STATE_LOCK_TABLE (DynamoDB, Terraform state locking)
#   3. OPENBENCH_STATE_REGION
#
# Safe to re-run: detects existing resources and skips creation.
set -euo pipefail

log()  { printf '\033[36m[verify]\033[0m %s\n' "$*"; }
err()  { printf '\033[31m[verify]\033[0m %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

# ---- 1. Tool presence -------------------------------------------------------
log "checking tools..."
for tool in aws terraform kubectl; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done
log "tool versions:"
aws --version 2>&1        | sed 's/^/  /'
terraform version            | head -1 | sed 's/^/  /'
kubectl version --client 2>/dev/null | head -1 | sed 's/^/  /'

if [ -x ./orchestrator/openbench ]; then
  log "openbench binary found at ./orchestrator/openbench"
else
  log "openbench binary not found; building ./orchestrator/openbench ..."
  (cd orchestrator && go build -o openbench ./cmd/openbench)
fi

# ---- 2. AWS credentials -----------------------------------------------------
log "checking AWS credentials..."
IDENTITY="$(aws sts get-caller-identity 2>/dev/null || true)"
if [ -z "$IDENTITY" ]; then
  die "no AWS credentials found. Run 'aws configure' or set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY."
fi
echo "$IDENTITY" | sed 's/^/  /'
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"

# ---- 3. Region --------------------------------------------------------------
# Precedence: OPENBENCH_STATE_REGION, then AWS_REGION, then the profile's
# configured default region, then us-east-1 as a last resort.
PROFILE_REGION="$(aws configure get region 2>/dev/null || true)"
REGION="${OPENBENCH_STATE_REGION:-${AWS_REGION:-${PROFILE_REGION:-us-east-1}}}"
log "state region: $REGION"

# ---- 4. Bucket --------------------------------------------------------------
BUCKET="${OPENBENCH_STATE_BUCKET:-openbench-state-${ACCOUNT}}"
log "ensuring S3 state bucket s3://$BUCKET ..."
if aws s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then
  log "  bucket already exists"
else
  log "  creating bucket..."
  if [ "$REGION" = "us-east-1" ]; then
    aws s3api create-bucket --bucket "$BUCKET" --region "$REGION" >/dev/null
  else
    aws s3api create-bucket --bucket "$BUCKET" --region "$REGION" \
      --create-bucket-configuration LocationConstraint="$REGION" >/dev/null
  fi
  log "  enabling versioning (Terraform backend best practice)..."
  aws s3api put-bucket-versioning --bucket "$BUCKET" \
    --versioning-configuration Status=Enabled >/dev/null
fi

# Encryption + handoff hygiene are applied idempotently even to pre-existing
# buckets, so re-running verify-aws converges the configuration.
log "  enabling SSE-S3 default encryption..."
aws s3api put-bucket-encryption --bucket "$BUCKET" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}' \
  >/dev/null 2>&1 || log "  (could not set encryption; skipping)"

log "  adding lifecycle to expire staged run handoffs under kubeconfig/ ..."
aws s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" \
  --lifecycle-configuration '{"Rules":[{"ID":"openbench-handoffs","Status":"Enabled","Prefix":"kubeconfig/","Expiration":{"Days":1},"NoncurrentVersionExpiration":{"NoncurrentDays":1}}]}' \
  >/dev/null 2>&1 || log "  (could not set lifecycle; skipping)"

# ---- 5. Logs bucket (job storage.type: s3) --------------------------------
LOGS_BUCKET="${OPENBENCH_LOGS_BUCKET:-openbench-logs-${ACCOUNT}}"
log "ensuring S3 logs bucket s3://$LOGS_BUCKET ..."
if aws s3api head-bucket --bucket "$LOGS_BUCKET" >/dev/null 2>&1; then
  log "  bucket already exists"
else
  log "  creating bucket..."
  if [ "$REGION" = "us-east-1" ]; then
    aws s3api create-bucket --bucket "$LOGS_BUCKET" --region "$REGION" >/dev/null
  else
    aws s3api create-bucket --bucket "$LOGS_BUCKET" --region "$REGION" \
      --create-bucket-configuration LocationConstraint="$REGION" >/dev/null
  fi
fi
log "  enabling versioning + SSE-S3 on logs bucket..."
aws s3api put-bucket-versioning --bucket "$LOGS_BUCKET" \
  --versioning-configuration Status=Enabled >/dev/null 2>&1 || true
aws s3api put-bucket-encryption --bucket "$LOGS_BUCKET" \
  --server-side-encryption-configuration '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}' \
  >/dev/null 2>&1 || true

# ---- 6. DynamoDB lock table -------------------------------------------------
LOCK="${OPENBENCH_STATE_LOCK_TABLE:-openbench-tf-locks-${ACCOUNT}}"
log "ensuring DynamoDB lock table $LOCK ..."
if aws dynamodb describe-table --table-name "$LOCK" --region "$REGION" >/dev/null 2>&1; then
  log "  table already exists"
else
  log "  creating table..."
  aws dynamodb create-table --table-name "$LOCK" --region "$REGION" \
    --attribute-definitions AttributeName=LockID,AttributeType=S \
    --key-schema AttributeName=LockID,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST >/dev/null
  log "  waiting for table to become ACTIVE..."
  aws dynamodb wait table-exists --table-name "$LOCK" --region "$REGION"
fi

# ---- 7. Summary -------------------------------------------------------------
cat <<EOF

OpenBench AWS verification complete.

Export these before running a job:
  export OPENBENCH_STATE_BUCKET=$BUCKET
  export OPENBENCH_STATE_REGION=$REGION
  export OPENBENCH_STATE_LOCK_TABLE=$LOCK

Point the job's storage.bucket at the logs bucket (S3 names are global, so
edit config/examples/example-job-aws-k8s.yaml):
  storage.bucket: $LOGS_BUCKET

Then validate a job and run:
  ./orchestrator/openbench validate config/examples/example-job-aws-k8s.yaml
  ./orchestrator/openbench run --mode k8s config/examples/example-job-aws-k8s.yaml
EOF