// Package datasources implements all QNAP Terraform data sources.
//
// Data sources provided:
//   - qnap_system_info       — NAS model, firmware, CPU, RAM, temperature
//   - qnap_volumes           — storage volumes
//   - qnap_storage_pools     — storage pools / RAID groups
//   - qnap_shared_folders    — shared folders (SMB/NFS/AFP)
//   - qnap_iscsi_targets     — iSCSI targets
//   - qnap_iscsi_luns        — iSCSI LUNs
//   - qnap_network_interfaces — NIC configuration
//   - qnap_snapshots         — volume snapshots
//   - qnap_users             — local user accounts
//   - qnap_groups            — local groups
//   - qnap_apps              — installed QPKG applications
//   - qnap_containers        — Container Station containers
//   - qnap_projects          — Container Station compose projects
package datasources

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steventaylor/terraform-provider-qnap/internal/client"
)

// -----------------------------------------------------------------------
// System Info
// -----------------------------------------------------------------------

var _ datasource.DataSource = (*SystemInfoDataSource)(nil)

type SystemInfoDataSource struct{ client *client.Client }

func NewSystemInfoDataSource() datasource.DataSource { return &SystemInfoDataSource{} }

func (d *SystemInfoDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_info"
}

func (d *SystemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

type systemInfoModel struct {
	ID           types.String `tfsdk:"id"`
	Hostname     types.String `tfsdk:"hostname"`
	Model        types.String `tfsdk:"model"`
	SerialNumber types.String `tfsdk:"serial_number"`
	Firmware     types.String `tfsdk:"firmware"`
	Build        types.String `tfsdk:"build"`
	FirmwareDate types.String `tfsdk:"firmware_date"`
	UptimeSecs   types.Int64  `tfsdk:"uptime_secs"`
	CPUModel     types.String `tfsdk:"cpu_model"`
	CPUCores     types.Int64  `tfsdk:"cpu_cores"`
	TotalRAMMB   types.Int64  `tfsdk:"total_ram_mb"`
	FreeRAMMB    types.Int64  `tfsdk:"free_ram_mb"`
	TempC        types.Int64  `tfsdk:"cpu_temp_c"`
	SystemTempC  types.Int64  `tfsdk:"system_temp_c"`
	TimeZone     types.String `tfsdk:"time_zone"`
	NTPServer    types.String `tfsdk:"ntp_server"`
	DNSPrimary   types.String `tfsdk:"dns_primary"`
	DNSSecondary types.String `tfsdk:"dns_secondary"`
}

func (d *SystemInfoDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads QNAP NAS system information — model, firmware, CPU, RAM, temperature, network config.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Computed: true},
			"hostname":      schema.StringAttribute{Computed: true},
			"model":         schema.StringAttribute{Computed: true},
			"serial_number": schema.StringAttribute{Computed: true},
			"firmware":      schema.StringAttribute{Computed: true},
			"build":         schema.StringAttribute{Computed: true},
			"firmware_date": schema.StringAttribute{Computed: true},
			"uptime_secs":   schema.Int64Attribute{Computed: true},
			"cpu_model":     schema.StringAttribute{Computed: true},
			"cpu_cores":     schema.Int64Attribute{Computed: true},
			"total_ram_mb":  schema.Int64Attribute{Computed: true},
			"free_ram_mb":   schema.Int64Attribute{Computed: true},
			"cpu_temp_c":    schema.Int64Attribute{Computed: true},
			"system_temp_c": schema.Int64Attribute{Computed: true},
			"time_zone":     schema.StringAttribute{Computed: true},
			"ntp_server":    schema.StringAttribute{Computed: true},
			"dns_primary":   schema.StringAttribute{Computed: true},
			"dns_secondary": schema.StringAttribute{Computed: true},
		},
	}
}

func (d *SystemInfoDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.SystemInfo()
	if err != nil {
		resp.Diagnostics.AddError("Could not read QNAP system info", err.Error())
		return
	}
	state := systemInfoModel{
		ID:           types.StringValue(d.client.Host),
		Hostname:     types.StringValue(info.Hostname),
		Model:        types.StringValue(info.Model),
		SerialNumber: types.StringValue(info.SerialNumber),
		Firmware:     types.StringValue(info.Firmware),
		Build:        types.StringValue(info.Build),
		FirmwareDate: types.StringValue(info.FirmwareDate),
		UptimeSecs:   types.Int64Value(info.Uptime),
		CPUModel:     types.StringValue(info.CPUModel),
		CPUCores:     types.Int64Value(int64(info.CPUCores)),
		TotalRAMMB:   types.Int64Value(int64(info.TotalRAMMB)),
		FreeRAMMB:    types.Int64Value(int64(info.FreeRAMMB)),
		TempC:        types.Int64Value(int64(info.TemperatureC)),
		SystemTempC:  types.Int64Value(int64(info.SystemTempC)),
		TimeZone:     types.StringValue(info.TimeZone),
		NTPServer:    types.StringValue(info.NTPServer),
		DNSPrimary:   types.StringValue(info.DNSPrimary),
		DNSSecondary: types.StringValue(info.DNSSecondary),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// -----------------------------------------------------------------------
// Generic list data source helper
// -----------------------------------------------------------------------

// listField describes one attribute in a generic list data source.
type listField struct {
	name string
	desc string
	kind string // "string" | "int64" | "bool"
}

// listDS is a generic data source that reads a list of objects.
type listDS struct {
	client      *client.Client
	typeSuffix  string
	description string
	fields      []listField
	fetchFn     func() ([]map[string]interface{}, error)
}

func (d *listDS) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.typeSuffix
}

func (d *listDS) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *listDS) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	itemAttrs := map[string]schema.Attribute{}
	for _, f := range d.fields {
		switch f.kind {
		case "int64":
			itemAttrs[f.name] = schema.Int64Attribute{Computed: true, MarkdownDescription: f.desc}
		case "bool":
			itemAttrs[f.name] = schema.BoolAttribute{Computed: true, MarkdownDescription: f.desc}
		default:
			itemAttrs[f.name] = schema.StringAttribute{Computed: true, MarkdownDescription: f.desc}
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: d.description,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true},
			"items": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: d.description,
				NestedObject:        schema.NestedAttributeObject{Attributes: itemAttrs},
			},
		},
	}
}

func (d *listDS) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	// Build attr type map
	attrTypes := map[string]attr.Type{}
	for _, f := range d.fields {
		switch f.kind {
		case "int64":
			attrTypes[f.name] = types.Int64Type
		case "bool":
			attrTypes[f.name] = types.BoolType
		default:
			attrTypes[f.name] = types.StringType
		}
	}

	type model struct {
		ID    types.String `tfsdk:"id"`
		Items types.List   `tfsdk:"items"`
	}

	rows, err := d.fetchFn()
	if err != nil {
		resp.Diagnostics.AddWarning("Could not read "+d.typeSuffix, err.Error())
		empty, _ := types.ListValue(types.ObjectType{AttrTypes: attrTypes}, []attr.Value{})
		state := model{ID: types.StringValue(d.client.Host), Items: empty}
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	objs := make([]attr.Value, 0, len(rows))
	for _, row := range rows {
		vals := map[string]attr.Value{}
		for _, f := range d.fields {
			raw, ok := row[f.name]
			if !ok {
				raw = nil
			}
			switch f.kind {
			case "int64":
				switch v := raw.(type) {
				case int64:
					vals[f.name] = types.Int64Value(v)
				case int:
					vals[f.name] = types.Int64Value(int64(v))
				case float64:
					vals[f.name] = types.Int64Value(int64(v))
				default:
					vals[f.name] = types.Int64Value(0)
				}
			case "bool":
				switch v := raw.(type) {
				case bool:
					vals[f.name] = types.BoolValue(v)
				default:
					vals[f.name] = types.BoolValue(false)
				}
			default:
				switch v := raw.(type) {
				case string:
					vals[f.name] = types.StringValue(v)
				case []string:
					vals[f.name] = types.StringValue(strings.Join(v, ","))
				case bool:
					if v {
						vals[f.name] = types.StringValue("true")
					} else {
						vals[f.name] = types.StringValue("false")
					}
				default:
					vals[f.name] = types.StringValue(fmt.Sprintf("%v", raw))
				}
			}
		}
		obj, diags := types.ObjectValue(attrTypes, vals)
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}

	list, _ := types.ListValue(types.ObjectType{AttrTypes: attrTypes}, objs)
	state := model{ID: types.StringValue(d.client.Host), Items: list}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// -----------------------------------------------------------------------
// Volumes
// -----------------------------------------------------------------------

func NewVolumesDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "volumes",
		description: "Lists all storage volumes on the QNAP NAS.",
		fields: []listField{
			{"id", "Volume ID.", "string"},
			{"label", "Volume label.", "string"},
			{"status", "Volume status.", "string"},
			{"file_system", "Filesystem type.", "string"},
			{"total_bytes", "Total capacity in bytes.", "int64"},
			{"used_bytes", "Used space in bytes.", "int64"},
			{"free_bytes", "Free space in bytes.", "int64"},
			{"pool_id", "Parent storage pool ID.", "string"},
			{"encrypted", "Whether the volume is encrypted.", "bool"},
			{"compression", "Whether compression is enabled.", "bool"},
			{"dedup", "Whether deduplication is enabled.", "bool"},
			{"thin", "Whether thin provisioning is enabled.", "bool"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Volumes()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "label": v.Label, "status": v.Status,
				"file_system": v.FileSystem, "total_bytes": v.TotalBytes,
				"used_bytes": v.UsedBytes, "free_bytes": v.FreeBytes,
				"pool_id": v.PoolID, "encrypted": v.Encrypted,
				"compression": v.Compression, "dedup": v.Dedup, "thin": v.Thin,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Storage Pools
// -----------------------------------------------------------------------

func NewStoragePoolsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "storage_pools",
		description: "Lists all storage pools (RAID groups) on the QNAP NAS.",
		fields: []listField{
			{"id", "Pool ID.", "string"},
			{"label", "Pool label.", "string"},
			{"status", "Pool status.", "string"},
			{"raid_type", "RAID level (RAID0, RAID1, RAID5, RAID6, RAID10, JBOD).", "string"},
			{"total_bytes", "Total capacity in bytes.", "int64"},
			{"used_bytes", "Used space in bytes.", "int64"},
			{"free_bytes", "Free space in bytes.", "int64"},
			{"disk_count", "Number of data disks.", "int64"},
			{"spare_count", "Number of hot spare disks.", "int64"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.StoragePools()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "label": v.Label, "status": v.Status,
				"raid_type": v.RAIDType, "total_bytes": v.TotalBytes,
				"used_bytes": v.UsedBytes, "free_bytes": v.FreeBytes,
				"disk_count": int64(v.DiskCount), "spare_count": int64(v.SpareCount),
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Shared Folders
// -----------------------------------------------------------------------

func NewSharedFoldersDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "shared_folders",
		description: "Lists all shared folders on the QNAP NAS.",
		fields: []listField{
			{"name", "Share name.", "string"},
			{"path", "Filesystem path.", "string"},
			{"volume_id", "Parent volume ID.", "string"},
			{"comment", "Share description.", "string"},
			{"owner", "Share owner.", "string"},
			{"protocols", "Comma-separated enabled protocols (SMB,NFS,AFP,FTP).", "string"},
			{"compression", "Compression enabled.", "bool"},
			{"encryption", "Encryption enabled.", "bool"},
			{"read_only", "Read-only share.", "bool"},
			{"hidden", "Hidden share.", "bool"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.SharedFolders()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"name": v.Name, "path": v.Path, "volume_id": v.VolumeID,
				"comment": v.Comment, "owner": v.Owner,
				"protocols":   strings.Join(v.Protocols, ","),
				"compression": v.Compression, "encryption": v.Encryption,
				"read_only": v.ReadOnly, "hidden": v.Hidden,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// iSCSI Targets
// -----------------------------------------------------------------------

func NewISCSITargetsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "iscsi_targets",
		description: "Lists all iSCSI targets on the QNAP NAS.",
		fields: []listField{
			{"id", "Target ID.", "string"},
			{"name", "Target name.", "string"},
			{"iqn", "iSCSI Qualified Name.", "string"},
			{"status", "Target status.", "string"},
			{"enabled", "Whether the target is enabled.", "bool"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.ISCSITargets()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "name": v.Name, "iqn": v.IQN,
				"status": v.Status, "enabled": v.Enabled,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// iSCSI LUNs
// -----------------------------------------------------------------------

func NewISCSILunsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "iscsi_luns",
		description: "Lists all iSCSI LUNs on the QNAP NAS.",
		fields: []listField{
			{"id", "LUN ID.", "string"},
			{"name", "LUN name.", "string"},
			{"target_id", "Parent target ID.", "string"},
			{"lun_id", "LUN number within the target.", "int64"},
			{"size_bytes", "Provisioned size in bytes.", "int64"},
			{"used_bytes", "Used space in bytes.", "int64"},
			{"status", "LUN status.", "string"},
			{"lun_type", "LUN type: File or Block.", "string"},
			{"thin", "Thin provisioning enabled.", "bool"},
			{"volume_id", "Host volume ID.", "string"},
			{"file_path", "File path (for file-based LUNs).", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.ISCSILuns()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "name": v.Name, "target_id": v.TargetID,
				"lun_id": int64(v.LunID), "size_bytes": v.SizeBytes,
				"used_bytes": v.UsedBytes, "status": v.Status,
				"lun_type": v.LunType, "thin": v.ThinProv,
				"volume_id": v.VolumeID, "file_path": v.FilePath,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Network Interfaces
// -----------------------------------------------------------------------

func NewNetworkInterfacesDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "network_interfaces",
		description: "Lists all network interfaces on the QNAP NAS.",
		fields: []listField{
			{"name", "Interface name (e.g. eth0).", "string"},
			{"mac", "MAC address.", "string"},
			{"status", "Link status (up/down).", "string"},
			{"speed", "Link speed.", "string"},
			{"duplex", "Duplex mode.", "string"},
			{"ipv4", "Comma-separated IPv4 addresses.", "string"},
			{"ipv6", "Comma-separated IPv6 addresses.", "string"},
			{"gateway", "Default gateway.", "string"},
			{"mtu", "MTU value.", "int64"},
			{"bond_mode", "Bonding mode (if applicable).", "string"},
			{"vlan_tag", "VLAN tag (0 = untagged).", "int64"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.NetworkInterfaces()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"name": v.Name, "mac": v.MAC, "status": v.Status,
				"speed": v.Speed, "duplex": v.Duplex,
				"ipv4":      strings.Join(v.IPv4, ","),
				"ipv6":      strings.Join(v.IPv6, ","),
				"gateway":   v.Gateway, "mtu": int64(v.MTU),
				"bond_mode": v.BondMode, "vlan_tag": int64(v.VLANTag),
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Snapshots
// -----------------------------------------------------------------------

func NewSnapshotsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "snapshots",
		description: "Lists all volume snapshots on the QNAP NAS.",
		fields: []listField{
			{"id", "Snapshot ID.", "string"},
			{"name", "Snapshot name.", "string"},
			{"volume_id", "Parent volume ID.", "string"},
			{"created_at", "Creation timestamp.", "string"},
			{"size_bytes", "Snapshot size in bytes.", "int64"},
			{"description", "Snapshot description.", "string"},
			{"type", "Snapshot type: Manual, Schedule, or Replication.", "string"},
			{"status", "Snapshot status.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Snapshots()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "name": v.Name, "volume_id": v.VolumeID,
				"created_at": v.CreatedAt, "size_bytes": v.SizeBytes,
				"description": v.Description, "type": v.Type, "status": v.Status,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Users
// -----------------------------------------------------------------------

func NewUsersDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "users",
		description: "Lists all local user accounts on the QNAP NAS.",
		fields: []listField{
			{"name", "Username.", "string"},
			{"uid", "User ID.", "int64"},
			{"description", "User description.", "string"},
			{"email", "User email address.", "string"},
			{"enabled", "Whether the account is enabled.", "bool"},
			{"groups", "Comma-separated list of group memberships.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Users()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"name": v.Name, "uid": int64(v.UID),
				"description": v.Description, "email": v.Email,
				"enabled": v.Enabled,
				"groups":  strings.Join(v.Groups, ","),
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Groups
// -----------------------------------------------------------------------

func NewGroupsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "groups",
		description: "Lists all local groups on the QNAP NAS.",
		fields: []listField{
			{"name", "Group name.", "string"},
			{"gid", "Group ID.", "int64"},
			{"description", "Group description.", "string"},
			{"members", "Comma-separated list of member usernames.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Groups()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"name": v.Name, "gid": int64(v.GID),
				"description": v.Description,
				"members":     strings.Join(v.Members, ","),
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Apps (QPKG)
// -----------------------------------------------------------------------

func NewAppsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "apps",
		description: "Lists all installed QPKG applications on the QNAP NAS.",
		fields: []listField{
			{"name", "Package name.", "string"},
			{"display_name", "Human-readable display name.", "string"},
			{"version", "Installed version.", "string"},
			{"author", "Package author.", "string"},
			{"status", "Runtime status: Running, Stopped, Error.", "string"},
			{"enabled", "Whether the app is enabled.", "bool"},
			{"location", "Installation path.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Apps()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"name": v.Name, "display_name": v.DisplayName,
				"version": v.Version, "author": v.Author,
				"status": v.Status, "enabled": v.Enabled, "location": v.Location,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Containers (Container Station)
// -----------------------------------------------------------------------

func NewContainersDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "containers",
		description: "Lists all Container Station containers on the QNAP NAS.",
		fields: []listField{
			{"id", "Container ID.", "string"},
			{"name", "Container name.", "string"},
			{"image", "Container image.", "string"},
			{"status", "Container status: running, stopped, paused, exited.", "string"},
			{"runtime", "Container runtime: docker or podman.", "string"},
			{"project_name", "Compose project name (if part of a project).", "string"},
			{"cpu_percent", "Current CPU usage percent.", "string"},
			{"mem_usage", "Current memory usage.", "string"},
			{"created", "Creation timestamp.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Containers()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "name": v.Name, "image": v.Image,
				"status": v.Status, "runtime": v.Runtime,
				"project_name": v.ProjectName, "cpu_percent": v.CPUPercent,
				"mem_usage": v.MemUsage, "created": v.Created,
			}
		}
		return rows, nil
	}
	return d
}

// -----------------------------------------------------------------------
// Projects (Container Station compose)
// -----------------------------------------------------------------------

func NewProjectsDataSource() datasource.DataSource {
	d := &listDS{
		typeSuffix:  "projects",
		description: "Lists all Container Station compose projects on the QNAP NAS.",
		fields: []listField{
			{"id", "Project ID.", "string"},
			{"name", "Project name.", "string"},
			{"status", "Project status.", "string"},
			{"path", "Compose file path on the NAS.", "string"},
		},
	}
	d.fetchFn = func() ([]map[string]interface{}, error) {
		items, err := d.client.Projects()
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]interface{}, len(items))
		for i, v := range items {
			rows[i] = map[string]interface{}{
				"id": v.ID, "name": v.Name, "status": v.Status, "path": v.Path,
			}
		}
		return rows, nil
	}
	return d
}
