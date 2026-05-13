// Package resources implements managed Terraform resources for the QNAP provider.
package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steventaylor/terraform-provider-qnap/internal/client"
)

// Ensure interface compliance at compile time.
var (
	_ resource.Resource                = (*ISCSILunResource)(nil)
	_ resource.ResourceWithConfigure   = (*ISCSILunResource)(nil)
	_ resource.ResourceWithImportState = (*ISCSILunResource)(nil)
)

// NewISCSILunResource returns a new qnap_iscsi_lun resource.
func NewISCSILunResource() resource.Resource { return &ISCSILunResource{} }

// ISCSILunResource manages a single QNAP iSCSI LUN.
type ISCSILunResource struct{ client *client.Client }

// iscsiLunModel maps the Terraform state for qnap_iscsi_lun.
type iscsiLunModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	TargetID  types.String `tfsdk:"target_id"`
	LunID     types.Int64  `tfsdk:"lun_id"`
	SizeBytes types.Int64  `tfsdk:"size_bytes"`
	ThinProv  types.Bool   `tfsdk:"thin_prov"`
	FilePath  types.String `tfsdk:"file_path"`
	SerialNum types.String `tfsdk:"serial_num"`
	NAA       types.String `tfsdk:"naa"`
	Status    types.String `tfsdk:"status"`
	Enabled   types.Bool   `tfsdk:"enabled"`
}

func (r *ISCSILunResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iscsi_lun"
}

func (r *ISCSILunResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a QNAP iSCSI LUN.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "LUN index assigned by QNAP (e.g. `0`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "LUN name.",
			},
			"target_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Target index to map this LUN to.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"lun_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "LUN ID within the target.",
			},
			"size_bytes": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "LUN capacity in bytes.",
			},
			"thin_prov": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Enable thin provisioning. Defaults to `true`.",
			},
			"file_path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Storage volume path (e.g. `/share/CACHEDEV1_DATA`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"serial_num": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "LUN serial number (read-only).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"naa": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "LUN NAA identifier (read-only).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "LUN status returned by QNAP (read-only).",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the LUN is enabled. Defaults to `true`.",
			},
		},
	}
}

func (r *ISCSILunResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// Create creates the LUN and saves state.
func (r *ISCSILunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan iscsiLunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.ISCSILunCreateInput{
		Name:      plan.Name.ValueString(),
		TargetID:  plan.TargetID.ValueString(),
		SizeBytes: plan.SizeBytes.ValueInt64(),
		ThinProv:  plan.ThinProv.ValueBool(),
		VolumeID:  volumeIDFromPath(plan.FilePath.ValueString()),
	}

	lun, err := r.client.CreateISCSILun(input)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create iSCSI LUN", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, lunToModel(lun))...)
}

// Read refreshes state from the QNAP API.
func (r *ISCSILunResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state iscsiLunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lun, err := r.findLunByID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read iSCSI LUN", err.Error())
		return
	}
	if lun == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, lunToModel(lun))...)
}

// Update applies mutable changes (name, size, enabled).
func (r *ISCSILunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan iscsiLunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.ISCSILunUpdateInput{
		ID:        plan.ID.ValueString(),
		Name:      plan.Name.ValueString(),
		SizeBytes: plan.SizeBytes.ValueInt64(),
		Enabled:   plan.Enabled.ValueBool(),
	}

	if err := r.client.UpdateISCSILun(input); err != nil {
		resp.Diagnostics.AddError("Failed to update iSCSI LUN", err.Error())
		return
	}

	// Re-read to get authoritative state
	lun, err := r.findLunByID(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read iSCSI LUN after update", err.Error())
		return
	}
	if lun == nil {
		resp.Diagnostics.AddError("iSCSI LUN not found after update", fmt.Sprintf("LUN ID %s disappeared", plan.ID.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, lunToModel(lun))...)
}

// Delete removes the LUN.
func (r *ISCSILunResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state iscsiLunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteISCSILun(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete iSCSI LUN", err.Error())
	}
}

// ImportState imports an existing LUN by its index ID.
func (r *ISCSILunResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func (r *ISCSILunResource) findLunByID(id string) (*client.ISCSILun, error) {
	luns, err := r.client.ISCSILuns()
	if err != nil {
		return nil, err
	}
	for i := range luns {
		if luns[i].ID == id {
			return &luns[i], nil
		}
	}
	return nil, nil // not found — caller removes from state
}

func lunToModel(l *client.ISCSILun) iscsiLunModel {
	return iscsiLunModel{
		ID:        types.StringValue(l.ID),
		Name:      types.StringValue(l.Name),
		TargetID:  types.StringValue(l.TargetID),
		LunID:     types.Int64Value(int64(l.LunID)),
		SizeBytes: types.Int64Value(l.SizeBytes),
		ThinProv:  types.BoolValue(l.ThinProv),
		FilePath:  types.StringValue(l.FilePath),
		SerialNum: types.StringValue(l.SerialNum),
		NAA:       types.StringValue(l.NAA),
		Status:    types.StringValue(l.Status),
		Enabled:   types.BoolValue(l.Enabled),
	}
}

// volumeIDFromPath strips the /share/ prefix to get the bare volume ID
// that the create API expects (e.g. "/share/CACHEDEV1_DATA" → "CACHEDEV1_DATA").
func volumeIDFromPath(p string) string {
	const prefix = "/share/"
	if len(p) > len(prefix) && p[:len(prefix)] == prefix {
		return p[len(prefix):]
	}
	return p
}
