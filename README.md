# prometheus-elasticache-sd

Generate [`file_sd`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#file_sd_config) file of Prometheus for Amazon ElastiCache.

## Usage

```
./prometheus-elasticache-sd --output.file=/path/to/elasticache_sd.json --refresh.interval=120
```


## Metadata

The following meta labels are available on targets during relabeling:

- `__meta_elasticache_availability_zone`: the availability zone in which the instance is running
- `__meta_elasticache_engine`: the ElastiCache engine name
- `__meta_elasticache_engine_version`: the ElastiCache engine version
- `__meta_elasticache_cluster_id`: the ElastiCache cluster ID
- `__meta_elasticache_node_id`: the ElastiCache node ID
- `__meta_elasticache_instance_state`: the state of the ElastiCache instance
- `__meta_elasticache_instance_type`: the type of the ElastiCache instance
- `__meta_elasticache_tag_<tagkey>`: each tag value of the instance

## Output example

```
[
    {
        "targets": [
            "elasticache-example-001.abcdef.0001.apne1.cache.amazonaws.com:6379"
        ],
        "labels": {
            "__address__": "elasticache-example-001.abcdef.0001.apne1.cache.amazonaws.com:6379",
            "__meta_elasticache_availability_zone": "ap-northeast-1a",
            "__meta_elasticache_cluster_id": "elasticache-example-001",
            "__meta_elasticache_endpoint_address": "elasticache-example-001.abcdef.0001.apne1.cache.amazonaws.com",
            "__meta_elasticache_endpoint_port": "6379",
            "__meta_elasticache_engine": "redis",
            "__meta_elasticache_engine_version": "2.8.24",
            "__meta_elasticache_instance_state": "available",
            "__meta_elasticache_instance_type": "cache.r3.large",
            "__meta_elasticache_node_id": "0001",
            "__meta_elasticache_tag_Environment": "development"
        }
    },
]
```

