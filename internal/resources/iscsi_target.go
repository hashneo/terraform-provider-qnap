package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = (*ISCSITargetResource)(nil)
	_ resource.ResourceWithConfigure   = (*ISCSITargetResource)(nil)
	_ resource.ResourceWithImportState = (*ISCSITargetResource)(nil)
)

// NewISCSITargetResource returns a new qnap_iscsi_target resource.
func NewISCSITargetResource() resource.Resource { return &ISCSITargetResource{} }

// ISCSITargetResource manages a single QNAP iSCSI target.
type ISCSITargetResource struct{ client *client.Client }

// iscsiTargetModel maps the Terraform state for qnap_iscsi_target.
type iscsiTargetModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Alias      types.String `tfsdk:"alias"`
	IQN        types.String `tfsdk:"iqn"`
	Status     types.String `tfsdk:"status"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	Initiators types.List   `tfsdk:"initiators"`
}

func (r *ISCSITargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_iscsi_target"
}

func (r *ISCSITargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a QNAP iSCSI target.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Target index assigned by QNAP (e.g. `0`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Target name.",
			},
			"alias": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Target alias.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"iqn": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "iSCSI Qualified Name. If omitted QNAP generates one.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Target status returned by QNAP (read-only).",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the target is enabled. Defaults to `true`.",
			},
			"initiators": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "Connected initiator IQNs (read-only).",
			},
		},
	}
}

func (r *ISCSITargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create creates the target and saves state.
func (r *ISCSITargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan iscsiTargetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.ISCSITargetCreateInput{
		Name:    plan.Name.ValueString(),
		Alias:   plan.Alias.ValueString(),
		IQN:     plan.IQN.ValueString(),
		Enabled: plan.Enabled.ValueBool(),
	}

	target, err := r.client.CreateISCSITarget(input)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create iSCSI target", err.Error())
		return
	}

	model, diags := targetToModel(ctx, target)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

// Read refreshes state from the QNAP API.
func (r *ISCSITargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state iscsiTargetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	target, err := r.findTargetByID(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read iSCSI target", err.Error())
		return
	}
	if target == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	model, diags := targetToModel(ctx, target)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

// Update applies mutable changes (name, alias, enabled).
func (r *ISCSITargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan iscsiTargetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.ISCSITargetUpdateInput{
		ID:      plan.ID.ValueString(),
		Name:    plan.Name.ValueString(),
		Alias:   plan.Alias.ValueString(),
		Enabled: plan.Enabled.ValueBool(),
	}

	if err := r.client.UpdateISCSITarget(input); err != nil {
		resp.Diagnostics.AddError("Failed to update iSCSI target", err.Error())
		return
	}

	target, err := r.findTargetByID(plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read iSCSI target after update", err.Error())
		return
	}
	if target == nil {
		resp.Diagnostics.AddError("iSCSI target not found after update", fmt.Sprintf("target ID %s disappeared", plan.ID.ValueString()))
		return
	}

	model, diags := targetToModel(ctx, target)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

// Delete removes the target.
func (r *ISCSITargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state iscsiTargetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteISCSITarget(state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Failed to delete iSCSI target", err.Error())
	}
}

// ImportState imports an existing target by its index ID.
func (r *ISCSITargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func (r *ISCSITargetResource) findTargetByID(id string) (*client.ISCSITarget, error) {
	targets, err := r.client.ISCSITargets()
	if err != nil {
		return nil, err
	}
	for i := range targets {
		if targets[i].ID == id {
			return &targets[i], nil
		}
	}
	return nil, nil // not found — caller removes from state
}

func targetToModel(ctx context.Context, t *client.ISCSITarget) (iscsiTargetModel, diag.Diagnostics) {
	initiators, diags := types.ListValueFrom(ctx, types.StringType, t.Initiators)
	return iscsiTargetModel{
		ID:         types.StringValue(t.ID),
		Name:       types.StringValue(t.Name),
		Alias:      types.StringValue(t.Alias),
		IQN:        types.StringValue(t.IQN),
		Status:     types.StringValue(t.Status),
		Enabled:    types.BoolValue(t.Enabled),
		Initiators: initiators,
	}, diags
}
