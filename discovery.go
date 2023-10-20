package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	"github.com/prometheus/prometheus/util/strutil"
)

const (
	elasticacheLabel                = model.MetaLabelPrefix + "elasticache_"
	elasticacheLabelAZ              = elasticacheLabel + "availability_zone"
	elasticacheLabelClusterID       = elasticacheLabel + "cluster_id"
	elasticacheLabelNodeID          = elasticacheLabel + "node_id"
	elasticacheLabelInstanceState   = elasticacheLabel + "instance_state"
	elasticacheLabelInstanceType    = elasticacheLabel + "instance_type"
	elasticacheLabelEngine          = elasticacheLabel + "engine"
	elasticacheLabelEngineVersion   = elasticacheLabel + "engine_version"
	elasticacheLabelTag             = elasticacheLabel + "tag_"
	elasticacheLabelEndpointAddress = elasticacheLabel + "endpoint_address"
	elasticacheLabelEndpointPort    = elasticacheLabel + "endpoint_port"
)

type discovery struct {
	refreshInterval int
	logger          log.Logger
	accountID       string
	region          string
}

func newDiscovery(conf sdConfig, logger log.Logger) (*discovery, error) {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	ctx := context.TODO()
	accountId, err := getAccountId(ctx)
	if err != nil {
		return nil, err
	}

	var region string
	for region == "" {
		var err error
		region, err = getRegion(ctx)
		if err != nil {
			level.Error(logger).Log("msg", "could not get region", "err", err)
			time.Sleep(time.Duration(5) * time.Second)
			continue
		}
	}

	d := &discovery{
		logger:          logger,
		refreshInterval: conf.RefreshInterval,
		accountID:       accountId,
		region:          region,
	}

	return d, nil
}

func getAccountId(ctx context.Context) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(0))
	if err != nil {
		return "", err
	}

	client := sts.NewFromConfig(cfg)
	response, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}

	return *response.Account, nil
}

func getRegion(ctx context.Context) (string, error) {
	var region string

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(0))
	if err != nil {
		return "", err
	}

	client := imds.NewFromConfig(cfg)
	response, err := client.GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
	} else {
		region = response.Region
	}

	return region, nil
}

func (d *discovery) Run(ctx context.Context, ch chan<- []*targetgroup.Group) {
	for c := time.Tick(time.Duration(d.refreshInterval) * time.Second); ; {
		var tgs []*targetgroup.Group

		sdkConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(d.region))
		if err != nil {
			level.Error(d.logger).Log("msg", "could not load config", "err", err)
			time.Sleep(time.Duration(d.refreshInterval) * time.Second)
			continue
		}
		client := elasticache.NewFromConfig(sdkConfig)

		input := &elasticache.DescribeCacheClustersInput{
			ShowCacheNodeInfo: aws.Bool(true),
		}

		paginator := elasticache.NewDescribeCacheClustersPaginator(client, input)
		for paginator.HasMorePages() {
			out, err := paginator.NextPage(ctx)
			if err != nil {
				level.Error(d.logger).Log("msg", "could not describe cache cluster", "err", err)
				time.Sleep(time.Duration(d.refreshInterval) * time.Second)
				continue
			}
			for _, cluster := range out.CacheClusters {
				for _, node := range cluster.CacheNodes {
					if node.Endpoint.Address == nil {
						continue // instance is not ready
					}

					labels := model.LabelSet{
						elasticacheLabelClusterID: model.LabelValue(*cluster.CacheClusterId),
						elasticacheLabelNodeID:    model.LabelValue(*node.CacheNodeId),
					}

					labels[elasticacheLabelAZ] = model.LabelValue(*node.CustomerAvailabilityZone)
					labels[elasticacheLabelInstanceState] = model.LabelValue(*node.CacheNodeStatus)
					labels[elasticacheLabelInstanceType] = model.LabelValue(*cluster.CacheNodeType)

					addr := net.JoinHostPort(*node.Endpoint.Address, strconv.FormatInt(int64(node.Endpoint.Port), 10))
					labels[model.AddressLabel] = model.LabelValue(addr)

					labels[elasticacheLabelEngine] = model.LabelValue(*cluster.Engine)
					labels[elasticacheLabelEngineVersion] = model.LabelValue(*cluster.EngineVersion)

					labels[elasticacheLabelEndpointAddress] = model.LabelValue(*node.Endpoint.Address)
					labels[elasticacheLabelEndpointPort] = model.LabelValue(strconv.FormatInt(int64(node.Endpoint.Port), 10))

					tags, err := listTagsForInstance(ctx, client, d.accountID, d.region, cluster, node)
					if err != nil {
						level.Error(d.logger).Log("msg", "could not list tags for elasticache instance", "err", err)
						continue
					}

					for _, t := range tags.TagList {
						if t.Key == nil || t.Value == nil {
							continue
						}

						name := strutil.SanitizeLabelName(*t.Key)
						labels[elasticacheLabelTag+model.LabelName(name)] = model.LabelValue(*t.Value)
					}

					tgs = append(tgs, &targetgroup.Group{
						Source:  *cluster.CacheClusterId + *node.CacheNodeId,
						Targets: []model.LabelSet{{model.AddressLabel: labels[model.AddressLabel]}},
						Labels:  labels,
					})
				}
			}
		}

		ch <- tgs

		select {
		case <-c:
			continue
		case <-ctx.Done():
			return
		}
	}
}

func listTagsForInstance(ctx context.Context, client *elasticache.Client, accountId string, region string, cluster types.CacheCluster, node types.CacheNode) (*elasticache.ListTagsForResourceOutput, error) {
	input := &elasticache.ListTagsForResourceInput{
		ResourceName: aws.String(fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s", region, accountId, *cluster.CacheClusterId)),
	}
	return client.ListTagsForResource(ctx, input)
}
