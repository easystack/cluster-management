package openstack

import (
	"context"
	"github.com/cluster-management/pkg/utils"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/containerinfra/v1/clusters"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"

	ecnsv1 "github.com/cluster-management/pkg/api/v1"
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

func (c *OSService) GetMagnumClusterStatus(ctx context.Context, clusterID string) (ecnsv1.EksSpec, error) {
	logger := utils.GetLoggerOrDie(ctx)
	provider, err := openstack.AuthenticatedClient(*c.Opts)
	var eksSpec = ecnsv1.EksSpec{}
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return eksSpec, err
	}
	client, err := openstack.NewContainerInfraV1(provider, gophercloud.EndpointOpts{
		Region: "RegionOne",
	})
	if err != nil {
		logger.Error(err, "Failed to Initialize Magnum client")
		return eksSpec, err
	}
	clusterInfo, err := clusters.Get(client, clusterID).Extract()
	if err != nil {
		logger.Error(err, "Failed to Get Magnum cluster status")
		return eksSpec, err
	}

	eksSpec.EksStatus = clusterInfo.Status
	eksSpec.EksClusterID = clusterInfo.UUID
	eksSpec.EksName = clusterInfo.Name
	eksSpec.APIAddress = clusterInfo.APIAddress
	return eksSpec, nil
}
