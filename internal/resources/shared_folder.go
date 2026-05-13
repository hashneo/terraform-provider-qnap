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
	_ resource.Resource                = (*SharedFolderResource)(nil)
	_ resource.ResourceWithConfigure   = (*SharedFolderResource)(nil)
	_ resource.ResourceWithImportState = (*SharedFolderResource)(nil)
)

// NewSharedFolderResource returns a new qnap_shared_folder resource.
func NewSharedFolderResource() resource.Resource { return &SharedFolderResource{} }

// SharedFolderResource manages a single QNAP shared folder.
type SharedFolderResource struct{ client *client.Client }

// sharedFolderModel maps the Terraform state for qnap_shared_folder.
type sharedFolderModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Path        types.String `tfsdk:"path"`
	VolumeID    types.String `tfsdk:"volume_id"`
	Comment     types.String `tfsdk:"comment"`
	Compression types.Bool   `tfsdk:"compression"`
	Encryption  types.Bool   `tfsdk:"encryption"`
	ReadOnly    types.Bool   `tfsdk:"readonly"`
	Hidden      types.Bool   `tfsdk:"hidden"`
}

func (r *SharedFolderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_shared_folder"
}

func (r *SharedFolderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a QNAP shared folder (SMB/NFS/AFP share).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Shared folder name used as the unique identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Shared folder name (must be unique on the NAS).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Absolute path on the NAS (e.g. `/Public`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"volume_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Storage volume / cache device ID (e.g. `CACHEDEV1_DATA`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"comment": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Description for the shared folder.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"compression": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Enable data compression. Defaults to `false`.",
			},
			"encryption": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the folder is encrypted (read-only, set at creation by NAS).",
			},
			"readonly": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Make the share read-only. Defaults to `false`.",
			},
			"hidden": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Hide the share from network browse lists. Defaults to `false`.",
			},
		},
	}
}

func (r *SharedFolderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create creates the shared folder and saves state.
func (r *SharedFolderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sharedFolderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.SharedFolderCreateInput{
		Name:        plan.Name.ValueString(),
		VolumeID:    plan.VolumeID.ValueString(),
		Comment:     plan.Comment.ValueString(),
		Compression: plan.Compression.ValueBool(),
		Hidden:      plan.Hidden.ValueBool(),
		ReadOnly:    plan.ReadOnly.ValueBool(),
	}

	if err := r.client.CreateSharedFolder(input); err != nil {
		resp.Diagnostics.AddError("Failed to create shared folder", err.Error())
		return
	}

	// Read back to populate computed fields
	folder, err := r.findFolderByName(plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read shared folder after create", err.Error())
		return
	}
	if folder == nil {
		resp.Diagnostics.AddError("Shared folder not found after create", fmt.Sprintf("folder %q not found", plan.Name.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, folderToModel(folder))...)
}

// Read refreshes state from the QNAP API.
func (r *SharedFolderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sharedFolderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, err := r.findFolderByName(state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read shared folder", err.Error())
		return
	}
	if folder == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, folderToModel(folder))...)
}

// Update applies mutable changes (comment, compression, hidden, readonly).
func (r *SharedFolderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sharedFolderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.SharedFolderUpdateInput{
		Name:        plan.Name.ValueString(),
		Comment:     plan.Comment.ValueString(),
		Compression: plan.Compression.ValueBool(),
		Hidden:      plan.Hidden.ValueBool(),
		ReadOnly:    plan.ReadOnly.ValueBool(),
	}

	if err := r.client.UpdateSharedFolder(input); err != nil {
		resp.Diagnostics.AddError("Failed to update shared folder", err.Error())
		return
	}

	folder, err := r.findFolderByName(plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read shared folder after update", err.Error())
		return
	}
	if folder == nil {
		resp.Diagnostics.AddError("Shared folder not found after update", fmt.Sprintf("folder %q disappeared", plan.Name.ValueString()))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, folderToModel(folder))...)
}

// Delete removes the shared folder.
func (r *SharedFolderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sharedFolderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteSharedFolder(state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete shared folder", err.Error())
	}
}

// ImportState imports an existing shared folder by its name.
func (r *SharedFolderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func (r *SharedFolderResource) findFolderByName(name string) (*client.SharedFolder, error) {
	folders, err := r.client.SharedFolders()
	if err != nil {
		return nil, err
	}
	for i := range folders {
		if folders[i].Name == name {
			return &folders[i], nil
		}
	}
	return nil, nil // not found — caller removes from state or errors
}

func folderToModel(f *client.SharedFolder) sharedFolderModel {
	return sharedFolderModel{
		ID:          types.StringValue(f.Name),
		Name:        types.StringValue(f.Name),
		Path:        types.StringValue(f.Path),
		VolumeID:    types.StringValue(f.VolumeID),
		Comment:     types.StringValue(f.Comment),
		Compression: types.BoolValue(f.Compression),
		Encryption:  types.BoolValue(f.Encryption),
		ReadOnly:    types.BoolValue(f.ReadOnly),
		Hidden:      types.BoolValue(f.Hidden),
	}
}
