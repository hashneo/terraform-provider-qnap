terraform {
  required_providers {
    qnap = {
      source  = "registry.terraform.io/local/qnap/qnap"
      version = "0.1.0"
    }
  }
}

provider "qnap" {
  host         = "192.168.1.50"
  username     = "admin"
  password     = "yourpassword"
  ssl_insecure = true
}

# Full system snapshot
data "qnap_system_info" "nas" {}

# Storage
data "qnap_volumes" "nas" {}
data "qnap_storage_pools" "nas" {}
data "qnap_shared_folders" "nas" {}
data "qnap_snapshots" "nas" {}

# iSCSI
data "qnap_iscsi_targets" "nas" {}
data "qnap_iscsi_luns" "nas" {}

# Network
data "qnap_network_interfaces" "nas" {}

# Users & access
data "qnap_users" "nas" {}
data "qnap_groups" "nas" {}

# Apps
data "qnap_apps" "nas" {}

# Container Station
data "qnap_containers" "nas" {}
data "qnap_projects" "nas" {}

output "system_info" {
  value = data.qnap_system_info.nas
}

output "volumes" {
  value = data.qnap_volumes.nas.items
}

output "shared_folders" {
  value = data.qnap_shared_folders.nas.items
}

output "containers" {
  value = data.qnap_containers.nas.items
}
