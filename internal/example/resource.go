// Copyright 2020 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.
package example

import (
	"strconv"
	"time"

	"github.com/golang/protobuf/ptypes"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
)

func makeCluster(r snapshotRecipe) *cluster.Cluster {
	return &cluster.Cluster{
		Name:                 r.clusterName,
		ConnectTimeout:       ptypes.DurationProto(5 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_LOGICAL_DNS},
		LbPolicy:             cluster.Cluster_ROUND_ROBIN,
		LoadAssignment:       makeEndpoint(r),
		DnsLookupFamily:      cluster.Cluster_V4_ONLY,
	}
}

func makeEndpoint(r snapshotRecipe) *endpoint.ClusterLoadAssignment {
	return &endpoint.ClusterLoadAssignment{
		ClusterName: r.clusterName,
		Endpoints: []*endpoint.LocalityLbEndpoints{{
			LbEndpoints: []*endpoint.LbEndpoint{{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Protocol: core.SocketAddress_TCP,
									Address:  r.upstreamHost,
									PortSpecifier: &core.SocketAddress_PortValue{
										PortValue: r.upstreamPort,
									},
								},
							},
						},
					},
				},
			}},
		}},
	}
}

func makeRoute(r snapshotRecipe) *route.RouteConfiguration {
	return &route.RouteConfiguration{
		Name: r.routeName,
		VirtualHosts: []*route.VirtualHost{{
			Name:    "local_service",
			Domains: []string{"*"},
			Routes: []*route.Route{{
				Match: &route.RouteMatch{
					PathSpecifier: &route.RouteMatch_Prefix{
						Prefix: "/",
					},
				},
				Action: &route.Route_Route{
					Route: &route.RouteAction{
						ClusterSpecifier: &route.RouteAction_Cluster{
							Cluster: r.clusterName,
						},
						HostRewriteSpecifier: &route.RouteAction_HostRewriteLiteral{
							HostRewriteLiteral: r.upstreamHost,
						},
					},
				},
			}},
		}},
	}
}

func makeHTTPListener(r snapshotRecipe) *listener.Listener {
	// HTTP filter configuration
	manager := &hcm.HttpConnectionManager{
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: "http",
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource:    makeConfigSource(),
				RouteConfigName: r.routeName,
			},
		},
		HttpFilters: []*hcm.HttpFilter{{
			Name: wellknown.Router,
		}},
	}
	pbst, err := ptypes.MarshalAny(manager)
	if err != nil {
		panic(err)
	}

	return &listener.Listener{
		Name: r.listenerName,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: r.listenerPort,
					},
				},
			},
		},
		FilterChains: []*listener.FilterChain{{
			Filters: []*listener.Filter{{
				Name: wellknown.HTTPConnectionManager,
				ConfigType: &listener.Filter_TypedConfig{
					TypedConfig: pbst,
				},
			}},
		}},
	}
}

func makeConfigSource() *core.ConfigSource {
	source := &core.ConfigSource{}
	source.ResourceApiVersion = resource.DefaultAPIVersion
	source.ConfigSourceSpecifier = &core.ConfigSource_ApiConfigSource{
		ApiConfigSource: &core.ApiConfigSource{
			TransportApiVersion:       resource.DefaultAPIVersion,
			ApiType:                   core.ApiConfigSource_GRPC,
			SetNodeOnFirstMessageOnly: true,
			GrpcServices: []*core.GrpcService{{
				TargetSpecifier: &core.GrpcService_EnvoyGrpc_{
					EnvoyGrpc: &core.GrpcService_EnvoyGrpc{ClusterName: "xds_cluster"},
				},
			}},
		},
	}
	return source
}

type snapshotRecipe struct {
	clusterName  string
	routeName    string
	listenerName string
	listenerPort uint32
	upstreamHost string
	upstreamPort uint32
}

func getBasicRecipes() []snapshotRecipe {
	return []snapshotRecipe{
		snapshotRecipe{
			clusterName:  "envoy_proxy_cluster",
			routeName:    "local_route_0",
			listenerName: "listener_0",
			listenerPort: uint32(10000),
			upstreamHost: "www.envoyproxy.io",
			upstreamPort: uint32(80),
		},
		snapshotRecipe{
			clusterName:  "github_proxy_cluster",
			routeName:    "local_route_1",
			listenerName: "listener_1",
			listenerPort: uint32(10001),
			upstreamHost: "github.com",
			upstreamPort: uint32(80),
		},
		snapshotRecipe{
			clusterName:  "greymatter_proxy_cluster",
			routeName:    "local_route_2",
			listenerName: "listener_2",
			listenerPort: uint32(10002),
			upstreamHost: "greymatter.io",
			upstreamPort: uint32(80),
		},
		snapshotRecipe{
			clusterName:  "envoy_proxy_cluster",
			routeName:    "local_route_3",
			listenerName: "listener_3",
			listenerPort: uint32(10003),
			upstreamHost: "www.envoyproxy.io",
			upstreamPort: uint32(80),
		},
		snapshotRecipe{
			clusterName:  "github_proxy_cluster",
			routeName:    "local_route_0",
			listenerName: "listener_4",
			listenerPort: uint32(10004),
			upstreamHost: "github.com",
			upstreamPort: uint32(80),
		},
		snapshotRecipe{
			clusterName:  "greymatter_proxy_cluster",
			routeName:    "local_route",
			listenerName: "listener_5",
			listenerPort: uint32(10005),
			upstreamHost: "greymatter.io",
			upstreamPort: uint32(80),
		},
	}

}

func createSnapshot(recipes []snapshotRecipe, version int) cache.Snapshot {
	var endpoints []types.Resource
	var clusters []types.Resource
	var routes []types.Resource
	var listeners []types.Resource
	var runtimes []types.Resource
	var secrets []types.Resource
	var extensions []types.Resource

	for _, r := range recipes {
		clusters = append(clusters, makeCluster(r))
		routes = append(routes, makeRoute(r))
		listeners = append(listeners, makeHTTPListener(r))

	}

	return cache.NewSnapshot(strconv.Itoa(version), endpoints, clusters, routes, listeners, runtimes, secrets, extensions)
}

func CreateSnapshots(counts int) []cache.Snapshot {
	var snapshots []cache.Snapshot
	recipes := getBasicRecipes()
	for i := 0; i < counts; i++ {
		s := createSnapshot(recipes[0:(i%6)], i)
		snapshots = append(snapshots, s)
	}
	return snapshots
}
