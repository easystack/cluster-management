package openstack

import (
	"context"
	"github.com/cluster-management/utils"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
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
