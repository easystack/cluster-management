package openstack

import (
	"context"
	"github.com/cluster-management/pkg/utils"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/containerinfra/v1/clusters"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
)

type OSService struct {
	Opts *gophercloud.AuthOptions
}

func (c *OSService) GetKeystoneToken(ctx context.Context) (*tokens.Token, error) {
	logger := utils.GetLoggerOrDie(ctx)
	provider, err := openstack.AuthenticatedClient(*c.Opts)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return nil, err
	}
	client, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
	if err != nil {
		logger.Error(err, "Failed to Initialize Keystone client")
		return nil, err
	}
	token, err := tokens.Create(client, c.Opts).ExtractToken()
	if err != nil {
		logger.Error(err, "Failed to Get token")
		return nil, err
	}

	return token, nil
}

func (c *OSService) GetMagnumClusterStatus(ctx context.Context, clusterID string) (string, string, error) {
	logger := utils.GetLoggerOrDie(ctx)
	provider, err := openstack.AuthenticatedClient(*c.Opts)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return "", "", err
	}
	client, err := openstack.NewContainerInfraV1(provider, gophercloud.EndpointOpts{
		Region: "RegionOne",
	})
	if err != nil {
		logger.Error(err, "Failed to Initialize Magnum client")
		return "", "", err
	}
	clusterInfo, err := clusters.Get(client, clusterID).Extract()
	if err != nil {
		logger.Error(err, "Failed to Get Magnum cluster status")
		return "", "", err
	}

	return clusterInfo.Status, clusterInfo.APIAddress, nil
}
