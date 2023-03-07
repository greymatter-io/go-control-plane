package builder

import (
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// CLAOpt provides a hook to modify a load assignment given pre-defined
// builder methods. This provides the ability to abstract large boilerplate
// while building envoy proto objects. The goal is to provide users of the library
// the ability to write business logic around what needs to be built rather
// than focusing on building the objects.
type CLAOpt func(*envoyendpoint.ClusterLoadAssignment)

// WithLoadAssignmentPolicy let's users configure the linked items:
// https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/endpoint/v3/endpoint.proto#envoy-v3-api-msg-config-endpoint-v3-clusterloadassignment-policy
func WithLoadAssignmentPolicy(overprovisionFactor int, staleFactor time.Duration, dropPercentages map[string]int) func(*envoyendpoint.ClusterLoadAssignment) {
	return func(cla *envoyendpoint.ClusterLoadAssignment) {
		cla.Policy = &envoyendpoint.ClusterLoadAssignment_Policy{
			OverprovisioningFactor: &wrapperspb.UInt32Value{Value: uint32(overprovisionFactor)},
			EndpointStaleAfter:     durationpb.New(staleFactor),
			DropOverloads:          make([]*envoyendpoint.ClusterLoadAssignment_Policy_DropOverload, 0),
		}

		//  Loop through all the drop protection mechanisms and add them to our
		// global load assignment policy.
		for category, percent := range dropPercentages {
			cla.Policy.DropOverloads = append(cla.Policy.DropOverloads, &envoyendpoint.ClusterLoadAssignment_Policy_DropOverload{
				Category: category,
				// No denominator is supplied fractional percentages to inherit
				// the default of numerator/100.
				DropPercentage: &typev3.FractionalPercent{
					Numerator: uint32(percent),
				},
			})
		}
	}
}

// WithClusterName specifies the cluster that this load assignment
// will be assigned to. Clusters can only have one load assignment.
func WithClusterName(name string) func(*envoyendpoint.ClusterLoadAssignment) {
	return func(cla *envoyendpoint.ClusterLoadAssignment) {
		cla.ClusterName = name
	}
}

func WithLocalityLBEndpoints(llEndpoints []*envoyendpoint.LocalityLbEndpoints) func(*envoyendpoint.ClusterLoadAssignment) {
	return func(cla *envoyendpoint.ClusterLoadAssignment) {
		cla.Endpoints = llEndpoints
	}
}

// NewLoadAssignment constructor builds the target LA
// given the provided optional values from the users input.
func NewLoadAssignment(opts ...CLAOpt) *envoyendpoint.ClusterLoadAssignment {
	var cla envoyendpoint.ClusterLoadAssignment

	// Apply our user inputs to the load assignment.
	for _, opt := range opts {
		opt(&cla)
	}

	return &cla
}

// LLEOpt provides a hook to modify a LocalityLBEndpoint given pre-defined
// builder methods. This provides the ability to abstract large boilerplate
// while building envoy proto objects. The goal is to provide users of the library
// the ability to write business logic around what needs to be built rather
// than focusing on building the objects.
type LLEOpt func(*envoyendpoint.LocalityLbEndpoints)

func WithLocality(region, zone, subzone string) func(*envoyendpoint.LocalityLbEndpoints) {
	return func(lle *envoyendpoint.LocalityLbEndpoints) {
		lle.Locality = &corev3.Locality{
			Region:  region,
			Zone:    zone,
			SubZone: subzone,
		}
	}
}

func WithPriority(priority int) func(*envoyendpoint.LocalityLbEndpoints) {
	return func(lle *envoyendpoint.LocalityLbEndpoints) {
		lle.Priority = uint32(priority)
	}
}

func WithLocalityLBWeight(weight int) func(*envoyendpoint.LocalityLbEndpoints) {
	return func(lle *envoyendpoint.LocalityLbEndpoints) {
		lle.LoadBalancingWeight = &wrapperspb.UInt32Value{Value: uint32(weight)}
	}
}

func WithEndpoints(endpoints []*envoyendpoint.LbEndpoint) func(*envoyendpoint.LocalityLbEndpoints) {
	return func(lle *envoyendpoint.LocalityLbEndpoints) {
		lle.LbEndpoints = endpoints
	}
}

func WithLoadBalancedEndpoints(endpoints []*envoyendpoint.LbEndpoint) func(*envoyendpoint.LocalityLbEndpoints) {
	return func(lle *envoyendpoint.LocalityLbEndpoints) {
		lle.LbConfig = &envoyendpoint.LocalityLbEndpoints_LoadBalancerEndpoints{
			LoadBalancerEndpoints: &envoyendpoint.LocalityLbEndpoints_LbEndpointList{
				LbEndpoints: endpoints,
			},
		}

		// TODO: LEDS isn't inplemented yet in envoy/go-control-plane, but when it is, use this config below.
		// LEDS -> load balancer eDS.
		// lle.LbConfig = &envoyendpoint.LocalityLbEndpoints_LedsClusterLocalityConfig{
		// 	LedsClusterLocalityConfig: &envoyendpoint.LedsClusterLocalityConfig{
		// 		LedsConfig:         nil,
		// 		LedsCollectionName: "TODO: IMPLiMENT_ME_WHEN_READY",
		// 	},
		// }
	}
}

// NewLocalityEndpoints constructor builds the target LLE
// given the provided optional values from the users input.
func NewLocalityEndpoints(opts ...LLEOpt) *envoyendpoint.LocalityLbEndpoints {
	var lle envoyendpoint.LocalityLbEndpoints

	// Apply our user inputs to the locality endpoints.
	for _, opt := range opts {
		opt(&lle)
	}

	return &lle
}
