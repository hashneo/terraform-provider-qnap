// Package provider implements the Terraform provider for QNAP QTS 5.
package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/steventaylor/terraform-provider-qnap/internal/client"
	"github.com/steventaylor/terraform-provider-qnap/internal/datasources"
	"github.com/steventaylor/terraform-provider-qnap/internal/resources"
)

var _ provider.Provider = (*QNAPProvider)(nil)

type QNAPProvider struct{ version string }

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &QNAPProvider{version: version}
	}
}

type providerModel struct {
	Host        types.String `tfsdk:"host"`
	Username    types.String `tfsdk:"username"`
	Password    types.String `tfsdk:"password"`
	SSLInsecure types.Bool   `tfsdk:"ssl_insecure"`
}

func (p *QNAPProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "qnap"
	resp.Version = p.version
}

func (p *QNAPProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
The **qnap** provider manages QNAP NAS devices running QTS 5.x via the QTS REST API.

## Example Usage

~~~hcl
provider "qnap" {
  host        = "192.168.1.50"
  username    = "admin"
  password    = "yourpassword"
  ssl_insecure = true
}
~~~
`,
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Hostname or IP address of the QNAP NAS.",
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "QNAP admin username.",
			},
			"password": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				MarkdownDescription: "QNAP admin password.",
			},
			"ssl_insecure": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip TLS certificate verification. Set `true` for self-signed NAS certificates.",
			},
		},
	}
}

func (p *QNAPProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sslInsecure := true
	if !config.SSLInsecure.IsNull() && !config.SSLInsecure.IsUnknown() {
		sslInsecure = config.SSLInsecure.ValueBool()
	}

	c, err := client.New(
		config.Host.ValueString(),
		config.Username.ValueString(),
		config.Password.ValueString(),
		sslInsecure,
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to authenticate with QNAP", fmt.Sprintf("%v", err))
		return
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *QNAPProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewSharedFolderResource,
		resources.NewISCSITargetResource,
		resources.NewISCSILunResource,
	}
}

func (p *QNAPProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewSystemInfoDataSource,
		datasources.NewVolumesDataSource,
		datasources.NewStoragePoolsDataSource,
		datasources.NewSharedFoldersDataSource,
		datasources.NewISCSITargetsDataSource,
		datasources.NewISCSILunsDataSource,
		datasources.NewNetworkInterfacesDataSource,
		datasources.NewSnapshotsDataSource,
		datasources.NewUsersDataSource,
		datasources.NewGroupsDataSource,
		datasources.NewAppsDataSource,
		datasources.NewContainersDataSource,
		datasources.NewProjectsDataSource,
	}
}
