# terraform-provider-qnap

A Terraform provider for QNAP NAS devices running **QTS 5.x**, using the QTS REST API v2.

Pairs with the `terraform-provider-idrac7` provider in this lab repository to give a
complete infrastructure-as-code inventory of a Dell PowerEdge + QNAP lab environment.

---

## Data Sources

| Data Source | Description |
|-------------|-------------|
| `qnap_system_info` | Model, firmware, CPU, RAM, temperature, DNS, NTP |
| `qnap_volumes` | Storage volumes (capacity, filesystem, encryption, compression) |
| `qnap_storage_pools` | Storage pools / RAID groups |
| `qnap_shared_folders` | Shared folders with enabled protocols (SMB/NFS/AFP/FTP) |
| `qnap_iscsi_targets` | iSCSI targets and IQNs |
| `qnap_iscsi_luns` | iSCSI LUNs (size, type, thin provisioning) |
| `qnap_network_interfaces` | NICs, IPs, bonding, VLANs |
| `qnap_snapshots` | Volume snapshots (manual, scheduled, replication) |
| `qnap_users` | Local user accounts |
| `qnap_groups` | Local groups |
| `qnap_apps` | Installed QPKG applications |
| `qnap_containers` | Container Station containers |
| `qnap_projects` | Container Station compose projects |

---

## Build & Install

```bash
# From this directory:
make build    # produces ./terraform-provider-qnap binary
make install  # installs to ~/.terraform.d/plugins/registry.terraform.io/local/qnap/qnap/0.1.0/<os_arch>/
```

After installing, add to your `required_providers`:

```hcl
terraform {
  required_providers {
    qnap = {
      source  = "registry.terraform.io/local/qnap/qnap"
      version = "0.1.0"
    }
  }
}
```

---

## Provider Configuration

```hcl
provider "qnap" {
  host         = "192.168.1.50"   # QNAP IP or hostname
  username     = "admin"
  password     = "yourpassword"
  ssl_insecure = true             # recommended for lab self-signed certs
}
```

| Attribute | Required | Description |
|-----------|----------|-------------|
| `host` | yes | QNAP hostname or IP |
| `username` | yes | Admin username |
| `password` | yes | Admin password (sensitive) |
| `ssl_insecure` | no | Skip TLS cert verification (default: true) |

---

## Authentication

The provider uses QNAP's legacy CGI authentication endpoint (`/cgi-bin/authLogin.cgi`)
to obtain a session ID (SID), then passes the SID as a query parameter on all subsequent
REST API v2 calls. The password is MD5-hashed before sending, as required by QNAP.

---

## API Compatibility

Tested against **QTS 5.x**. The REST v2 API (`/api/v2/`) was introduced in QTS 4.4 and
expanded significantly in QTS 5.0. If you are running QTS 4.x some endpoints may return
errors — these are logged as warnings and the data source returns an empty list rather
than failing the plan.
