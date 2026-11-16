output "cluster" {
  value = aws_ecs_cluster.main.name
}

output "service" {
  value = aws_ecs_service.app.name
}

output "task_definition" {
  value = aws_ecs_task_definition.app.family
}

output "subnets" {
  value = data.aws_subnets.default.ids
}

output "app_security_group" {
  value = aws_security_group.app.id
}

output "ecr_app" {
  value = aws_ecr_repository.app.repository_url
}

output "ecr_tei" {
  value = aws_ecr_repository.tei.repository_url
}
