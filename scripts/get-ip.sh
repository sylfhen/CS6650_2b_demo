#!/usr/bin/env bash
# Prints the public IP of each running task in the deployed ECS service.
# Usage (from the repo root):  ./scripts/get-ip.sh
#                              IP=$(./scripts/get-ip.sh)
set -euo pipefail
REGION="${AWS_REGION:-us-west-2}"
CLUSTER=$(terraform -chdir=terraform output -raw ecs_cluster_name)
SERVICE=$(terraform -chdir=terraform output -raw ecs_service_name)

echo "Waiting for service $SERVICE in $CLUSTER to become stable..." >&2
aws ecs wait services-stable --cluster "$CLUSTER" --services "$SERVICE" --region "$REGION"

for TASK in $(aws ecs list-tasks --cluster "$CLUSTER" --service-name "$SERVICE" \
    --desired-status RUNNING --region "$REGION" --query 'taskArns[]' --output text); do
  ENI=$(aws ecs describe-tasks --cluster "$CLUSTER" --tasks "$TASK" --region "$REGION" \
    --query 'tasks[0].attachments[0].details[?name==`networkInterfaceId`].value' --output text)
  aws ec2 describe-network-interfaces --network-interface-ids "$ENI" --region "$REGION" \
    --query 'NetworkInterfaces[0].Association.PublicIp' --output text
done
