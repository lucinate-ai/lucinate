#!/bin/bash
# Generate new access keys for the openclaw-bedrock user

set -e

USER_NAME="openclaw-bedrock"

echo "Creating access keys for user: $USER_NAME"
echo ""

aws iam create-access-key --user-name "$USER_NAME" --output json | tee /tmp/access-keys.json

echo ""
echo "=========================================="
echo "SAVE THESE CREDENTIALS SECURELY!"
echo "=========================================="
echo ""
echo "AccessKeyId:     $(jq -r '.AccessKey.AccessKeyId' /tmp/access-keys.json)"
echo "SecretAccessKey: $(jq -r '.AccessKey.SecretAccessKey' /tmp/access-keys.json)"
echo ""
echo "Either set them:"
echo ""
echo "  export AWS_ACCESS_KEY_ID=$(jq -r '.AccessKey.AccessKeyId' /tmp/access-keys.json)"
echo "  export AWS_SECRET_ACCESS_KEY=$(jq -r '.AccessKey.SecretAccessKey' /tmp/access-keys.json)"
echo "  export AWS_REGION=us-east-1"
echo ""
echo "Or add to ~/.aws/credentials:"
echo ""
echo "  [default]"
echo "  aws_access_key_id = $(jq -r '.AccessKey.AccessKeyId' /tmp/access-keys.json)"
echo "  aws_secret_access_key = <secret-from-above>"
echo "  region = us-east-1"
echo ""

# Don't leave secrets sitting around
rm -f /tmp/access-keys.json
