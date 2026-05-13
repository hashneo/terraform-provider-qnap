# terraform-provider-qnap

A custom Terraform provider for QNAP NAS devices running QTS 5.x. Uses the legacy CGI auth endpoint to obtain a session token, then calls QTS REST v2 APIs for inventory data.

Tested against: QNAP TVS-EC880, QTS 5.2.7.

---

## Requirements

- [Go](https://golang.org/) >= 1.21
- [Terraform](https://www.terraform.io/) >= 1.5.0
- macOS: `codesign` (included with Xcode Command Line Tools)

---

## Building and installing

```bash
make install
```

This builds the provider binary and copies it to `~/.terraform.d/plugins/registry.terraform.io/local/qnap/qnap/0.1.0/<os>_<arch>/`.

On **macOS** you must sign the binary after install to avoid Gatekeeper killing it:

```bash
codesign --sign - ~/.terraform.d/plugins/registry.terraform.io/local/qnap/qnap/0.1.0/darwin_arm64/terraform-provider-qnap
```

---

## Terraform configuration

### `~/.terraformrc` — dev overrides

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/local/qnap/qnap" = "/Users/<you>/.terraform.d/plugins/registry.terraform.io/local/qnap/qnap/0.1.0/darwin_arm64"
  }
  direct {}
}
```

### `required_providers` block

```hcl
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    qnap = {
      source  = "registry.terraform.io/local/qnap/qnap"
      version = "0.1.0"
    }
  }
}

provider "qnap" {
  host         = "https://192.168.1.60"   # NAS URL (scheme + host, optional port)
  username     = "admin"
  password     = "yourpassword"
  ssl_insecure = true                   # required for self-signed NAS certificates
}
```

---

## Data sources

| Data source | Description |
|---|---|
| `qnap_system_info` | NAS model, firmware version, CPU, RAM, temperatures, timezone, NTP, DNS. Uses the legacy CGI sysinfo endpoint — works on all QTS versions. |
| `qnap_volumes` | Storage volumes: capacity, used/free space, filesystem, encryption, compression, dedup, thin provisioning. |
| `qnap_storage_pools` | Storage pools (RAID groups): RAID type, capacity, disk count, spare count. |
| `qnap_shared_folders` | Shared folders: path, volume, enabled protocols (SMB/NFS/AFP/FTP), encryption, read-only, hidden. |
| `qnap_iscsi_targets` | iSCSI targets: IQN, status, enabled state. |
| `qnap_iscsi_luns` | iSCSI LUNs: target, size, used space, type (File/Block), thin provisioning. |
| `qnap_snapshots` | Volume snapshots: type (Manual/Schedule/Replication), created time, size. |
| `qnap_network_interfaces` | NICs: MAC, IP addresses, speed, duplex, gateway, MTU, bond mode, VLAN. |
| `qnap_users` | Local user accounts: UID, email, enabled state, group memberships. |
| `qnap_groups` | Local groups: GID, description, member list. |
| `qnap_apps` | Installed QPKG applications: version, author, running status, enabled state. |
| `qnap_containers` | Container Station containers: image, status, runtime (docker/podman), project, CPU%, memory. |
| `qnap_projects` | Container Station compose projects: status, path. |

---

## Example

```hcl
data "qnap_system_info" "nas" {}

output "nas_model" {
  value = data.qnap_system_info.nas.model
}

output "nas_firmware" {
  value = data.qnap_system_info.nas.firmware
}

data "qnap_volumes" "nas" {}

output "volumes" {
  value = data.qnap_volumes.nas.items
}

data "qnap_shared_folders" "nas" {}

output "shares" {
  value = data.qnap_shared_folders.nas.items
}
```

See [`examples/main.tf`](examples/main.tf) for a more complete example.

---

## qnap-report — standalone HTML inventory report

A standalone tool that connects to a QNAP NAS and produces a self-contained HTML (or JSON) inventory report without requiring Terraform.

### Run

```bash
go run ./cmd/qnap-report/ \
  --host     https://192.168.1.60 \
  --user     admin \
  --password yourpassword \
  --out      qnap-report.html
```

Or use environment variables:

```bash
export QNAP_HOST=https://192.168.1.60
export QNAP_USER=admin
export QNAP_PASSWORD=yourpassword

go run ./cmd/qnap-report/ --out qnap-report.html
```

### Flags

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--host` | `QNAP_HOST` | _(required)_ | NAS URL — include scheme, e.g. `https://192.168.1.60` or `http://192.168.1.60:8080` |
| `--user` | `QNAP_USER` | `admin` | NAS username |
| `--password` | `QNAP_PASSWORD` | _(required)_ | NAS password |
| `--out` | — | `qnap-report.html` | Output file path. Use `.json` extension for JSON output |

### Output

The report covers 13 sections across 5 groups:

| Group | Sections |
|---|---|
| System | System Info |
| Storage | Volumes, Storage Pools, Shared Folders, iSCSI Targets, iSCSI LUNs, Snapshots |
| Network | Network Interfaces |
| Services | Installed Apps (QPKG), Containers, Compose Projects |
| Users | Local Users, Local Groups |

Fetches run in parallel. A full report typically completes in under 5 seconds.

### JSON output

```bash
go run ./cmd/qnap-report/ --host https://192.168.1.60 --password yourpassword --out qnap-report.json
```

---

## Authentication

QNAP QTS uses a two-step auth flow:

1. **CGI login** — `POST /cgi-bin/authLogin.cgi` with base64-encoded password → returns an XML body containing a `SID` (session token).
2. **REST API** — all subsequent requests include `?sid=<SID>` as a query parameter.

The `system_info` data source uses an older CGI endpoint (`manaRequest.cgi?subfunc=sysinfo`) that returns XML and works on all QTS versions. The remaining data sources use the QTS REST v2 API (`/api/v2/...`).

## Known limitations

- **QTS REST v2 availability.** The `/api/v2/` endpoints may not be accessible on all NAS configurations. If list endpoints return empty results, verify that the NAS API server is enabled and accessible on the configured host/port.
- **Read-only.** This provider currently implements data sources only. No managed resources.
- **TLS.** Self-signed certificates are common on home NAS devices; set `ssl_insecure = true` in the provider config.
