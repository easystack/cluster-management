package openstack

import (
	"context"
	"github.com/cluster-management/pkg/utils"

	ecnsv1 "github.com/cluster-management/pkg/api/v1"
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
	eksSpec.EksStackID = clusterInfo.StackID
	return eksSpec, nil
}

func (c *OSService) GenerateOpenstackClient(ctx context.Context, clientType string) (*gophercloud.ServiceClient, error) {
	logger := utils.GetLoggerOrDie(ctx)
	provider, err := openstack.AuthenticatedClient(*c.Opts)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return nil, err
	}

	var client *gophercloud.ServiceClient
	switch clientType {
	case "MagnumV1":
		client, err = openstack.NewContainerInfraV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			logger.Error(err, "Failed to Initialize MagnumV1 client")
			return nil, err
		}
	case "HeatV1":
		client, err = openstack.NewOrchestrationV1(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			logger.Error(err, "Failed to Initialize HeatV1 client")
			return nil, err
		}
	case "CinderV2":
		client, err = openstack.NewBlockStorageV2(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			logger.Error(err, "Failed to Initialize ClientV2 client")
			return nil, err
		}
	case "LoadBalancerV2":
		client, err = openstack.NewNetworkV2(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			logger.Error(err, "Failed to Initialize LoadBalancerV2 client")
			return nil, err
		}
	default:
		logger.Info("No client can be matched !")
		return nil, nil
	}
	return client, nil
}
