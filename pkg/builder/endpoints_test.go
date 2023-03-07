package builder_test

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/envoyproxy/go-control-plane/pkg/builder"
	"github.com/stretchr/testify/assert"
	structpb "google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	localhost = "127.0.0.1"
	hostname  = "my-hostname.com"
)

func TestLBEndpoints(t *testing.T) {
	tests := map[string]struct {
		opts []builder.LBEndpointOpt
		want *envoyendpoint.LbEndpoint
	}{
		"noop": {
			opts: []builder.LBEndpointOpt{},
			want: &envoyendpoint.LbEndpoint{},
		},
		"with_lb_weight": {
			opts: []builder.LBEndpointOpt{
				builder.WithLBWeight(10),
			},
			want: &envoyendpoint.LbEndpoint{
				LoadBalancingWeight: &wrapperspb.UInt32Value{
					Value: uint32(10),
				},
			},
		},
		"with_health_status": {
			opts: []builder.LBEndpointOpt{
				builder.WithHealthStatus(corev3.HealthStatus_HEALTHY),
			},
			want: &envoyendpoint.LbEndpoint{
				HealthStatus: corev3.HealthStatus_HEALTHY,
			},
		},
		"with_metadata": {
			opts: []builder.LBEndpointOpt{
				builder.WithMetadata(&corev3.Metadata{
					FilterMetadata: make(map[string]*structpb.Struct),
				}),
			},
			want: &envoyendpoint.LbEndpoint{
				Metadata: &corev3.Metadata{
					FilterMetadata: make(map[string]*structpb.Struct),
				},
			},
		},
		"with_endpoint": {
			opts: []builder.LBEndpointOpt{
				builder.WithEndpoint(&envoyendpoint.Endpoint{
					Hostname: hostname,
				}),
			},
			want: &envoyendpoint.LbEndpoint{
				HostIdentifier: &envoyendpoint.LbEndpoint_Endpoint{
					Endpoint: &envoyendpoint.Endpoint{
						Hostname: hostname,
					},
				},
			},
		},
		"with_named_endpoint": {
			opts: []builder.LBEndpointOpt{
				builder.WithNamedEndpoint("my-endpoint"),
			},
			want: &envoyendpoint.LbEndpoint{
				HostIdentifier: &envoyendpoint.LbEndpoint_EndpointName{
					EndpointName: "my-endpoint",
				},
			},
		},
		"with_lots_of_opts": {
			opts: []builder.LBEndpointOpt{
				builder.WithLBWeight(10),
				builder.WithHealthStatus(corev3.HealthStatus_HEALTHY),
				builder.WithEndpoint(&envoyendpoint.Endpoint{
					Hostname: hostname,
				}),
			},
			want: &envoyendpoint.LbEndpoint{
				LoadBalancingWeight: &wrapperspb.UInt32Value{
					Value: uint32(10),
				},
				HealthStatus: corev3.HealthStatus_HEALTHY,
				HostIdentifier: &envoyendpoint.LbEndpoint_Endpoint{
					Endpoint: &envoyendpoint.Endpoint{
						Hostname: hostname,
					},
				},
			},
		},
	}

	// Run through our test cases
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := builder.NewLbEndpoint(tc.opts...)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEndpoints(t *testing.T) {
	tests := map[string]struct {
		opts []builder.EndpointOpt
		want *envoyendpoint.Endpoint
	}{
		"noop": {
			opts: []builder.EndpointOpt{},
			want: &envoyendpoint.Endpoint{},
		},
		"with_socket_address": {
			opts: []builder.EndpointOpt{
				builder.WithSocketAddress(localhost, 8080, corev3.SocketAddress_TCP),
			},
			want: &envoyendpoint.Endpoint{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Protocol: corev3.SocketAddress_TCP,
							Address:  localhost,
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: uint32(8080),
							},
						},
					},
				},
			},
		},
		"with_hostname": {
			opts: []builder.EndpointOpt{
				builder.WithHostname(hostname),
			},
			want: &envoyendpoint.Endpoint{
				Hostname: hostname,
			},
		},
		"with_active_health_check_overrides": {
			opts: []builder.EndpointOpt{
				builder.WithActiveHealthCheckOverrides(hostname, 8080, true),
			},
			want: &envoyendpoint.Endpoint{
				HealthCheckConfig: &envoyendpoint.Endpoint_HealthCheckConfig{
					DisableActiveHealthCheck: true,
					Hostname:                 hostname,
					PortValue:                uint32(8080),
				},
			},
		},
		"with_lots_of_opts": {
			opts: []builder.EndpointOpt{
				builder.WithActiveHealthCheckOverrides(hostname, 8080, true),
				builder.WithSocketAddress(localhost, 8080, corev3.SocketAddress_TCP),
				builder.WithHostname(hostname),
			},
			want: &envoyendpoint.Endpoint{
				HealthCheckConfig: &envoyendpoint.Endpoint_HealthCheckConfig{
					DisableActiveHealthCheck: true,
					Hostname:                 hostname,
					PortValue:                uint32(8080),
				},
				Hostname: hostname,
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Protocol: corev3.SocketAddress_TCP,
							Address:  localhost,
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: uint32(8080),
							},
						},
					},
				},
			},
		},
	}

	// Run through our test cases
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := builder.NewEndpoint(tc.opts...)
			assert.Equal(t, tc.want, got)
		})
	}
}
