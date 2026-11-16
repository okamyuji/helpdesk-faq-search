variable "region" {
  type    = string
  default = "ap-northeast-1"
}

variable "embedding_model" {
  type    = string
  default = "ruri-v3-30m"
  validation {
    condition     = contains(["ruri-v3-30m", "ruri-v3-130m", "ruri-v3-310m"], var.embedding_model)
    error_message = "embedding_model must be ruri-v3-30m, ruri-v3-130m or ruri-v3-310m."
  }
}

variable "app_image_tag" {
  type    = string
  default = "latest"
}

variable "app_desired_count" {
  description = "0 stops the service between measurements (ADR 0006)."
  type        = number
  default     = 0
}

# 30m needs 1.88GiB at TEI start-up (ADR 0003); 310m needs 5.79GiB.
variable "task_cpu" {
  type    = number
  default = 1024
}

variable "task_memory" {
  type    = number
  default = 3072
}

variable "db_enabled" {
  description = "false deletes the RDS instance (ADR 0006)."
  type        = bool
  default     = true
}

variable "restore_snapshot_id" {
  description = "Restore RDS from this snapshot instead of creating an empty database."
  type        = string
  default     = null
}

variable "skip_final_snapshot" {
  description = "Set true with `terraform apply` before the final destroy; destroy reads the value from state, so passing it to destroy alone still leaves a final snapshot."
  type        = bool
  default     = false
}

variable "budget_limit_usd" {
  type    = number
  default = 30
}

variable "budget_emails" {
  description = "Empty creates the budget without email alerts."
  type        = list(string)
  default     = []
}

variable "rerank_model_id" {
  type    = string
  default = "cohere.rerank-v3-5:0"
}

variable "db_second_az" {
  description = "An AZ the default VPC has no subnet in."
  type        = string
  default     = "ap-northeast-1a"
}

variable "db_second_az_cidr" {
  description = "A free range inside the default VPC (172.31.0.0/16)."
  type        = string
  default     = "172.31.240.0/24"
}
