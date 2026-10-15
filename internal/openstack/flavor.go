/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"context"
	"fmt"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
)

// Flavor is the resource spec of an OpenStack flavor.
type Flavor struct {
	Name string
	// CPU is the number of vCPUs of the flavor.
	CPU int64
	// MemoryMiB is the memory size of the flavor in MiB (Nova reports it in MB,
	// which is effectively MiB for these flavors).
	MemoryMiB int64
}

// FlavorLister lists the flavor catalog of the OpenStack cloud keyed by flavor name.
type FlavorLister interface {
	ListFlavors(ctx context.Context) (map[string]Flavor, error)
}

// ListFlavors lists all nova flavors with details.
func ListFlavors(ctx context.Context, client *gophercloud.ServiceClient) ([]flavors.Flavor, error) {
	return listFlavors(ctx, client)
}

// listFlavors ref: https://docs.openstack.org/api-ref/compute/#list-flavors-with-details
func listFlavors(ctx context.Context, client *gophercloud.ServiceClient) ([]flavors.Flavor, error) {
	allPages, err := flavors.ListDetail(client, nil).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list flavors: %w", err)
	}

	allFlavors, err := flavors.ExtractFlavors(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract flavors: %w", err)
	}

	return allFlavors, nil
}

// ListFlavors returns the flavor catalog of the OpenStack cloud keyed by flavor
// name. It implements FlavorLister.
func (cm *ClientManager) ListFlavors(ctx context.Context) (map[string]Flavor, error) {
	client, err := cm.ComputeClient()
	if err != nil {
		return nil, err
	}

	allFlavors, err := listFlavors(ctx, client)
	if err != nil {
		return nil, err
	}

	catalog := make(map[string]Flavor, len(allFlavors))
	for _, flavor := range allFlavors {
		catalog[flavor.Name] = Flavor{Name: flavor.Name, CPU: int64(flavor.VCPUs), MemoryMiB: int64(flavor.RAM)}
	}
	return catalog, nil
}
