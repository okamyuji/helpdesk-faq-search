terraform {
  required_version = "~> 1.17.2"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.73.0"
    }
  }
}

provider "aws" {
  region = var.region
  default_tags {
    tags = { Project = local.name }
  }
}

locals {
  name       = "helpdesk-faq-search"
  rerank_arn = "arn:aws:bedrock:${var.region}::foundation-model/${var.rerank_model_id}"
}

data "aws_caller_identity" "me" {}

# The default VPC's subnets are public; the task gets a public IP for ECR and SSM, so no NAT gateway is needed (ADR 0004).
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

resource "aws_security_group" "app" {
  name        = "${local.name}-app"
  description = "No inbound rules; the page is opened through ECS Exec port forwarding."
  vpc_id      = data.aws_vpc.default.id
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "db" {
  name        = "${local.name}-db"
  description = "PostgreSQL from the app only."
  vpc_id      = data.aws_vpc.default.id
  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
}

# RDS needs subnets in two AZs; this account's default VPC has a subnet in only one, so this adds a second.
resource "aws_subnet" "db_second_az" {
  vpc_id                  = data.aws_vpc.default.id
  availability_zone       = var.db_second_az
  cidr_block              = var.db_second_az_cidr
  map_public_ip_on_launch = false
  tags                    = { Name = "${local.name}-db" }
}

resource "aws_db_subnet_group" "db" {
  name       = local.name
  subnet_ids = concat(data.aws_subnets.default.ids, [aws_subnet.db_second_az.id])
}

resource "aws_db_instance" "db" {
  count                        = var.db_enabled ? 1 : 0
  identifier                   = local.name
  engine                       = "postgres"
  engine_version               = "18.6"
  instance_class               = "db.t4g.micro"
  allocated_storage            = 20
  storage_type                 = "gp3"
  db_name                      = "faq"
  username                     = "faq"
  manage_master_user_password  = true
  snapshot_identifier          = var.restore_snapshot_id
  db_subnet_group_name         = aws_db_subnet_group.db.name
  vpc_security_group_ids       = [aws_security_group.db.id]
  publicly_accessible          = false
  storage_encrypted            = true
  backup_retention_period      = 0
  skip_final_snapshot          = var.skip_final_snapshot
  final_snapshot_identifier    = "${local.name}-final"
  apply_immediately            = true
  auto_minor_version_upgrade   = false
  copy_tags_to_snapshot        = true
  performance_insights_enabled = false
  deletion_protection          = false
}

resource "aws_ecr_repository" "app" {
  name                 = "${local.name}/app"
  force_delete         = true
  image_tag_mutability = "MUTABLE"
}

resource "aws_ecr_repository" "tei" {
  name                 = "${local.name}/tei"
  force_delete         = true
  image_tag_mutability = "MUTABLE"
}

resource "aws_cloudwatch_log_group" "ecs" {
  name              = "/ecs/${local.name}"
  retention_in_days = 7
}

resource "aws_ecs_cluster" "main" {
  name = local.name
}

data "aws_iam_policy_document" "ecs_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${local.name}-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "execution_secret" {
  count = var.db_enabled ? 1 : 0
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_db_instance.db[0].master_user_secret[0].secret_arn]
  }
}

resource "aws_iam_role_policy" "execution_secret" {
  count  = var.db_enabled ? 1 : 0
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_secret[0].json
}

resource "aws_iam_role" "task" {
  name               = "${local.name}-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
}

data "aws_iam_policy_document" "task" {
  statement {
    sid       = "InvokeRerankModel"
    actions   = ["bedrock:InvokeModel"]
    resources = [local.rerank_arn]
  }
  statement {
    # bedrock:Rerank is checked against no specific resource, so it cannot be narrowed further.
    sid       = "Rerank"
    actions   = ["bedrock:Rerank"]
    resources = ["*"]
  }
  statement {
    sid       = "EcsExec"
    actions   = ["ssmmessages:CreateControlChannel", "ssmmessages:CreateDataChannel", "ssmmessages:OpenControlChannel", "ssmmessages:OpenDataChannel"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "task" {
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task.json
}

locals {
  ecr = "${data.aws_caller_identity.me.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  log = { logDriver = "awslogs", options = { "awslogs-group" = aws_cloudwatch_log_group.ecs.name, "awslogs-region" = var.region } }
  db_env = var.db_enabled ? [
    { name = "DATABASE_URL", value = "postgres://faq@${aws_db_instance.db[0].address}:5432/faq?sslmode=require" },
  ] : []
  db_secret = var.db_enabled ? [
    { name = "PGPASSWORD", valueFrom = "${aws_db_instance.db[0].master_user_secret[0].secret_arn}:password::" },
  ] : []
}

resource "aws_ecs_task_definition" "app" {
  family                   = local.name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.task_cpu
  memory                   = var.task_memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([
    {
      name         = "app"
      image        = "${local.ecr}/${aws_ecr_repository.app.name}:${var.app_image_tag}"
      essential    = true
      portMappings = [{ containerPort = 8080 }]
      environment = concat(local.db_env, [
        { name = "TEI_EMBED_URL", value = "http://localhost:8081" },
        { name = "RERANK_MODEL_ARN", value = local.rerank_arn },
        { name = "EMBEDDING_MODEL", value = var.embedding_model },
        { name = "AWS_REGION", value = var.region },
      ])
      secrets          = local.db_secret
      dependsOn        = [{ containerName = "tei", condition = "HEALTHY" }]
      logConfiguration = merge(local.log, { options = merge(local.log.options, { "awslogs-stream-prefix" = "app" }) })
    },
    {
      name             = "tei"
      image            = "${local.ecr}/${aws_ecr_repository.tei.name}:${var.embedding_model}"
      essential        = true
      command          = ["--port", "8081"]
      healthCheck      = { command = ["CMD-SHELL", "curl -sf localhost:8081/health || exit 1"], interval = 10, timeout = 5, retries = 3, startPeriod = 120 }
      logConfiguration = merge(local.log, { options = merge(local.log.options, { "awslogs-stream-prefix" = "tei" }) })
    },
  ])
}

resource "aws_ecs_service" "app" {
  name                   = local.name
  cluster                = aws_ecs_cluster.main.id
  task_definition        = aws_ecs_task_definition.app.arn
  desired_count          = var.app_desired_count
  launch_type            = "FARGATE"
  enable_execute_command = true
  network_configuration {
    subnets          = data.aws_subnets.default.ids
    security_groups  = [aws_security_group.app.id]
    assign_public_ip = true
  }
}

resource "aws_budgets_budget" "monthly" {
  name         = local.name
  budget_type  = "COST"
  limit_amount = tostring(var.budget_limit_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  cost_filter {
    name   = "TagKeyValue"
    values = ["user:Project$${local.name}"]
  }
  dynamic "notification" {
    for_each = length(var.budget_emails) > 0 ? [{ t = 80, k = "ACTUAL" }, { t = 100, k = "ACTUAL" }, { t = 100, k = "FORECASTED" }] : []
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = notification.value.t
      threshold_type             = "PERCENTAGE"
      notification_type          = notification.value.k
      subscriber_email_addresses = var.budget_emails
    }
  }
}
