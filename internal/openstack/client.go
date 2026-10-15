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
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
)

const xOpenStackNovaAPIVersion = "2.59"

// ClientManager holds the OpenStack credentials built from the OS_* environment
// variables and lazily creates and caches the authenticated provider client.
type ClientManager struct {
	ctx      context.Context
	option   gophercloud.AuthOptions
	endpoint gophercloud.EndpointOpts
	mu       sync.Mutex
	provider *gophercloud.ProviderClient
}

// NewClientManager builds a ClientManager from the OS_* environment variables
// (same set as the launcher cluster-manager deployment: OS_AUTH_URL,
// OS_USERNAME, OS_PASSWORD, OS_PROJECT_NAME, OS_USER_DOMAIN_NAME,
// OS_PROJECT_DOMAIN_NAME, OS_REGION_NAME, OS_ENDPOINT_TYPE, ...).
func NewClientManager(ctx context.Context) (*ClientManager, error) {
	option, endpoint, err := authOptionsFromEnv()
	if err != nil {
		return nil, err
	}
	return &ClientManager{ctx: ctx, option: option, endpoint: endpoint}, nil
}

// authOptionsFromEnv builds the gophercloud auth options and endpoint options
// from the OS_* environment variables.
func authOptionsFromEnv() (gophercloud.AuthOptions, gophercloud.EndpointOpts, error) {
	authURL := os.Getenv("OS_AUTH_URL")
	username := os.Getenv("OS_USERNAME")
	userID := os.Getenv("OS_USERID")
	password := os.Getenv("OS_PASSWORD")
	if authURL == "" {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, fmt.Errorf("environment variable OS_AUTH_URL is not set")
	}
	if username == "" && userID == "" {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, fmt.Errorf("environment variables OS_USERNAME or OS_USERID must be set")
	}
	if password == "" {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, fmt.Errorf("environment variable OS_PASSWORD is not set")
	}

	option := gophercloud.AuthOptions{
		IdentityEndpoint: authURL,
		Username:         username,
		UserID:           userID,
		Password:         password,
		DomainName:       envFallback("OS_USER_DOMAIN_NAME", "OS_DOMAIN_NAME"),
		AllowReauth:      true,
	}

	projectName := os.Getenv("OS_PROJECT_NAME")
	projectID := os.Getenv("OS_PROJECT_ID")
	if projectName != "" || projectID != "" {
		option.Scope = &gophercloud.AuthScope{
			ProjectName: projectName,
			ProjectID:   projectID,
			DomainName:  envFallback("OS_PROJECT_DOMAIN_NAME", "OS_DOMAIN_NAME"),
		}
	}

	availability, err := endpointAvailability(os.Getenv("OS_ENDPOINT_TYPE"))
	if err != nil {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, err
	}
	endpoint := gophercloud.EndpointOpts{
		Region:       os.Getenv("OS_REGION_NAME"),
		Availability: availability,
	}
	return option, endpoint, nil
}

func envFallback(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

// endpointAvailability maps the OS_ENDPOINT_TYPE value (openstackclient style,
// e.g. internalURL) to a gophercloud endpoint availability.
func endpointAvailability(name string) (gophercloud.Availability, error) {
	switch strings.TrimSuffix(name, "URL") {
	case "", string(gophercloud.AvailabilityPublic):
		return gophercloud.AvailabilityPublic, nil
	case string(gophercloud.AvailabilityInternal):
		return gophercloud.AvailabilityInternal, nil
	case string(gophercloud.AvailabilityAdmin):
		return gophercloud.AvailabilityAdmin, nil
	default:
		return "", fmt.Errorf("unsupported OS_ENDPOINT_TYPE %q", name)
	}
}

// ProviderClient returns the authenticated provider client, creating and
// authenticating it on first use.
func (cm *ClientManager) ProviderClient() (*gophercloud.ProviderClient, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.provider != nil {
		return cm.provider, nil
	}
	provider, err := openstack.NewClient(cm.option.IdentityEndpoint)
	if err != nil {
		return nil, fmt.Errorf("create openstack client for %s: %w", cm.option.IdentityEndpoint, err)
	}
	provider.HTTPClient.Transport = http.DefaultTransport
	if err := openstack.Authenticate(cm.ctx, provider, cm.option); err != nil {
		return nil, fmt.Errorf("authenticate against openstack %s: %w", cm.option.IdentityEndpoint, err)
	}
	cm.provider = provider
	return provider, nil
}

// ComputeClient returns a nova service client.
func (cm *ClientManager) ComputeClient() (*gophercloud.ServiceClient, error) {
	provider, err := cm.ProviderClient()
	if err != nil {
		return nil, err
	}
	client, err := openstack.NewComputeV2(provider, cm.endpoint)
	if err != nil {
		return nil, fmt.Errorf("get nova endpoint: %w", err)
	}
	client.Microversion = xOpenStackNovaAPIVersion
	return client, nil
}
