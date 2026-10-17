# Create (or ensure) an ECR repo exists
resource "aws_ecr_repository" "this" {
  name = var.repository_name
  force_delete = true
  # You can add lifecycle_policy, scan_on_push, etc., here
}
