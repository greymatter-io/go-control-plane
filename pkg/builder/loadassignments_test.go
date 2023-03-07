package builder_test

import (
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/envoyproxy/go-control-plane/pkg/builder"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	clusterName = "test-cluster"
)

func TestClusterLoadAssignments(t *testing.T) {
	tests := map[string]struct {
		opts []builder.CLAOpt
		want *envoyendpoint.ClusterLoadAssignment
	}{
		"noop": {
			opts: []builder.CLAOpt{},
			want: &envoyendpoint.ClusterLoadAssignment{},
		},
		"with_cluster_name": {
			opts: []builder.CLAOpt{
				builder.WithClusterName(clusterName),
			},
			want: &envoyendpoint.ClusterLoadAssignment{
				ClusterName: clusterName,
			},
		},
		"with_load_assignment_policy": {
			opts: []builder.CLAOpt{
				builder.WithLoadAssignmentPolicy(100, time.Hour*1, map[string]int{
					"throttle": 10,
					"lb":       50,
				}),
			},
			want: &envoyendpoint.ClusterLoadAssignment{
				Policy: &envoyendpoint.ClusterLoadAssignment_Policy{
					OverprovisioningFactor: &wrapperspb.UInt32Value{Value: uint32(100)},
					EndpointStaleAfter:     durationpb.New(time.Hour * 1),
					DropOverloads: []*envoyendpoint.ClusterLoadAssignment_Policy_DropOverload{
						{
							Category: "throttle",
							DropPercentage: &typev3.FractionalPercent{
								Numerator: uint32(10),
							},
						},
						{
							Category: "lb",
							DropPercentage: &typev3.FractionalPercent{
								Numerator: uint32(50),
							},
						},
					},
				},
			},
		},
		"with_lots_of_opts": {
			opts: []builder.CLAOpt{
				builder.WithLoadAssignmentPolicy(100, time.Hour*1, map[string]int{
					"throttle": 10,
					"lb":       50,
				}),
				builder.WithClusterName(clusterName),
				builder.WithLocalityLBEndpoints([]*envoyendpoint.LocalityLbEndpoints{}),
			},
			want: &envoyendpoint.ClusterLoadAssignment{
				Policy: &envoyendpoint.ClusterLoadAssignment_Policy{
					OverprovisioningFactor: &wrapperspb.UInt32Value{Value: uint32(100)},
					EndpointStaleAfter:     durationpb.New(time.Hour * 1),
					DropOverloads: []*envoyendpoint.ClusterLoadAssignment_Policy_DropOverload{
						{
							Category: "throttle",
							DropPercentage: &typev3.FractionalPercent{
								Numerator: uint32(10),
							},
						},
						{
							Category: "lb",
							DropPercentage: &typev3.FractionalPercent{
								Numerator: uint32(50),
							},
						},
					},
				},
				ClusterName: clusterName,
				Endpoints:   make([]*envoyendpoint.LocalityLbEndpoints, 0),
			},
		},
	}

	// Run through our test cases
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := builder.NewLoadAssignment(tc.opts...)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLocalityLBEndpoints(t *testing.T) {
	tests := map[string]struct {
		opts []builder.LLEOpt
		want *envoyendpoint.LocalityLbEndpoints
	}{
		"noop": {
			opts: []builder.LLEOpt{},
			want: &envoyendpoint.LocalityLbEndpoints{},
		},
		"with_locality": {
			opts: []builder.LLEOpt{
				builder.WithLocality("us-east", "us-east-1", "a"),
			},
			want: &envoyendpoint.LocalityLbEndpoints{
				Locality: &corev3.Locality{
					Region:  "us-east",
					Zone:    "us-east-1",
					SubZone: "a",
				},
			},
		},
		"with_priority": {
			opts: []builder.LLEOpt{
				builder.WithPriority(1),
				builder.WithLocalityLBWeight(70),
			},
			want: &envoyendpoint.LocalityLbEndpoints{
				Priority: uint32(1),
				LoadBalancingWeight: &wrapperspb.UInt32Value{
					Value: uint32(70),
				},
			},
		},
		"with_lots_of_opts": {
			opts: []builder.LLEOpt{
				builder.WithPriority(1),
				builder.WithLocalityLBWeight(70),
				builder.WithLocality("us-east", "us-east-1", "a"),
				builder.WithEndpoints([]*envoyendpoint.LbEndpoint{}),
			},
			want: &envoyendpoint.LocalityLbEndpoints{
				Priority: uint32(1),
				LoadBalancingWeight: &wrapperspb.UInt32Value{
					Value: uint32(70),
				},
				Locality: &corev3.Locality{
					Region:  "us-east",
					Zone:    "us-east-1",
					SubZone: "a",
				},
				LbEndpoints: make([]*envoyendpoint.LbEndpoint, 0),
			},
		},
	}

	// Run through our test cases
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := builder.NewLocalityEndpoints(tc.opts...)
			assert.Equal(t, tc.want, got)
		})
	}
}
