package vlans

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	equinix_errors "github.com/equinix/terraform-provider-equinix/internal/errors"
	"github.com/equinix/terraform-provider-equinix/internal/framework"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/packethost/packngo"
)

type Resource struct {
	framework.BaseResource
	framework.WithTimeouts
}

func NewResource() resource.Resource {
	r := Resource{
		BaseResource: framework.NewBaseResource(
			framework.BaseResourceConfig{
				Name: "equinix_metal_vlan",
			},
		),
	}

	return &r
}

func (r *Resource) Schema(
	ctx context.Context,
	req resource.SchemaRequest,
	resp *resource.SchemaResponse,
) {
	s := resourceSchema(ctx)
	if s.Blocks == nil {
		s.Blocks = make(map[string]schema.Block)
	}

	resp.Schema = s
}

func (r *Resource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	r.Meta.AddFwModuleToMetalUserAgent(ctx, request.ProviderMeta)
	client := r.Meta.Metal

	var data DataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	if data.Facility.IsNull() && data.Metro.IsNull() {
		response.Diagnostics.AddError("Invalid inout params",
			equinix_errors.FriendlyError(errors.New("one of facility or metro must be configured")).Error())
		return
	}
	if !(data.Facility.IsNull() && data.Vxlan.IsNull()) {
		response.Diagnostics.AddError("Invalid inout params",
			equinix_errors.FriendlyError(errors.New("you can set vxlan only for metro vlans")).Error())
		return
	}

	createRequest := &packngo.VirtualNetworkCreateRequest{
		ProjectID:   data.ProjectID.ValueString(),
		Description: data.Description.ValueString(),
	}
	if !data.Metro.IsNull() {
		createRequest.Metro = data.Metro.ValueString()
		createRequest.VXLAN = int(data.Vxlan.ValueInt64())
	}
	if !data.Facility.IsNull() {
		createRequest.Facility = data.Facility.ValueString()
	}
	vlan, _, err := client.ProjectVirtualNetworks.Create(createRequest)
	if err != nil {
		response.Diagnostics.AddError("Error creating Vlan", equinix_errors.FriendlyError(err).Error())
		return
	}

	var vlanModel ResourceModel
	// Parse API response into the Terraform state
	response.Diagnostics.Append(vlanModel.parse(vlan)...)
	if response.Diagnostics.HasError() {
		return
	}

	// Set state to fully populated data
	response.Diagnostics.Append(response.State.Set(ctx, &vlanModel)...)
	return
}

func (r *Resource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	r.Meta.AddFwModuleToMetalUserAgent(ctx, request.ProviderMeta)
	client := r.Meta.Metal

	var data ResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	vlan, _, err := client.ProjectVirtualNetworks.Get(
		data.ID.ValueString(),
		&packngo.GetOptions{Includes: []string{"assigned_to"}},
	)
	if err != nil {
		if equinix_errors.IsNotFound(err) {
			response.Diagnostics.AddWarning(
				"Equinix Metal Vlan not found during refresh",
				fmt.Sprintf("[WARN] Vlan (%s) not found, removing from state", data.ID.ValueString()),
			)
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError("Error fetching Vlan using vlanId",
			equinix_errors.FriendlyError(err).Error())
		return
	}

	response.Diagnostics.Append(data.parse(vlan)...)
	if response.Diagnostics.HasError() {
		return
	}

	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

func (r *Resource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	//TODO implement me
	panic("implement me")
}

func (r *Resource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	r.Meta.AddFwModuleToMetalUserAgent(ctx, request.ProviderMeta)
	client := r.Meta.Metal

	var data ResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	vlan, resp, err := client.ProjectVirtualNetworks.Get(
		data.ID.ValueString(),
		&packngo.GetOptions{Includes: []string{"instances", "meta_gateway"}},
	)
	if err != nil {
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			response.Diagnostics.AddWarning(
				"Equinix Metal Vlan not found during delete",
				err.Error(),
			)
			return
		}
		response.Diagnostics.AddError("Error fetching Vlan using vlanId",
			equinix_errors.FriendlyError(err).Error())
		return
	}

	// all device ports must be unassigned before delete
	for _, instance := range vlan.Instances {
		for _, port := range instance.NetworkPorts {
			for _, v := range port.AttachedVirtualNetworks {
				if v.ID == vlan.ID {
					_, resp, err = client.Ports.Unassign(port.ID, vlan.ID)
					if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
						response.Diagnostics.AddError("Error unassign port with Vlan",
							equinix_errors.FriendlyError(err).Error())
						return
					}
				}
			}
		}
	}

	resp, err = client.ProjectVirtualNetworks.Delete(vlan.ID)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		response.Diagnostics.AddError("Error deleting Vlan",
			equinix_errors.FriendlyError(err).Error())
		return
	}
}
