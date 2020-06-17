package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/elasticache"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
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

	stsSvc := sts.New(session.New())
	identity, err := stsSvc.GetCallerIdentity(&sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, err
	}
	metadataSvc := ec2metadata.New(session.New())
	region, err := metadataSvc.Region()
	if err != nil {
		level.Warn(logger).Log("msg", "could not get region", "err", err)
		region = "us-east-1"
	}

	d := &discovery{
		logger:          logger,
		refreshInterval: conf.RefreshInterval,
		accountID:       *identity.Account,
		region:          region,
	}

	return d, nil
}

func (d *discovery) Run(ctx context.Context, ch chan<- []*targetgroup.Group) {
	for c := time.Tick(time.Duration(d.refreshInterval) * time.Second); ; {
		var tgs []*targetgroup.Group

		sess := session.Must(session.NewSession())
		client := elasticache.New(sess, &aws.Config{Region: aws.String(d.region)})

		input := &elasticache.DescribeCacheClustersInput{
			ShowCacheNodeInfo: aws.Bool(true),
		}

		if err := client.DescribeCacheClustersPagesWithContext(ctx, input, func(out *elasticache.DescribeCacheClustersOutput, lastPage bool) bool {
			for _, cluster := range out.CacheClusters {
				for _, node := range cluster.CacheNodes {
					labels := model.LabelSet{
						elasticacheLabelClusterID: model.LabelValue(*cluster.CacheClusterId),
						elasticacheLabelNodeID:    model.LabelValue(*node.CacheNodeId),
					}

					labels[elasticacheLabelAZ] = model.LabelValue(*node.CustomerAvailabilityZone)
					labels[elasticacheLabelInstanceState] = model.LabelValue(*node.CacheNodeStatus)
					labels[elasticacheLabelInstanceType] = model.LabelValue(*cluster.CacheNodeType)

					addr := net.JoinHostPort(*node.Endpoint.Address, strconv.FormatInt(*node.Endpoint.Port, 10))
					labels[model.AddressLabel] = model.LabelValue(addr)

					labels[elasticacheLabelEngine] = model.LabelValue(*cluster.Engine)
					labels[elasticacheLabelEngineVersion] = model.LabelValue(*cluster.EngineVersion)

					labels[elasticacheLabelEndpointAddress] = model.LabelValue(*node.Endpoint.Address)
					labels[elasticacheLabelEndpointPort] = model.LabelValue(strconv.FormatInt(*node.Endpoint.Port, 10))

					tags, err := listTagsForInstance(client, d.accountID, d.region, cluster, node)
					if err != nil {
						level.Error(d.logger).Log("msg", "could not list tags for elasticache instance", "err", err)
					}

					for _, t := range tags.TagList {
						if t == nil || t.Key == nil || t.Value == nil {
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
			return !lastPage
		}); err != nil {
			level.Error(d.logger).Log("msg", "could not describe elasticache instance", "err", err)
			time.Sleep(time.Duration(d.refreshInterval) * time.Second)
			continue
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

func listTagsForInstance(client *elasticache.ElastiCache, accountId string, region string, cluster *elasticache.CacheCluster, node *elasticache.CacheNode) (*elasticache.TagListMessage, error) {
	input := &elasticache.ListTagsForResourceInput{
		ResourceName: aws.String(fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s", region, accountId, *cluster.CacheClusterId)),
	}
	return client.ListTagsForResource(input)
}
